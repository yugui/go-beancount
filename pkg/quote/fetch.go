package quote

import (
	"context"
	"errors"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// Fetch resolves the price requests in spec by dispatching them to
// sources from reg, walking each request's fallback chain in
// synchronised levels until each request is either resolved (a Price
// was returned) or exhausted (every Source in the chain has been
// tried).
//
// # Level-by-level scheduling
//
// Scheduling proceeds in levels. At level k every still-unresolved
// unit contributes its k-th-priority source name to a
// {sourceName -> []unit} grouping; the orchestrator then issues, in
// parallel, exactly one call per source on that level — carrying
// every query for that source in a single batch and (for
// ModeRange) the full requested interval. Source-side splitting by
// query count or by date range is the source author's
// responsibility, expressed at registration time via the helpers
// in pkg/quote/sourceutil. After the entire level finishes, results
// are merged in: units that came back with a Price are marked done;
// units that did not advance to the next fallback in their Sources
// slice. Level k+1 then begins with whatever units remain
// unresolved.
//
// The synchronised barrier between levels is what makes the fallback
// semantics safe under shared batch sources. Consider two requests A
// and B sharing two batch sources in opposite priorities (A=[yahoo,
// google], B=[google, yahoo]). With the barrier, level 0 hits yahoo
// with [A] and google with [B] in parallel, both exactly once; level 1
// hits yahoo with [B] and google with [A] iff the level-0 calls did
// not yield prices. Without the barrier — for example with a "start
// primary; on failure ask fallback" implementation that did not
// coordinate — A's google fallback batch would have to wait on the
// completion of B's google batch, while B's yahoo fallback would have
// to wait on A's yahoo batch, and a circular wait between the batches
// is structurally possible.
//
// A speculative alternative (start primary AND fallback in parallel,
// drop fallback if primary succeeds) trades the barrier away at the
// cost of unbounded fallback fan-out the moment several priority
// chains share the same downstream sources. Fetch deliberately picks
// the barrier.
//
// # Capability ↔ Mode demotion
//
// The orchestrator picks the source method to call by combining
// spec.Mode with the source's declared Capabilities. The table below
// is exhaustive for the four declared-axis combinations; "Multi-impl"
// covers any source that declares more than one of SupportsLatest,
// SupportsAt, SupportsRange.
//
//	spec.Mode \ source         | LatestSource only | AtSource only      | RangeSource only        | Multi-impl
//	---------------------------+-------------------+--------------------+-------------------------+----------------------
//	ModeLatest                 | QuoteLatest       | QuoteAt(now)       | QuoteRange(now-1d,      | Prefer
//	                           |                   |                    | now+1d) and pick the    | Latest > At > Range
//	                           |                   |                    | latest entry            |
//	ModeAt(t)                  | quote-mode-       | QuoteAt(t)         | QuoteRange(t, t+1d) and | Prefer
//	                           | unsupported       |                    | filter                  | At > Range > Latest
//	                           | unless now ∈      |                    |                         |
//	                           | [t, t+1d); else   |                    |                         |
//	                           | QuoteLatest       |                    |                         |
//	ModeRange(s, e)            | QuoteLatest only  | one QuoteAt per    | QuoteRange(s, e)        | Prefer
//	                           | when now ∈ [s,e), | calendar day in    |                         | Range > At-loop > Latest
//	                           | else quote-mode-  | [s, e)             |                         |
//	                           | unsupported       |                    |                         |
//
// "now" is supplied by WithClock (default time.Now). The "calendar
// day loop" inside ModeRange + AtSource-only is run by the
// orchestrator one calendar day at a time across [s, e); per-day no-
// result calls turn into Diagnostics rather than top-level errors, so
// non-business-day calendars degrade gracefully.
//
// A ModeRange request with Start >= End is treated as a vacuous
// request: no calls are issued to any source and no Diagnostics are
// produced. Because no prices are obtained, Fetch returns
// errZeroPrices in this case.
//
// # Concurrency
//
// A single semaphore of size WithConcurrency (default 32) bounds the
// total number of in-flight Source method calls across all sources.
// Within a level, the dispatch loop acquires a slot just before each
// call and releases it as the call returns; between levels the
// semaphore is naturally drained because the level barrier waits for
// every spawned call to finish. ctx.Done() propagates: any pending
// dispatch loop returns ctx.Err() promptly, in-flight calls observe
// the cancelled ctx through their own arguments, and Fetch returns
// ctx.Err() without leaking goroutines.
//
// # Error policy
//
//   - A Source method returning a non-nil error or panicking is
//     converted into a "quote-fetch-error" Diagnostic with severity
//     Error; the affected unit's depth advances to the next fallback.
//   - An unknown source name on a unit's chain produces a
//     "quote-source-unknown" Diagnostic with severity Error; the unit
//     advances to the next fallback.
//   - A spec.Mode unsupported by the entry the unit is currently
//     trying produces a "quote-mode-unsupported" Diagnostic with
//     severity Warning; the unit advances to the next fallback.
//   - Fetch itself returns a non-nil error only when ctx.Err() != nil
//     or when the cumulative result contains zero ast.Price entries.
//     If at least one Price came back, the per-unit failures are in
//     the diagnostic slice and Fetch's error return is nil.
func Fetch(ctx context.Context, reg Registry, spec api.Spec, opts ...Option) ([]ast.Price, []ast.Diagnostic, error) {
	cfg := defaultRunConfig()
	for _, o := range opts {
		o(cfg)
	}
	return runFetch(ctx, reg, spec, cfg)
}

// Option configures a single Fetch call. Options are applied in
// order; later options override earlier ones for the same setting.
type Option func(*runConfig)

// runConfig collects the per-call knobs Fetch consults. It is private
// so the public surface is just the Option setters.
type runConfig struct {
	// concurrency is the cap on simultaneous in-flight source method
	// calls across all sources combined.
	concurrency int
	// now returns the current time for Latest ↔ At/Range demotion
	// decisions. WithClock substitutes a deterministic value in
	// tests.
	now func() time.Time
	// observer, if non-nil, receives an Event for each level
	// boundary and for each call start/end.
	observer func(Event)
}

func defaultRunConfig() *runConfig {
	return &runConfig{
		concurrency: 32,
		now:         time.Now,
		observer:    nil,
	}
}

// WithConcurrency caps the number of concurrent Source method calls
// that Fetch will issue across all sources combined. A value <= 0 is
// silently clamped to 1. The default is 32.
//
// The default is sized for IO-bound sources rate-limited at roughly 1
// req/s where blocking goroutines are cheap; CPU-bound conventional
// values like GOMAXPROCS or 4 are deliberately not used here.
func WithConcurrency(n int) Option {
	return func(c *runConfig) {
		if n <= 0 {
			n = 1
		}
		c.concurrency = n
	}
}

// WithClock supplies the current time used by Fetch for Latest ↔
// At/Range demotion decisions. The default is time.Now. Tests
// override this to make the demotion table deterministic.
func WithClock(now func() time.Time) Option {
	return func(c *runConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// WithObserver registers a callback invoked once per Source call (and
// for level-boundary events). The callback runs synchronously on the
// goroutine of the originating scheduling step and must not block; if
// expensive work is needed, fan it out to a separate goroutine. The
// callback may be invoked concurrently from multiple goroutines (one
// per source per level), so the implementation must be goroutine-safe.
//
// Multiple WithObserver options compose by replacement: the last
// option in the variadic list wins. Wrap several observer functions
// in a single one if multiplexing is desired.
func WithObserver(fn func(Event)) Option {
	return func(c *runConfig) {
		c.observer = fn
	}
}

// errZeroPrices is the sentinel returned by Fetch when every unit
// failed and the cumulative result has no ast.Price entries. The
// caller can use errors.Is to detect this case if it cares to
// distinguish it from a context cancellation.
var errZeroPrices = errors.New("quote: no prices fetched")
