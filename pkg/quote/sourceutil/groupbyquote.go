package sourceutil

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// GroupByQuoteCurrency wraps s so that any inbound Quote* call carrying
// queries with multiple distinct Pair.QuoteCurrency values is split into
// one downstream call per quote currency. Use this when your source can
// only batch pairs that share a single quote currency (the Phase 7
// obligation on every Source implementer; see [pkg/quote/api]).
//
// The returned source preserves whichever sub-interfaces the input s
// declares (LatestSource / AtSource / RangeSource). Each per-currency
// partition is dispatched in its own goroutine; results from completed
// partitions are merged before returning.
//
// # Goroutine model
//
// One goroutine per distinct QuoteCurrency in the input slice; if the
// input is empty or carries only one quote currency, the call forwards
// directly without spawning extras. Stack with [Concurrency] to bound
// the per-call goroutine count if the in-call partition count exceeds
// what the source-side concurrency cap should allow.
//
// # Error aggregation
//
// A non-nil error from one partition does not fail the whole call: it
// is converted to a quote-fetch-error Diagnostic naming the partition's
// quote currency, and the surviving partitions' prices and diagnostics
// are merged in. Per-query failures the wrapped source already encodes
// as Diagnostics flow through unchanged.
//
// # Context cancellation timing
//
// In-flight partitions are not individually cancellable from outside
// the wrapped source; on cancellation the wrapper waits for every
// already-spawned goroutine to return, merges any partial results
// they produced, and returns those alongside ctx.Err() (matching
// [SplitBatch] and [SplitRange]).
//
// # Stack ordering
//
// The typical stack from outside in is:
//
//	GroupByQuoteCurrency → SplitBatch → wrapped source
//
// because partitioning by quote currency may produce per-partition
// slices of varying length that each then want batch-size capping.
func GroupByQuoteCurrency(s api.Source) api.Source {
	w := &groupByQuoteSource{base: s}
	if l, ok := s.(api.LatestSource); ok {
		w.latest = l
	}
	if a, ok := s.(api.AtSource); ok {
		w.at = a
	}
	if r, ok := s.(api.RangeSource); ok {
		w.rng = r
	}
	return w.asSource()
}

// groupByQuoteSource holds whichever sub-interfaces the wrapped source
// satisfies. Its asSource method returns a value that re-satisfies the
// same set, so callers can recover the original interface set with a
// type assertion.
type groupByQuoteSource struct {
	base   api.Source
	latest api.LatestSource
	at     api.AtSource
	rng    api.RangeSource
}

func (s *groupByQuoteSource) Name() string                   { return s.base.Name() }
func (s *groupByQuoteSource) Capabilities() api.Capabilities { return s.base.Capabilities() }

// partitionByQuoteCurrency groups q by Pair.QuoteCurrency, preserving
// the original slice order within each partition. The returned slice
// of currency keys is sorted so iteration order is deterministic.
func partitionByQuoteCurrency(q []api.SourceQuery) ([]string, map[string][]api.SourceQuery) {
	if len(q) == 0 {
		return nil, nil
	}
	groups := make(map[string][]api.SourceQuery)
	for _, qq := range q {
		c := qq.Pair.QuoteCurrency
		groups[c] = append(groups[c], qq)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, groups
}

// runPartitions fans the per-currency partitions out into parallel
// goroutines, calling fn per partition and merging results in
// deterministic (sorted-by-currency) order. A non-nil err from a
// partition is converted into a quote-fetch-error diagnostic naming
// that quote currency and folded into the merged diags. ctx
// cancellation propagates as a top-level error after all spawned
// goroutines have returned.
func (s *groupByQuoteSource) runPartitions(ctx context.Context, q []api.SourceQuery, fn func(qc string, sub []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error)) ([]ast.Price, []ast.Diagnostic, error) {
	keys, groups := partitionByQuoteCurrency(q)
	switch len(keys) {
	case 0:
		return nil, nil, nil
	case 1:
		k := keys[0]
		ps, ds, err := fn(k, groups[k])
		if err != nil {
			ds = append(ds, ast.Diagnostic{
				Code:    "quote-fetch-error",
				Message: fmt.Sprintf("%s: quote currency %s: %v", s.base.Name(), k, err),
			})
		}
		if cerr := ctx.Err(); cerr != nil {
			return ps, ds, cerr
		}
		return ps, ds, nil
	}
	type result struct {
		prices []ast.Price
		diags  []ast.Diagnostic
	}
	results := make([]result, len(keys))
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps, ds, err := fn(k, groups[k])
			results[i] = result{prices: ps, diags: ds}
			if err != nil {
				results[i].diags = append(results[i].diags, ast.Diagnostic{
					Code:    "quote-fetch-error",
					Message: fmt.Sprintf("%s: quote currency %s: %v", s.base.Name(), k, err),
				})
			}
		}()
	}
	wg.Wait()
	var prices []ast.Price
	var diags []ast.Diagnostic
	for _, r := range results {
		prices = append(prices, r.prices...)
		diags = append(diags, r.diags...)
	}
	if err := ctx.Err(); err != nil {
		return prices, diags, err
	}
	return prices, diags, nil
}

func (s *groupByQuoteSource) doLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.runPartitions(ctx, q, func(_ string, sub []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
		return s.latest.QuoteLatest(ctx, sub)
	})
}

func (s *groupByQuoteSource) doAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.runPartitions(ctx, q, func(_ string, sub []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
		return s.at.QuoteAt(ctx, sub, at)
	})
}

func (s *groupByQuoteSource) doRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.runPartitions(ctx, q, func(_ string, sub []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
		return s.rng.QuoteRange(ctx, sub, start, end)
	})
}

// asSource returns s typed against whichever sub-interface combination
// the wrapped source satisfies.
func (s *groupByQuoteSource) asSource() api.Source {
	hasL := s.latest != nil
	hasA := s.at != nil
	hasR := s.rng != nil
	switch {
	case hasL && hasA && hasR:
		return groupByQuoteLatestAtRange{s}
	case hasL && hasA:
		return groupByQuoteLatestAt{s}
	case hasL && hasR:
		return groupByQuoteLatestRange{s}
	case hasA && hasR:
		return groupByQuoteAtRange{s}
	case hasL:
		return groupByQuoteLatestOnly{s}
	case hasA:
		return groupByQuoteAtOnly{s}
	case hasR:
		return groupByQuoteRangeOnly{s}
	default:
		return groupByQuoteBaseOnly{s}
	}
}

// The eight permutations below exist so the GroupByQuoteCurrency
// return value satisfies exactly the sub-interface set of the wrapped
// source.

type groupByQuoteBaseOnly struct{ *groupByQuoteSource }

type groupByQuoteLatestOnly struct{ *groupByQuoteSource }

func (s groupByQuoteLatestOnly) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doLatest(ctx, q)
}

type groupByQuoteAtOnly struct{ *groupByQuoteSource }

func (s groupByQuoteAtOnly) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doAt(ctx, q, at)
}

type groupByQuoteRangeOnly struct{ *groupByQuoteSource }

func (s groupByQuoteRangeOnly) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doRange(ctx, q, start, end)
}

type groupByQuoteLatestAt struct{ *groupByQuoteSource }

func (s groupByQuoteLatestAt) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doLatest(ctx, q)
}
func (s groupByQuoteLatestAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doAt(ctx, q, at)
}

type groupByQuoteLatestRange struct{ *groupByQuoteSource }

func (s groupByQuoteLatestRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doLatest(ctx, q)
}
func (s groupByQuoteLatestRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doRange(ctx, q, start, end)
}

type groupByQuoteAtRange struct{ *groupByQuoteSource }

func (s groupByQuoteAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doAt(ctx, q, at)
}
func (s groupByQuoteAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doRange(ctx, q, start, end)
}

type groupByQuoteLatestAtRange struct{ *groupByQuoteSource }

func (s groupByQuoteLatestAtRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doLatest(ctx, q)
}
func (s groupByQuoteLatestAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doAt(ctx, q, at)
}
func (s groupByQuoteLatestAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.groupByQuoteSource.doRange(ctx, q, start, end)
}
