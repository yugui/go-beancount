package quote

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
	"github.com/yugui/go-beancount/pkg/quote/sourceutil"
)

// unit is the orchestrator's internal scheduling atom: one
// PriceRequest's progress through its fallback chain.
//
// For range mode, a unit covers the whole [start, end) interval at the
// unit level; the orchestrator passes that whole interval through to
// the source in a single call. Source-side chunking (when the source's
// underlying API can't natively span arbitrary ranges) is the source
// author's responsibility, typically expressed by wrapping with
// pkg/quote/sourceutil.SplitRange.
type unit struct {
	// req is the original PriceRequest the unit tracks.
	req api.PriceRequest
	// depth is the index into req.Sources currently being attempted
	// (i.e. the "level" the unit will join in the next round).
	depth int
	// done indicates the unit has either produced at least one Price
	// or exhausted every fallback in req.Sources.
	done bool
	// got reports whether at least one Price was attributed back to
	// this unit, used by the level loop to decide between "succeeded"
	// and "advance to next fallback".
	got bool
}

// plan describes the single batched source call dispatchSource will
// issue for one (level, source) pair. invoke calls the appropriate
// Quote* method on the source with the time arguments captured at
// plan-creation time; mode is the api.Mode reported in the
// EventCallStart / EventCallDone event pair surrounding the call.
type plan struct {
	mode   api.Mode
	invoke func(ctx context.Context, queries []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
}

// planCallFunc is the per-mode strategy that picks which source
// method to call for a given (source, runConfig). Each public Fetch*
// entry point constructs a planCallFunc that owns one row of the
// capability ↔ mode demotion table — latestPlanner for FetchLatest,
// atPlanner(at) for FetchAt, rangePlanner(start, end) for FetchRange
// — and hands it to runFetch. The orchestrator no longer needs to
// switch on the requested mode.
//
// The second return is false when no method on the source can serve
// the planner's mode (mode-unsupported diagnostic territory).
//
// The type assertions inside the returned plan's invoke closure are
// intentionally unchecked: the planner picks the mode based on the
// source's declared Capabilities. If a source lies about its
// Capabilities (e.g. SupportsLatest=true without implementing
// api.LatestSource), the assertion will panic when invoke is called.
// That panic is recovered by runUnderSemaphore and converted into a
// quote-fetch-error Diagnostic, so the affected unit advances to its
// next fallback rather than tearing down the whole orchestrator.
type planCallFunc func(src api.Source, cfg *runConfig) (plan, bool)

// latestPlanner is the planCallFunc used by FetchLatest. Demotion
// order: LatestSource > AtSource(now) > RangeSource(now-1d, now+1d).
func latestPlanner(src api.Source, cfg *runConfig) (plan, bool) {
	caps := src.Capabilities()
	if caps.SupportsLatest {
		return plan{
			mode: api.ModeLatest,
			invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
				return src.(api.LatestSource).QuoteLatest(ctx, qs)
			},
		}, true
	}
	if caps.SupportsAt {
		return plan{
			mode: api.ModeAt,
			invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
				return src.(api.AtSource).QuoteAt(ctx, qs, cfg.now())
			},
		}, true
	}
	if caps.SupportsRange {
		now := cfg.now()
		return plan{
			mode: api.ModeRange,
			invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
				return src.(api.RangeSource).QuoteRange(ctx, qs, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1))
			},
		}, true
	}
	return plan{}, false
}

// atPlanner returns the planCallFunc used by FetchAt for the given
// calendar date. Demotion order: AtSource(at) > RangeSource(at,
// at+1d) > LatestSource (only when cfg.now() ∈ [at, at+1d)).
func atPlanner(at time.Time) planCallFunc {
	return func(src api.Source, cfg *runConfig) (plan, bool) {
		caps := src.Capabilities()
		if caps.SupportsAt {
			return plan{
				mode: api.ModeAt,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					return src.(api.AtSource).QuoteAt(ctx, qs, at)
				},
			}, true
		}
		if caps.SupportsRange {
			return plan{
				mode: api.ModeRange,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					return src.(api.RangeSource).QuoteRange(ctx, qs, at, at.AddDate(0, 0, 1))
				},
			}, true
		}
		if caps.SupportsLatest {
			now := cfg.now()
			if !now.Before(at) && now.Before(at.AddDate(0, 0, 1)) {
				return plan{
					mode: api.ModeLatest,
					invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
						return src.(api.LatestSource).QuoteLatest(ctx, qs)
					},
				}, true
			}
		}
		return plan{}, false
	}
}

// rangePlanner returns the planCallFunc used by FetchRange for the
// given half-open interval [start, end). Demotion order:
// RangeSource(start, end) > AtSource lifted via
// sourceutil.DateRangeIter (Calendar=AllDays) > LatestSource (only
// when cfg.now() ∈ [start, end)).
func rangePlanner(start, end time.Time) planCallFunc {
	return func(src api.Source, cfg *runConfig) (plan, bool) {
		caps := src.Capabilities()
		if caps.SupportsRange {
			return plan{
				mode: api.ModeRange,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					return src.(api.RangeSource).QuoteRange(ctx, qs, start, end)
				},
			}, true
		}
		if caps.SupportsAt {
			return plan{
				mode: api.ModeRange,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					wrapped := sourceutil.DateRangeIter(src.(api.AtSource), sourceutil.AllDays)
					return wrapped.QuoteRange(ctx, qs, start, end)
				},
			}, true
		}
		if caps.SupportsLatest {
			now := cfg.now()
			if !now.Before(start) && now.Before(end) {
				return plan{
					mode: api.ModeLatest,
					invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
						return src.(api.LatestSource).QuoteLatest(ctx, qs)
					},
				}, true
			}
		}
		return plan{}, false
	}
}

// attribute walks the Prices a call returned and marks the
// originating units as got. Prices the call produced that don't
// match any unit's Pair (the "ECB windfall") are still included in
// the final result so the cache decorator can capitalise on them.
func attribute(prices []ast.Price, units []*unit) {
	for _, price := range prices {
		pair := api.Pair{Commodity: price.Commodity, QuoteCurrency: price.Amount.Currency}
		for _, u := range units {
			if u.req.Pair == pair {
				u.got = true
			}
		}
	}
}

// fetchErrDiags turns a non-nil error from a source method into the
// matching quote-fetch-error Diagnostic per affected unit.
func fetchErrDiags(err error, sourceName string, affected []*unit) []ast.Diagnostic {
	out := make([]ast.Diagnostic, 0, len(affected))
	for _, u := range affected {
		out = append(out, ast.Diagnostic{
			Code:     "quote-fetch-error",
			Severity: ast.Error,
			Message:  fmt.Sprintf("quote: source %q failed for pair %s/%s: %v", sourceName, u.req.Pair.Commodity, u.req.Pair.QuoteCurrency, err),
		})
	}
	return out
}

// runUnderSemaphore acquires a slot on sem before running f, releases
// it on return, and converts any panic from f into a synthetic error
// so a single broken caller does not tear down siblings sharing the
// semaphore. ctx cancellation while waiting for the slot returns
// ctx.Err() without invoking f.
//
// The synthetic panic error is unannotated ("panicked: %v"); callers
// are expected to add their own context (the source name, the pair,
// etc.) when surfacing the error. dispatchSource does this via
// fetchErrDiags.
func runUnderSemaphore(
	ctx context.Context,
	sem chan struct{},
	f func() ([]ast.Price, []ast.Diagnostic, error),
) (prices []ast.Price, diags []ast.Diagnostic, err error) {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	defer func() { <-sem }()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked: %v", r)
		}
	}()
	return f()
}

// runFetch is the entry point each FetchX dispatches to once Options
// have been resolved. It owns the level loop, the {sourceName ->
// []unit} grouping, the per-source dispatch goroutines, and the
// cross-source concurrency semaphore. The mode-specific demotion logic
// lives in planner; runFetch is mode-agnostic.
func runFetch(ctx context.Context, reg Registry, requests []api.PriceRequest, planner planCallFunc, cfg *runConfig) ([]ast.Price, []ast.Diagnostic, error) {
	if len(requests) == 0 {
		return nil, nil, ErrZeroPrices
	}

	units := make([]*unit, 0, len(requests))
	for _, r := range requests {
		units = append(units, &unit{req: r})
	}

	sem := make(chan struct{}, cfg.concurrency)

	var (
		mu     sync.Mutex
		prices []ast.Price
		diags  []ast.Diagnostic
	)
	emit := func(ev Event) {
		if cfg.observer != nil {
			cfg.observer(ev)
		}
	}

	for level := 0; ; level++ {
		groups := map[string][]*unit{}
		for _, u := range units {
			if u.done {
				continue
			}
			if u.depth >= len(u.req.Sources) {
				u.done = true
				continue
			}
			name := u.req.Sources[u.depth].Source
			groups[name] = append(groups[name], u)
		}
		if len(groups) == 0 {
			break
		}

		emit(Event{Kind: EventLevelStart, Level: level})

		var wg sync.WaitGroup
		for name, gus := range groups {
			wg.Add(1)
			go func(sourceName string, us []*unit) {
				defer wg.Done()
				localPrices, localDiags := dispatchSource(ctx, reg, planner, cfg, level, sourceName, us, sem, emit)
				mu.Lock()
				prices = append(prices, localPrices...)
				diags = append(diags, localDiags...)
				mu.Unlock()
			}(name, gus)
		}
		wg.Wait()

		// Merge results back into units. A unit is done if it got at
		// least one Price OR if its remaining fallback chain has
		// been exhausted.
		for _, u := range units {
			if u.done {
				continue
			}
			if u.got {
				u.done = true
				continue
			}
			u.depth++
			if u.depth >= len(u.req.Sources) {
				u.done = true
			}
		}

		emit(Event{Kind: EventLevelEnd, Level: level})

		if err := ctx.Err(); err != nil {
			return prices, diags, err
		}
	}

	if len(prices) == 0 {
		return prices, diags, ErrZeroPrices
	}
	return prices, diags, nil
}

// dispatchSource is the per-source workhorse invoked once per
// (level, source) pair. It resolves the source from the registry,
// asks the supplied planner to pick the source method to call (the
// demotion table on each FetchX's godoc), assembles the SourceQuery
// slice (one query per unit), issues the call, converts errors /
// panics / unsupported-mode into Diagnostics, and attributes returned
// Prices back to the contributing units. Every unit going to the
// same source on the same level is dispatched in a single batched
// call; any per-source splitting (by query count or by date range)
// is the source author's responsibility.
func dispatchSource(
	ctx context.Context,
	reg Registry,
	planner planCallFunc,
	cfg *runConfig,
	level int,
	sourceName string,
	units []*unit,
	sem chan struct{},
	emit func(Event),
) ([]ast.Price, []ast.Diagnostic) {
	src, ok := reg.Lookup(sourceName)
	if !ok {
		diags := make([]ast.Diagnostic, 0, len(units))
		for _, u := range units {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-source-unknown",
				Severity: ast.Error,
				Message:  fmt.Sprintf("quote: unknown source %q for pair %s/%s", sourceName, u.req.Pair.Commodity, u.req.Pair.QuoteCurrency),
			})
		}
		return nil, diags
	}

	p, ok := planner(src, cfg)
	if !ok {
		diags := make([]ast.Diagnostic, 0, len(units))
		for _, u := range units {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-mode-unsupported",
				Severity: ast.Warning,
				Message:  fmt.Sprintf("quote: source %q cannot serve mode for pair %s/%s", sourceName, u.req.Pair.Commodity, u.req.Pair.QuoteCurrency),
			})
		}
		return nil, diags
	}

	// Each unit contributes one query, with the symbol from its
	// currently-active SourceRef (u.depth is stable within a level).
	queries := make([]api.SourceQuery, 0, len(units))
	for _, u := range units {
		queries = append(queries, api.SourceQuery{Pair: u.req.Pair, Symbol: u.req.Sources[u.depth].Symbol})
	}
	// For event reporting, when there is a single query in the batch,
	// populate Pair/Symbol; otherwise leave them zero.
	var pair api.Pair
	var sym string
	if len(units) == 1 {
		pair = units[0].req.Pair
		sym = units[0].req.Sources[units[0].depth].Symbol
	}

	// EventCallStart fires from inside the closure passed to
	// runUnderSemaphore so observers see it after the semaphore is
	// acquired (i.e. when the call actually begins running). When
	// ctx is cancelled before the semaphore is acquired,
	// runUnderSemaphore returns early without calling the closure;
	// start is then never assigned and the IsZero guard below
	// suppresses CallDone too, matching the silent-cancel behaviour
	// of the pre-refactor code.
	var start time.Time
	prices, diags, err := runUnderSemaphore(ctx, sem, func() ([]ast.Price, []ast.Diagnostic, error) {
		emit(Event{Kind: EventCallStart, Level: level, Source: sourceName, Pair: pair, Symbol: sym, Mode: p.mode})
		start = time.Now()
		return p.invoke(ctx, queries)
	})
	if !start.IsZero() {
		emit(Event{
			Kind:     EventCallDone,
			Level:    level,
			Source:   sourceName,
			Pair:     pair,
			Symbol:   sym,
			Mode:     p.mode,
			Duration: time.Since(start),
			Err:      err,
			NumPrice: len(prices),
		})
	}
	if err != nil {
		return nil, append(diags, fetchErrDiags(err, sourceName, units)...)
	}
	attribute(prices, units)
	return prices, diags
}
