package sourceutil

import (
	"context"
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// SplitRange caps per-call day count to relieve a RangeSource
// author of the obligation to handle arbitrarily long ranges
// natively.
//
// SplitRange wraps a RangeSource so that QuoteRange calls spanning
// more than perCall days are split into sequential per-bucket calls.
// Use this when your source cannot natively handle arbitrarily long
// ranges (the Phase 7 obligation on RangeSource implementers; see
// pkg/quote/api).
//
// The returned source's Capabilities reports SupportsRange:true
// (matching s). Buckets are issued sequentially within a single
// QuoteRange call; callers wanting parallel buckets can stack
// SplitBatch underneath, or stack SplitBatch around the RangeSource
// if they want simple concurrency.
//
// A perCall <= 0 disables chunking and forwards the call unchanged.
//
// # Error aggregation
//
// A non-nil error from one bucket does not fail the whole call. The
// failing bucket's prices and diagnostics are merged in, and a
// "quote-fetch-error" diagnostic identifying that bucket's
// [start, end) range is appended; iteration continues with the next
// bucket. Context cancellation observed via ctx.Err() between
// buckets is the only condition under which QuoteRange itself
// returns a non-nil top-level error.
func SplitRange(s api.RangeSource, perCall int) api.RangeSource {
	return &splitRangeSource{rng: s, perCall: perCall}
}

type splitRangeSource struct {
	rng     api.RangeSource
	perCall int
}

func (s *splitRangeSource) Name() string { return s.rng.Name() }

func (s *splitRangeSource) Capabilities() api.Capabilities {
	c := s.rng.Capabilities()
	c.SupportsRange = true
	return c
}

func (s *splitRangeSource) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	if s.perCall <= 0 {
		return s.rng.QuoteRange(ctx, q, start, end)
	}
	if !end.After(start) {
		return nil, nil, nil
	}
	var prices []ast.Price
	var diags []ast.Diagnostic
	for cur := start; cur.Before(end); {
		if err := ctx.Err(); err != nil {
			return prices, diags, err
		}
		next := cur.AddDate(0, 0, s.perCall)
		if next.After(end) {
			next = end
		}
		ps, ds, err := s.rng.QuoteRange(ctx, q, cur, next)
		prices = append(prices, ps...)
		diags = append(diags, ds...)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:    "quote-fetch-error",
				Message: fmt.Sprintf("%s: range [%s, %s): %v", s.rng.Name(), cur.Format("2006-01-02"), next.Format("2006-01-02"), err),
			})
		}
		cur = next
	}
	return prices, diags, nil
}
