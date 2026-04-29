package sourceutil

import (
	"context"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// BatchPairs splits the input query slice of an api.AtSource into
// batches of at most n queries and runs the batches in parallel
// goroutines. The returned source reports
// Capabilities{SupportsAt: true, BatchPairs: true} regardless of the
// wrapped source's BatchPairs flag, since BatchPairs delivers the
// orchestrator's "batched" guarantee independent of the underlying
// API shape.
//
// # Goroutine model
//
// One goroutine is spawned per batch; results from completed batches
// are merged on return. To bound parallelism within a single QuoteAt
// call, stack with Concurrency. A non-positive n is treated as 1
// (effectively serial). If the input query slice is shorter than n,
// only one goroutine runs.
//
// # Error aggregation
//
// A non-nil error from one batch does not fail the whole call: the
// failing batch's diagnostics and prices are still merged in, and
// only context cancellation propagates as a top-level error from
// QuoteAt. Per-query failures the wrapped source already encodes as
// diagnostics flow through unchanged. This matches the orchestrator's
// "diagnostic per failed unit, error only when nothing succeeded"
// contract.
//
// # Context cancellation timing
//
// In-flight batches are not individually cancellable from outside the
// wrapped source. When ctx is cancelled, QuoteAt waits for every
// already-spawned per-batch goroutine to return before reporting
// ctx.Err(); each downstream call receives the cancelled ctx and is
// expected to return promptly, so the wait is typically short, but it
// is bounded by the wrapped source's responsiveness rather than by
// the wrapper itself.
func BatchPairs(s api.AtSource, n int) api.AtSource {
	if n <= 0 {
		n = 1
	}
	return &batchPairs{at: s, n: n}
}

type batchPairs struct {
	at api.AtSource
	n  int
}

func (b *batchPairs) Name() string { return b.at.Name() }

func (b *batchPairs) Capabilities() api.Capabilities {
	return api.Capabilities{SupportsAt: true, BatchPairs: true}
}

func (b *batchPairs) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	if len(q) == 0 {
		return nil, nil, nil
	}
	type result struct {
		prices []ast.Price
		diags  []ast.Diagnostic
	}
	// Compute number of batches.
	numBatches := (len(q) + b.n - 1) / b.n
	results := make([]result, numBatches)
	var wg sync.WaitGroup
	for i := 0; i < numBatches; i++ {
		i := i
		start := i * b.n
		end := start + b.n
		if end > len(q) {
			end = len(q)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps, ds, err := b.at.QuoteAt(ctx, q[start:end], at)
			results[i] = result{prices: ps, diags: ds}
			if err != nil {
				results[i].diags = append(results[i].diags, ast.Diagnostic{
					Code:    "quote-fetch-error",
					Message: err.Error(),
				})
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var prices []ast.Price
	var diags []ast.Diagnostic
	for _, r := range results {
		prices = append(prices, r.prices...)
		diags = append(diags, r.diags...)
	}
	return prices, diags, nil
}
