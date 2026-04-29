package quote

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// unit is the orchestrator's internal scheduling atom: one
// PriceRequest's progress through its fallback chain.
//
// For ModeRange, a unit covers the whole [Spec.Start, Spec.End)
// interval at the unit level; the source-dispatch step is responsible
// for chopping that interval into RangePerCall-sized buckets when
// actually issuing the call. Modelling Range as one unit per request
// (rather than one per bucket) keeps the unit-bookkeeping uniform
// across modes and lets the scheduler drive the per-source bucket
// loop locally to the call site, where the relevant Capabilities are
// already in scope.
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
// SourceQuery slice (one query per unit, or one per bucket when
// ModeRange + AtSource demotion applies), issues the call(s),
// converts errors / panics / unsupported-mode into Diagnostics, and
// attributes returned Prices back to the contributing units.
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

	caps := src.Capabilities()

	// Build the per-unit (Pair, Symbol) records once. A symbol comes
	// from the unit's currently-active SourceRef.
	type queryUnit struct {
		u   *unit
		sym string
	}
	qus := make([]queryUnit, 0, len(units))
	for _, u := range units {
		qus = append(qus, queryUnit{u: u, sym: u.req.Sources[u.depth].Symbol})
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

	// chooseMode decides which source method to use given spec.Mode
	// and the source's declared Capabilities. The second return is
	// false when no method can serve the requested mode (mode-
	// unsupported diagnostic territory).
	type method int
	const (
		methodNone method = iota
		methodLatest
		methodAt
		methodRange
	)
	chooseMode := func() (method, bool) {
		switch spec.Mode {
		case api.ModeLatest:
			if caps.SupportsLatest {
				return methodLatest, true
			}
			if caps.SupportsAt {
				return methodAt, true
			}
			if caps.SupportsRange {
				return methodRange, true
			}
		case api.ModeAt:
			if caps.SupportsAt {
				return methodAt, true
			}
			if caps.SupportsRange {
				return methodRange, true
			}
			if caps.SupportsLatest {
				now := cfg.now()
				if !now.Before(spec.At) && now.Before(spec.At.Add(24*time.Hour)) {
					return methodLatest, true
				}
			}
		case api.ModeRange:
			if caps.SupportsRange {
				return methodRange, true
			}
			if caps.SupportsAt {
				return methodAt, true
			}
			if caps.SupportsLatest {
				now := cfg.now()
				if !now.Before(spec.Start) && now.Before(spec.End) {
					return methodLatest, true
				}
			}
		}
		return methodNone, false
	}

	m, ok := chooseMode()
	if !ok {
		diags := make([]ast.Diagnostic, 0, len(qus))
		for _, q := range qus {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-mode-unsupported",
				Severity: ast.Warning,
				Message:  fmt.Sprintf("quote: source %q cannot serve mode for pair %s/%s", sourceName, q.u.req.Pair.Commodity, q.u.req.Pair.QuoteCurrency),
			})
		}
		return nil, diags
	}

	// attribute walks the Prices a call returned and marks the
	// originating units as got. Prices the call produced that don't
	// match any unit's Pair (the "ECB windfall") are still included
	// in the final result so the cache decorator can capitalise on
	// them.
	attribute := func(in []ast.Price) {
		for i := range in {
			pair := api.Pair{Commodity: in[i].Commodity, QuoteCurrency: in[i].Amount.Currency}
			for _, q := range qus {
				if q.u.req.Pair == pair {
					q.u.got = true
				}
			}
		}
	}

	// fetchErrDiag turns a non-nil error from a source method into
	// the matching quote-fetch-error Diagnostic per affected unit.
	fetchErrDiags := func(err error, affected []queryUnit) []ast.Diagnostic {
		out := make([]ast.Diagnostic, 0, len(affected))
		for _, q := range affected {
			out = append(out, ast.Diagnostic{
				Code:     "quote-fetch-error",
				Severity: ast.Error,
				Message:  fmt.Sprintf("quote: source %q failed for pair %s/%s: %v", sourceName, q.u.req.Pair.Commodity, q.u.req.Pair.QuoteCurrency, err),
			})
		}
		return out
	}

	// callOnce dispatches a single source call (already mode-
	// resolved) on the given queries, with the given mode-specific
	// time arguments. It assembles the SourceQuery slice from qSet
	// and routes the result back through attribute / fetchErrDiags.
	callOnce := func(mode api.Mode, qSet []queryUnit, atTime, startTime, endTime time.Time) ([]ast.Price, []ast.Diagnostic) {
		queries := make([]api.SourceQuery, 0, len(qSet))
		for _, q := range qSet {
			queries = append(queries, api.SourceQuery{Pair: q.u.req.Pair, Symbol: q.sym})
		}
		// For event reporting, when there is a single query in the
		// batch, populate Pair/Symbol; otherwise leave them zero.
		var pair api.Pair
		var sym string
		if len(qSet) == 1 {
			pair = qSet[0].u.req.Pair
			sym = qSet[0].sym
		}
		prices, diags, err := runCall(pair, sym, mode, func() ([]ast.Price, []ast.Diagnostic, error) {
			// The type assertions below are intentionally unchecked:
			// chooseMode picked this mode based on the source's
			// declared Capabilities. If a source lies about its
			// Capabilities (e.g. SupportsLatest=true without
			// implementing api.LatestSource), the assertion will
			// panic. That panic is recovered by runCall and converted
			// into a quote-fetch-error Diagnostic, so the affected
			// unit advances to its next fallback rather than tearing
			// down the whole orchestrator.
			switch mode {
			case api.ModeLatest:
				return src.(api.LatestSource).QuoteLatest(ctx, queries)
			case api.ModeAt:
				return src.(api.AtSource).QuoteAt(ctx, queries, atTime)
			case api.ModeRange:
				return src.(api.RangeSource).QuoteRange(ctx, queries, startTime, endTime)
			}
			return nil, nil, fmt.Errorf("quote: internal: unknown mode %v", mode)
		})
		if err != nil {
			return nil, append(diags, fetchErrDiags(err, qSet)...)
		}
		attribute(prices)
		return prices, diags
	}

	// Now actually issue calls. We split into batches based on
	// caps.BatchPairs: when false, each query is its own call. When
	// true, all queries for this source go in a single call (or, for
	// ModeRange + RangeSource with RangePerCall>0, one call per
	// bucket carrying all queries).
	var batches [][]queryUnit
	if caps.BatchPairs {
		batches = [][]queryUnit{qus}
	} else {
		batches = make([][]queryUnit, 0, len(qus))
		for i := range qus {
			batches = append(batches, qus[i:i+1])
		}
	}

	var (
		allPrices []ast.Price
		allDiags  []ast.Diagnostic
	)

	switch m {
	case methodLatest:
		for _, b := range batches {
			ps, ds := callOnce(api.ModeLatest, b, time.Time{}, time.Time{}, time.Time{})
			allPrices = append(allPrices, ps...)
			allDiags = append(allDiags, ds...)
		}

	case methodAt:
		// For ModeLatest/ModeAt the call covers a single instant; for
		// ModeRange demoted to AtSource we loop one calendar day at
		// a time across [Start, End).
		switch spec.Mode {
		case api.ModeLatest:
			at := cfg.now()
			for _, b := range batches {
				ps, ds := callOnce(api.ModeAt, b, at, time.Time{}, time.Time{})
				allPrices = append(allPrices, ps...)
				allDiags = append(allDiags, ds...)
			}
		case api.ModeAt:
			for _, b := range batches {
				ps, ds := callOnce(api.ModeAt, b, spec.At, time.Time{}, time.Time{})
				allPrices = append(allPrices, ps...)
				allDiags = append(allDiags, ds...)
			}
		case api.ModeRange:
			for d := spec.Start; d.Before(spec.End); d = d.AddDate(0, 0, 1) {
				for _, b := range batches {
					ps, ds := callOnce(api.ModeAt, b, d, time.Time{}, time.Time{})
					allPrices = append(allPrices, ps...)
					allDiags = append(allDiags, ds...)
				}
			}
		}

	case methodRange:
		// Pick the [start, end) the call covers based on the source
		// mode. Then split it by RangePerCall and issue one call per
		// bucket (per batch).
		var rStart, rEnd time.Time
		switch spec.Mode {
		case api.ModeLatest:
			now := cfg.now()
			rStart = now.AddDate(0, 0, -1)
			rEnd = now.AddDate(0, 0, 1)
		case api.ModeAt:
			rStart = spec.At
			rEnd = spec.At.AddDate(0, 0, 1)
		case api.ModeRange:
			rStart = spec.Start
			rEnd = spec.End
		}

		buckets := splitRange(rStart, rEnd, caps.RangePerCall)
		for _, bk := range buckets {
			for _, b := range batches {
				ps, ds := callOnce(api.ModeRange, b, time.Time{}, bk.start, bk.end)
				allPrices = append(allPrices, ps...)
				allDiags = append(allDiags, ds...)
			}
		}
	}

	return allPrices, allDiags
}

// rangeBucket is the internal half-open interval value used by
// splitRange. It exists only so dispatchSource's RangeSource branch
// can iterate over a homogeneous slice instead of a parallel pair of
// time slices.
type rangeBucket struct {
	start, end time.Time
}

// splitRange divides [start, end) into chunks of at most perCall
// days. perCall <= 0 means "no chunking — one bucket covering the
// whole interval".
func splitRange(start, end time.Time, perCall int) []rangeBucket {
	if !end.After(start) {
		return nil
	}
	if perCall <= 0 {
		return []rangeBucket{{start: start, end: end}}
	}
	var buckets []rangeBucket
	for d := start; d.Before(end); {
		next := d.AddDate(0, 0, perCall)
		if next.After(end) {
			next = end
		}
		buckets = append(buckets, rangeBucket{start: d, end: next})
		d = next
	}
	return buckets
}
