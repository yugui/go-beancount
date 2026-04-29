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
// For ModeRange, a unit covers the whole [Spec.Start, Spec.End)
// interval at the unit level; the orchestrator passes that whole
// interval through to the source in a single call. Source-side
// chunking (when the source's underlying API can't natively span
// arbitrary ranges) is the source author's responsibility, typically
// expressed by wrapping with pkg/quote/sourceutil.SplitRange.
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

// planCall picks the source method to call given spec.Mode and the
// source's declared Capabilities (the demotion table on Fetch's
// godoc) and returns a plan whose invoke closure carries the
// concrete time arguments. The second return is false when no method
// can serve the requested mode (mode-unsupported diagnostic
// territory).
//
// When spec.Mode is ModeRange and the source declares only
// SupportsAt, the AtSource is lifted to a RangeSource via
// sourceutil.DateRangeIter with Calendar=AllDays. Source authors who
// need calendar-aware iteration (e.g. WeekdaysOnly for FX) should
// register a RangeSource themselves rather than relying on this
// fallback.
//
// The type assertions inside the returned closures are intentionally
// unchecked: planCall picks the mode based on the source's declared
// Capabilities. If a source lies about its Capabilities (e.g.
// SupportsLatest=true without implementing api.LatestSource), the
// assertion will panic when invoke is called. That panic is
// recovered by dispatchSource's runCall and converted into a
// quote-fetch-error Diagnostic, so the affected unit advances to its
// next fallback rather than tearing down the whole orchestrator.
func planCall(spec api.Spec, src api.Source, cfg *runConfig) (plan, bool) {
	caps := src.Capabilities()
	switch spec.Mode {
	case api.ModeLatest:
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
	case api.ModeAt:
		if caps.SupportsAt {
			return plan{
				mode: api.ModeAt,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					return src.(api.AtSource).QuoteAt(ctx, qs, spec.At)
				},
			}, true
		}
		if caps.SupportsRange {
			return plan{
				mode: api.ModeRange,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					return src.(api.RangeSource).QuoteRange(ctx, qs, spec.At, spec.At.AddDate(0, 0, 1))
				},
			}, true
		}
		if caps.SupportsLatest {
			now := cfg.now()
			if !now.Before(spec.At) && now.Before(spec.At.Add(24*time.Hour)) {
				return plan{
					mode: api.ModeLatest,
					invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
						return src.(api.LatestSource).QuoteLatest(ctx, qs)
					},
				}, true
			}
		}
	case api.ModeRange:
		if caps.SupportsRange {
			return plan{
				mode: api.ModeRange,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					return src.(api.RangeSource).QuoteRange(ctx, qs, spec.Start, spec.End)
				},
			}, true
		}
		if caps.SupportsAt {
			return plan{
				mode: api.ModeRange,
				invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
					wrapped := sourceutil.DateRangeIter(src.(api.AtSource), sourceutil.AllDays)
					return wrapped.QuoteRange(ctx, qs, spec.Start, spec.End)
				},
			}, true
		}
		if caps.SupportsLatest {
			now := cfg.now()
			if !now.Before(spec.Start) && now.Before(spec.End) {
				return plan{
					mode: api.ModeLatest,
					invoke: func(ctx context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
						return src.(api.LatestSource).QuoteLatest(ctx, qs)
					},
				}, true
			}
		}
	}
	return plan{}, false
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

// runFetch is the entry point Fetch dispatches to once Options have
// been resolved. It owns the level loop, the {sourceName -> []unit}
// grouping, the per-source dispatch goroutines, and the cross-source
// concurrency semaphore.
func runFetch(ctx context.Context, reg Registry, spec api.Spec, cfg *runConfig) ([]ast.Price, []ast.Diagnostic, error) {
	if len(spec.Requests) == 0 {
		return nil, nil, errZeroPrices
	}

	units := make([]*unit, 0, len(spec.Requests))
	for _, r := range spec.Requests {
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
				localPrices, localDiags := dispatchSource(ctx, reg, spec, cfg, level, sourceName, us, sem, emit)
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
		return prices, diags, errZeroPrices
	}
	return prices, diags, nil
}

// dispatchSource is the per-source workhorse invoked once per
// (level, source) pair. It resolves the source from the registry,
// picks the source method to call from spec.Mode and the source's
// Capabilities (the demotion table on Fetch's godoc), assembles the
// SourceQuery slice (one query per unit), issues the call,
// converts errors / panics / unsupported-mode into Diagnostics, and
// attributes returned Prices back to the contributing units. Every
// unit going to the same source on the same level is dispatched in
// a single batched call; any per-source splitting (by query count
// or by date range) is the source author's responsibility.
func dispatchSource(
	ctx context.Context,
	reg Registry,
	spec api.Spec,
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

	// runCall acquires the semaphore, emits the start event, runs fn,
	// emits the done event, and returns the call's outputs. Panics
	// in fn are recovered and converted to a synthetic error so that
	// a single broken source never tears down the whole run.
	runCall := func(pair api.Pair, sym string, mode api.Mode, fn func() ([]ast.Price, []ast.Diagnostic, error)) ([]ast.Price, []ast.Diagnostic, error) {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		defer func() { <-sem }()

		emit(Event{Kind: EventCallStart, Level: level, Source: sourceName, Pair: pair, Symbol: sym, Mode: mode})
		start := time.Now()
		var (
			prices []ast.Price
			diags  []ast.Diagnostic
			err    error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("quote: source %q panicked: %v", sourceName, r)
				}
			}()
			prices, diags, err = fn()
		}()
		emit(Event{
			Kind:     EventCallDone,
			Level:    level,
			Source:   sourceName,
			Pair:     pair,
			Symbol:   sym,
			Mode:     mode,
			Duration: time.Since(start),
			Err:      err,
			NumPrice: len(prices),
		})
		return prices, diags, err
	}

	p, ok := planCall(spec, src, cfg)
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
	prices, diags, err := runCall(pair, sym, p.mode, func() ([]ast.Price, []ast.Diagnostic, error) {
		return p.invoke(ctx, queries)
	})
	if err != nil {
		return nil, append(diags, fetchErrDiags(err, sourceName, units)...)
	}
	attribute(prices, units)
	return prices, diags
}
