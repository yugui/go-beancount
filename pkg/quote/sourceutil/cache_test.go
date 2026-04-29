package sourceutil

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

func priceOf(commodity, currency, num string, at time.Time) ast.Price {
	var d apd.Decimal
	_, _, _ = d.SetString(num)
	return ast.Price{
		Date:      at,
		Commodity: commodity,
		Amount:    ast.Amount{Number: d, Currency: currency},
	}
}

func TestCacheAllHitsShortCircuits(t *testing.T) {
	var calls int64
	at := &fakeAt{
		name: "ecb",
		caps: api.Capabilities{SupportsAt: true, BatchPairs: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&calls, 1)
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1.1", at))
			}
			return out, nil, nil
		},
	}
	src := Cache(at, CacheOptions{}).(api.AtSource)
	day := utcDate(2024, time.January, 5)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}, Symbol: "EUR"},
		{Pair: api.Pair{Commodity: "GBP", QuoteCurrency: "USD"}, Symbol: "GBP"},
	}
	// Prime.
	_, _, err := src.QuoteAt(context.Background(), queries, day)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 after first call", calls)
	}
	// Second identical call should not hit downstream.
	prices, _, err := src.QuoteAt(context.Background(), queries, day)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 (cache should short-circuit)", calls)
	}
	if len(prices) != 2 {
		t.Errorf("got %d prices, want 2", len(prices))
	}
}

func TestCachePartialMissForwardsOnlyMissing(t *testing.T) {
	var lastForwarded []api.SourceQuery
	var calls int64
	at := &fakeAt{
		name: "ecb",
		caps: api.Capabilities{SupportsAt: true, BatchPairs: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&calls, 1)
			cp := make([]api.SourceQuery, len(q))
			copy(cp, q)
			lastForwarded = cp
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1.1", at))
			}
			return out, nil, nil
		},
	}
	src := Cache(at, CacheOptions{}).(api.AtSource)
	day := utcDate(2024, time.January, 5)

	// Prime with EUR and GBP.
	_, _, err := src.QuoteAt(context.Background(), []api.SourceQuery{
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}, Symbol: "EUR"},
		{Pair: api.Pair{Commodity: "GBP", QuoteCurrency: "USD"}, Symbol: "GBP"},
	}, day)
	if err != nil {
		t.Fatalf("priming: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1", calls)
	}

	// Ask for EUR (hit) + JPY (miss). Only JPY should be forwarded.
	prices, _, err := src.QuoteAt(context.Background(), []api.SourceQuery{
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}, Symbol: "EUR"},
		{Pair: api.Pair{Commodity: "JPY", QuoteCurrency: "USD"}, Symbol: "JPY"},
	}, day)
	if err != nil {
		t.Fatalf("partial: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls=%d, want 2", calls)
	}
	if len(lastForwarded) != 1 || lastForwarded[0].Pair.Commodity != "JPY" {
		t.Errorf("lastForwarded=%+v, want [JPY]", lastForwarded)
	}
	if len(prices) != 2 {
		t.Errorf("got %d prices, want 2", len(prices))
	}
}

func TestCacheBatchPairsFalseSplitsPerEntry(t *testing.T) {
	var batchSizes []int
	at := &fakeAt{
		name: "single",
		caps: api.Capabilities{SupportsAt: true, BatchPairs: false},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			batchSizes = append(batchSizes, len(q))
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1", at))
			}
			return out, nil, nil
		},
	}
	src := Cache(at, CacheOptions{}).(api.AtSource)
	day := utcDate(2024, time.January, 5)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "A", QuoteCurrency: "USD"}, Symbol: "A"},
		{Pair: api.Pair{Commodity: "B", QuoteCurrency: "USD"}, Symbol: "B"},
		{Pair: api.Pair{Commodity: "C", QuoteCurrency: "USD"}, Symbol: "C"},
	}
	_, _, err := src.QuoteAt(context.Background(), queries, day)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(batchSizes) != 3 {
		t.Errorf("got %d calls, want 3 (BatchPairs=false splits per entry)", len(batchSizes))
	}
	for _, n := range batchSizes {
		if n != 1 {
			t.Errorf("got batch size %d, want 1", n)
		}
	}
}

func TestCacheTTLEviction(t *testing.T) {
	var calls int64
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true, BatchPairs: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&calls, 1)
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1", at))
			}
			return out, nil, nil
		},
	}
	src := Cache(at, CacheOptions{TTL: 30 * time.Millisecond}).(api.AtSource)
	queries := []api.SourceQuery{{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}}}
	day := utcDate(2024, time.January, 5)
	if _, _, err := src.QuoteAt(context.Background(), queries, day); err != nil {
		t.Fatalf("first: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1", calls)
	}
	// Within TTL: hit.
	if _, _, err := src.QuoteAt(context.Background(), queries, day); err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 (cached)", calls)
	}
	// Wait past TTL; expect re-fetch.
	time.Sleep(60 * time.Millisecond)
	if _, _, err := src.QuoteAt(context.Background(), queries, day); err != nil {
		t.Fatalf("third: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls=%d, want 2 (TTL evicted)", calls)
	}
}

func TestCacheMaxEntries(t *testing.T) {
	var calls int64
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true, BatchPairs: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&calls, 1)
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1", at))
			}
			return out, nil, nil
		},
	}
	src := Cache(at, CacheOptions{MaxEntries: 1}).(api.AtSource)
	day := utcDate(2024, time.January, 5)

	// Insert EUR then GBP; cap=1 means EUR is evicted.
	if _, _, err := src.QuoteAt(context.Background(), []api.SourceQuery{
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}},
	}, day); err != nil {
		t.Fatalf("EUR: %v", err)
	}
	if _, _, err := src.QuoteAt(context.Background(), []api.SourceQuery{
		{Pair: api.Pair{Commodity: "GBP", QuoteCurrency: "USD"}},
	}, day); err != nil {
		t.Fatalf("GBP: %v", err)
	}
	// EUR should now be evicted; querying it forces re-fetch.
	before := atomic.LoadInt64(&calls)
	if _, _, err := src.QuoteAt(context.Background(), []api.SourceQuery{
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}},
	}, day); err != nil {
		t.Fatalf("EUR re-fetch: %v", err)
	}
	if atomic.LoadInt64(&calls) != before+1 {
		t.Errorf("expected re-fetch of evicted EUR; calls did not increment")
	}
}

func TestCachePreservesSubInterfaces(t *testing.T) {
	src := &fakeLatestAt{
		name: "x",
		caps: api.Capabilities{SupportsLatest: true, SupportsAt: true},
		handleAt: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
		handleLatest: func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) { return nil, nil, nil },
	}
	wrapped := Cache(src, CacheOptions{})
	if _, ok := wrapped.(api.LatestSource); !ok {
		t.Errorf("Cache lost LatestSource")
	}
	if _, ok := wrapped.(api.AtSource); !ok {
		t.Errorf("Cache lost AtSource")
	}
	if _, ok := wrapped.(api.RangeSource); ok {
		t.Errorf("Cache unexpectedly added RangeSource")
	}
}

func TestCacheLatestModeShortCircuits(t *testing.T) {
	var calls int64
	src := &fakeLatest{
		name: "x",
		caps: api.Capabilities{SupportsLatest: true, BatchPairs: true},
		handle: func(_ context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&calls, 1)
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1", time.Now()))
			}
			return out, nil, nil
		},
	}
	wrapped := Cache(src, CacheOptions{}).(api.LatestSource)
	queries := []api.SourceQuery{{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}}}
	if _, _, err := wrapped.QuoteLatest(context.Background(), queries); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := wrapped.QuoteLatest(context.Background(), queries); err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 (cache short-circuit)", calls)
	}
}

func TestCacheRangeMode(t *testing.T) {
	var calls int64
	src := &fakeRange{
		name: "x",
		caps: api.Capabilities{SupportsRange: true, BatchPairs: true},
		handle: func(_ context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&calls, 1)
			var out []ast.Price
			for _, qq := range q {
				out = append(out, priceOf(qq.Pair.Commodity, qq.Pair.QuoteCurrency, "1", start))
			}
			return out, nil, nil
		},
	}
	wrapped := Cache(src, CacheOptions{}).(api.RangeSource)
	start := utcDate(2024, time.January, 1)
	end := utcDate(2024, time.January, 7)
	queries := []api.SourceQuery{{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}}}
	if _, _, err := wrapped.QuoteRange(context.Background(), queries, start, end); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := wrapped.QuoteRange(context.Background(), queries, start, end); err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls != 1 {
		t.Errorf("range calls=%d, want 1", calls)
	}
}
