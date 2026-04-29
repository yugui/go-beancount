package sourceutil

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

func TestBatchPairsHonoursBatchSize(t *testing.T) {
	var batchSizes []int
	var mu sync.Mutex
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			batchSizes = append(batchSizes, len(q))
			mu.Unlock()
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				var d apd.Decimal
				_, _, _ = d.SetString("1")
				out = append(out, ast.Price{
					Date:      at,
					Commodity: qq.Pair.Commodity,
					Amount:    ast.Amount{Number: d, Currency: qq.Pair.QuoteCurrency},
				})
			}
			return out, nil, nil
		},
	}
	bp := BatchPairs(at, 3)

	caps := bp.Capabilities()
	if !caps.SupportsAt || !caps.BatchPairs {
		t.Errorf("Capabilities()=%+v, want SupportsAt=true & BatchPairs=true", caps)
	}

	queries := make([]api.SourceQuery, 7)
	for i := range queries {
		queries[i] = api.SourceQuery{Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"}}
	}
	prices, _, err := bp.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
	if len(prices) != 7 {
		t.Errorf("got %d prices, want 7", len(prices))
	}
	// 3 batches of size 3, 3, 1.
	if len(batchSizes) != 3 {
		t.Errorf("got %d batches, want 3", len(batchSizes))
	}
	total := 0
	for _, n := range batchSizes {
		if n > 3 {
			t.Errorf("batch size %d exceeds n=3", n)
		}
		total += n
	}
	if total != 7 {
		t.Errorf("batches summed to %d, want 7", total)
	}
}

func TestBatchPairsRunsInParallel(t *testing.T) {
	const queries = 4
	// Each invocation of the fake signals "I am in" on `entered` and
	// then blocks on `release`. The test consumes >=2 signals to prove
	// at least two batches were in flight concurrently before letting
	// them all return. `entered` is buffered to fit every batch so
	// later sends never block.
	entered := make(chan struct{}, queries)
	release := make(chan struct{})
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, _ []api.SourceQuery, _ time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			entered <- struct{}{}
			<-release
			return nil, nil, nil
		},
	}
	bp := BatchPairs(at, 1)
	qs := make([]api.SourceQuery, queries)
	for i := range qs {
		qs[i] = api.SourceQuery{Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"}}
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := bp.QuoteAt(context.Background(), qs, utcDate(2024, time.January, 5))
		done <- err
	}()

	// Wait for at least two batches to be simultaneously in flight.
	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatalf("only %d/2 batches entered before deadline", i)
		}
	}

	// Let everything finish.
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
}

func TestBatchPairsAggregatesErrors(t *testing.T) {
	var calls int64
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			n := atomic.AddInt64(&calls, 1)
			if n == 1 {
				return nil, nil, errors.New("transient")
			}
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				var d apd.Decimal
				_, _, _ = d.SetString("1")
				out = append(out, ast.Price{
					Date:      at,
					Commodity: qq.Pair.Commodity,
					Amount:    ast.Amount{Number: d, Currency: qq.Pair.QuoteCurrency},
				})
			}
			return out, nil, nil
		},
	}
	bp := BatchPairs(at, 1)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "A", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "B", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "C", QuoteCurrency: "USD"}},
	}
	prices, diags, err := bp.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("got top-level err=%v, want nil (errors should be in diags)", err)
	}
	if len(prices) != 2 {
		t.Errorf("got %d prices, want 2", len(prices))
	}
	if len(diags) == 0 {
		t.Errorf("got 0 diagnostics, want >= 1")
	}
}

func TestBatchPairsNonPositiveN(t *testing.T) {
	var maxBatch int
	var mu sync.Mutex
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, q []api.SourceQuery, _ time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			if len(q) > maxBatch {
				maxBatch = len(q)
			}
			mu.Unlock()
			return nil, nil, nil
		},
	}
	bp := BatchPairs(at, 0) // treated as 1
	queries := make([]api.SourceQuery, 4)
	_, _, err := bp.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if maxBatch != 1 {
		t.Errorf("maxBatch=%d, want 1", maxBatch)
	}
}
