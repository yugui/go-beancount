package quote

import (
	"context"
	"errors"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// FetchLatest resolves each PriceRequest to the newest price the
// fallback chain can produce. It walks each request's fallback chain
// in synchronised levels until each request is either resolved (a
// Price was returned) or exhausted (every Source in the chain has
// been tried).
//
// # Level-by-level scheduling
//
// Scheduling proceeds in levels. At level k every still-unresolved
// unit contributes its k-th-priority source name to a
// {sourceName -> []unit} grouping; the orchestrator then issues, in
// parallel, exactly one call per source on that level — carrying
// every query for that source in a single batch and (for range mode)
// the full requested interval. Source-side splitting by query count
// or by date range is the source author's responsibility, expressed
// at registration time via the helpers in pkg/quote/sourceutil.
// After the entire level finishes, results are merged in: units that
// came back with a Price are marked done; units that did not advance
// to the next fallback in their Sources slice. Level k+1 then begins
// with whatever units remain unresolved.
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
// chains share the same downstream sources. The orchestrator
// deliberately picks the barrier.
//
// # Capability ↔ Mode demotion
//
// FetchLatest picks the source method to call by combining ModeLatest
// with the source's declared Capabilities:
//
//	source declares  | call issued
//	-----------------+---------------------------------------------
//	LatestSource     | QuoteLatest
//	AtSource only    | QuoteAt(now)
//	RangeSource only | QuoteRange(now-1d, now+1d) and pick the
//	                 | latest entry
//	multi-impl       | prefer Latest > At > Range
//
// "now" is supplied by WithClock (default time.Now).
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
// the cancelled ctx through their own arguments, and FetchLatest
// returns ctx.Err() without leaking goroutines.
//
// # Error policy
//
//   - A Source method returning a non-nil error or panicking is
//     converted into a "quote-fetch-error" Diagnostic with severity
//     Error; the affected unit's depth advances to the next fallback.
//   - An unknown source name on a unit's chain produces a
//     "quote-source-unknown" Diagnostic with severity Error; the unit
//     advances to the next fallback.
//   - A mode unsupported by the entry the unit is currently trying
//     produces a "quote-mode-unsupported" Diagnostic with severity
//     Warning; the unit advances to the next fallback.
//   - FetchLatest itself returns a non-nil error only when ctx.Err()
//     != nil or when the cumulative result contains zero ast.Price
//     entries. If at least one Price came back, the per-unit failures
//     are in the diagnostic slice and the error return is nil.
func FetchLatest(ctx context.Context, reg Registry, requests []api.PriceRequest, opts ...Option) ([]ast.Price, []ast.Diagnostic, error) {
	cfg := applyOpts(opts)
	return runFetch(ctx, reg, requests, latestPlanner, cfg)
}

// FetchAt resolves each PriceRequest to the price as of at, walking
// each request's fallback chain in synchronised levels until it is
// either resolved or exhausted. See [FetchLatest] for the
// level-by-level scheduling and deadlock-avoidance discussion that
// applies to all three entry points.
//
// at is a TZ-naïve calendar date conventionally constructed at 0:00
// UTC on the desired day (i.e. time.Date(y, m, d, 0, 0, 0, 0,
// time.UTC)). Projecting that calendar date onto a source-native
// exchange time zone is the responsibility of each individual quoter.
//
// # Capability ↔ Mode demotion
//
// FetchAt picks the source method to call by combining ModeAt with
// the source's declared Capabilities:
//
//	source declares  | call issued
//	-----------------+---------------------------------------------
//	AtSource         | QuoteAt(at)
//	RangeSource only | QuoteRange(at, at+1d) and filter
//	LatestSource     | QuoteLatest only when now ∈ [at, at+1d);
//	  only           | else quote-mode-unsupported diagnostic
//	multi-impl       | prefer At > Range > Latest
//
// "now" is supplied by WithClock (default time.Now).
func FetchAt(ctx context.Context, reg Registry, requests []api.PriceRequest, at time.Time, opts ...Option) ([]ast.Price, []ast.Diagnostic, error) {
	cfg := applyOpts(opts)
	return runFetch(ctx, reg, requests, atPlanner(at), cfg)
}

// FetchRange resolves each PriceRequest to a series of prices over
// the half-open calendar-date interval [start, end), walking each
// request's fallback chain in synchronised levels until it is either
// resolved or exhausted. See [FetchLatest] for the level-by-level
// scheduling and deadlock-avoidance discussion that applies to all
// three entry points.
//
// start and end are TZ-naïve calendar dates conventionally
// constructed at 0:00 UTC. The interval is half-open: prices on start
// are included, prices on end are not.
//
// # Argument validation
//
// FetchRange validates its time arguments at the entry point. If
// start is not strictly before end (i.e. the interval is empty or
// inverted), it returns ErrInvalidRange without consulting the
// registry. The previous Spec-shaped API silently produced
// ErrZeroPrices in this case, which conflated user error with a
// successful fetch that legitimately found nothing; the explicit
// validation error is more honest.
//
// # Capability ↔ Mode demotion
//
// FetchRange picks the source method to call by combining ModeRange
// with the source's declared Capabilities:
//
//	source declares  | call issued
//	-----------------+---------------------------------------------
//	RangeSource      | QuoteRange(start, end)
//	AtSource only    | QuoteRange(start, end) via
//	                 | sourceutil.DateRangeIter (Calendar=AllDays)
//	LatestSource     | QuoteLatest only when now ∈ [start, end);
//	  only           | else quote-mode-unsupported diagnostic
//	multi-impl       | prefer Range > At-lifted > Latest
//
// "now" is supplied by WithClock (default time.Now). For the
// AtSource-only case, the orchestrator lifts the AtSource to a
// RangeSource via sourceutil.DateRangeIter with Calendar=AllDays and
// issues a single QuoteRange call covering [start, end); the lift
// iterates calendar days internally and stops at the first per-day
// error. Source authors who need calendar-aware iteration (e.g.
// WeekdaysOnly for FX so that weekend "no data" is skipped instead
// of aborting the range) should register a RangeSource themselves —
// typically by composing sourceutil.DateRangeIter with the
// appropriate Calendar — rather than relying on this fallback.
func FetchRange(ctx context.Context, reg Registry, requests []api.PriceRequest, start, end time.Time, opts ...Option) ([]ast.Price, []ast.Diagnostic, error) {
	if !start.Before(end) {
		return nil, nil, ErrInvalidRange
	}
	cfg := applyOpts(opts)
	return runFetch(ctx, reg, requests, rangePlanner(start, end), cfg)
}

// Option configures a single Fetch* call. Options are applied in
// order; later options override earlier ones for the same setting.
type Option func(*runConfig)

// runConfig collects the per-call knobs the Fetch* entry points
// consult. It is private so the public surface is just the Option
// setters.
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

// applyOpts builds a runConfig from defaults and applies each Option
// in order. Each Fetch* entry point delegates option resolution here
// so the three call sites do not drift in lockstep.
func applyOpts(opts []Option) *runConfig {
	cfg := defaultRunConfig()
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

// WithConcurrency caps the number of concurrent Source method calls
// the Fetch* entry points will issue across all sources combined. A
// value <= 0 is silently clamped to 1. The default is 32.
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

// WithClock supplies the current time used by the Fetch* entry
// points for Latest ↔ At/Range demotion decisions. The default is
// time.Now. Tests override this to make the demotion table
// deterministic.
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

// ErrZeroPrices is the sentinel returned by the Fetch* entry points
// when every unit failed and the cumulative result has no ast.Price
// entries. The caller can use errors.Is to detect this case if it
// cares to distinguish it from a context cancellation.
var ErrZeroPrices = errors.New("quote: no prices fetched")

// ErrInvalidRange is the sentinel returned by FetchRange when its
// start/end arguments do not form a non-empty half-open interval
// (i.e. !start.Before(end)). The caller can use errors.Is to
// distinguish this input-validation failure from a successful fetch
// that produced no prices (ErrZeroPrices).
var ErrInvalidRange = errors.New("quote: range end must be strictly after start")
