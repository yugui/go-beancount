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

func TestSplitBatch_AtSourceHonoursBatchSize(t *testing.T) {
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
	bp := SplitBatch(at, 3).(api.AtSource)

	caps := bp.Capabilities()
	if !caps.SupportsAt {
		t.Errorf("Capabilities()=%+v, want SupportsAt=true", caps)
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

func TestSplitBatch_LatestSourceHonoursBatchSize(t *testing.T) {
	var batchSizes []int
	var mu sync.Mutex
	src := &fakeLatest{
		name: "x",
		caps: api.Capabilities{SupportsLatest: true},
		handle: func(_ context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			batchSizes = append(batchSizes, len(q))
			mu.Unlock()
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				var d apd.Decimal
				_, _, _ = d.SetString("1")
				out = append(out, ast.Price{
					Date:      time.Now(),
					Commodity: qq.Pair.Commodity,
					Amount:    ast.Amount{Number: d, Currency: qq.Pair.QuoteCurrency},
				})
			}
			return out, nil, nil
		},
	}
	bp := SplitBatch(src, 2).(api.LatestSource)
	queries := make([]api.SourceQuery, 5)
	for i := range queries {
		queries[i] = api.SourceQuery{Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"}}
	}
	prices, _, err := bp.QuoteLatest(context.Background(), queries)
	if err != nil {
		t.Fatalf("QuoteLatest err: %v", err)
	}
	if len(prices) != 5 {
		t.Errorf("got %d prices, want 5", len(prices))
	}
	if len(batchSizes) != 3 { // 2,2,1
		t.Errorf("got %d batches, want 3", len(batchSizes))
	}
	for _, n := range batchSizes {
		if n > 2 {
			t.Errorf("batch size %d exceeds n=2", n)
		}
	}
}

func TestSplitBatch_RangeSourceHonoursBatchSize(t *testing.T) {
	var batchSizes []int
	var seenStarts []time.Time
	var seenEnds []time.Time
	var mu sync.Mutex
	src := &fakeRange{
		name: "x",
		caps: api.Capabilities{SupportsRange: true},
		handle: func(_ context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			batchSizes = append(batchSizes, len(q))
			seenStarts = append(seenStarts, start)
			seenEnds = append(seenEnds, end)
			mu.Unlock()
			var out []ast.Price
			for _, qq := range q {
				var d apd.Decimal
				_, _, _ = d.SetString("1")
				out = append(out, ast.Price{
					Date:      start,
					Commodity: qq.Pair.Commodity,
					Amount:    ast.Amount{Number: d, Currency: qq.Pair.QuoteCurrency},
				})
			}
			return out, nil, nil
		},
	}
	bp := SplitBatch(src, 2).(api.RangeSource)
	queries := make([]api.SourceQuery, 5)
	for i := range queries {
		queries[i] = api.SourceQuery{Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"}}
	}
	start := utcDate(2024, time.January, 1)
	end := utcDate(2024, time.January, 10)
	prices, _, err := bp.QuoteRange(context.Background(), queries, start, end)
	if err != nil {
		t.Fatalf("QuoteRange err: %v", err)
	}
	if len(prices) != 5 {
		t.Errorf("got %d prices, want 5", len(prices))
	}
	if len(batchSizes) != 3 { // 2,2,1
		t.Errorf("got %d batches, want 3", len(batchSizes))
	}
	// All sub-batches must see the original full range.
	for i := range seenStarts {
		if !seenStarts[i].Equal(start) || !seenEnds[i].Equal(end) {
			t.Errorf("batch %d got [%v,%v), want [%v,%v)", i, seenStarts[i], seenEnds[i], start, end)
		}
	}
}

func TestSplitBatch_RunsInParallel(t *testing.T) {
	const queries = 4
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
	bp := SplitBatch(at, 1).(api.AtSource)
	qs := make([]api.SourceQuery, queries)
	for i := range qs {
		qs[i] = api.SourceQuery{Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"}}
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := bp.QuoteAt(context.Background(), qs, utcDate(2024, time.January, 5))
		done <- err
	}()

	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatalf("only %d/2 batches entered before deadline", i)
		}
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
}

func TestSplitBatch_AggregatesErrors(t *testing.T) {
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
	bp := SplitBatch(at, 1).(api.AtSource)
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

func TestSplitBatch_NonPositiveN(t *testing.T) {
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
	bp := SplitBatch(at, 0).(api.AtSource) // treated as 1
	queries := make([]api.SourceQuery, 4)
	_, _, err := bp.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if maxBatch != 1 {
		t.Errorf("maxBatch=%d, want 1", maxBatch)
	}
}

func TestSplitBatch_PreservesSubInterfaces(t *testing.T) {
	src := &fakeLatestAt{
		name: "x",
		caps: api.Capabilities{SupportsLatest: true, SupportsAt: true},
		handleAt: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
		handleLatest: func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	wrapped := SplitBatch(src, 2)
	if _, ok := wrapped.(api.LatestSource); !ok {
		t.Errorf("SplitBatch lost LatestSource")
	}
	if _, ok := wrapped.(api.AtSource); !ok {
		t.Errorf("SplitBatch lost AtSource")
	}
	if _, ok := wrapped.(api.RangeSource); ok {
		t.Errorf("SplitBatch unexpectedly added RangeSource")
	}
}
