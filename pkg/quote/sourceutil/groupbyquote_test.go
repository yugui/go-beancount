package sourceutil

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// priceOne builds a placeholder ast.Price keyed off a SourceQuery so
// tests can assert "the partition that received this query produced a
// matching price".
func priceOne(q api.SourceQuery, at time.Time) ast.Price {
	var d apd.Decimal
	_, _, _ = d.SetString("1")
	return ast.Price{
		Date:      at,
		Commodity: q.Pair.Commodity,
		Amount:    ast.Amount{Number: d, Currency: q.Pair.QuoteCurrency},
	}
}

func TestGroupByQuoteCurrency_PartitionsByQuoteCurrency(t *testing.T) {
	type call struct{ qcs []string }
	var mu sync.Mutex
	var calls []call

	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			seen := make(map[string]struct{})
			var qcs []string
			for _, qq := range q {
				if _, ok := seen[qq.Pair.QuoteCurrency]; !ok {
					seen[qq.Pair.QuoteCurrency] = struct{}{}
					qcs = append(qcs, qq.Pair.QuoteCurrency)
				}
			}
			mu.Lock()
			calls = append(calls, call{qcs: qcs})
			mu.Unlock()
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOne(qq, at))
			}
			return out, nil, nil
		},
	}
	wrapped := GroupByQuoteCurrency(at).(api.AtSource)

	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "GOOG", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "TOYOTA", QuoteCurrency: "JPY"}},
	}
	prices, diags, err := wrapped.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("got %d diags, want 0: %+v", len(diags), diags)
	}
	if len(prices) != 3 {
		t.Errorf("got %d prices, want 3", len(prices))
	}
	if len(calls) != 2 {
		t.Fatalf("got %d downstream calls, want 2", len(calls))
	}

	// Each downstream call must carry exactly one quote currency.
	got := make(map[string]int)
	for _, c := range calls {
		if len(c.qcs) != 1 {
			t.Errorf("downstream call carried %d quote currencies, want 1: %+v", len(c.qcs), c.qcs)
			continue
		}
		got[c.qcs[0]]++
	}
	for _, want := range []string{"USD", "JPY"} {
		if got[want] != 1 {
			t.Errorf("downstream call for %s: got %d, want 1", want, got[want])
		}
	}

	// Per-commodity coverage: every input query produced an output price.
	gotCommodities := make(map[string]bool)
	for _, p := range prices {
		gotCommodities[p.Commodity] = true
	}
	for _, c := range []string{"AAPL", "GOOG", "TOYOTA"} {
		if !gotCommodities[c] {
			t.Errorf("missing price for commodity %s", c)
		}
	}
}

func TestGroupByQuoteCurrency_SingleCurrencyForwardsDirectly(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOne(qq, at))
			}
			return out, nil, nil
		},
	}
	wrapped := GroupByQuoteCurrency(at).(api.AtSource)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "GOOG", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "MSFT", QuoteCurrency: "USD"}},
	}
	prices, _, err := wrapped.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
	if calls != 1 {
		t.Errorf("got %d downstream calls, want 1", calls)
	}
	if len(prices) != 3 {
		t.Errorf("got %d prices, want 3", len(prices))
	}
}

func TestGroupByQuoteCurrency_OneFails_OthersSurvive(t *testing.T) {
	at := &fakeAt{
		name: "src",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			// Fail any partition addressed in JPY.
			for _, qq := range q {
				if qq.Pair.QuoteCurrency == "JPY" {
					return nil, nil, errors.New("boom")
				}
			}
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOne(qq, at))
			}
			return out, nil, nil
		},
	}
	wrapped := GroupByQuoteCurrency(at).(api.AtSource)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "BMW", QuoteCurrency: "EUR"}},
		{Pair: api.Pair{Commodity: "TOYOTA", QuoteCurrency: "JPY"}},
	}
	prices, diags, err := wrapped.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("QuoteAt err = %v, want nil (errors should be diags)", err)
	}
	if len(prices) != 2 {
		t.Errorf("got %d prices, want 2", len(prices))
	}
	gotCurrencies := make(map[string]bool)
	for _, p := range prices {
		gotCurrencies[p.Amount.Currency] = true
	}
	for _, want := range []string{"USD", "EUR"} {
		if !gotCurrencies[want] {
			t.Errorf("missing price for %s", want)
		}
	}
	// Diagnostic must mention the failing quote currency.
	var found bool
	for _, d := range diags {
		if d.Code == "quote-fetch-error" && strings.Contains(d.Message, "JPY") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing quote-fetch-error diag for JPY, got: %+v", diags)
	}
}

func TestGroupByQuoteCurrency_PreservesSubInterfaces(t *testing.T) {
	t.Run("AtOnly", func(t *testing.T) {
		src := &fakeAt{
			name: "x",
			caps: api.Capabilities{SupportsAt: true},
			handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
				return nil, nil, nil
			},
		}
		wrapped := GroupByQuoteCurrency(src)
		if _, ok := wrapped.(api.AtSource); !ok {
			t.Errorf("AtOnly: GroupByQuoteCurrency lost AtSource")
		}
		if _, ok := wrapped.(api.LatestSource); ok {
			t.Errorf("AtOnly: GroupByQuoteCurrency unexpectedly added LatestSource")
		}
		if _, ok := wrapped.(api.RangeSource); ok {
			t.Errorf("AtOnly: GroupByQuoteCurrency unexpectedly added RangeSource")
		}
	})
	t.Run("LatestOnly", func(t *testing.T) {
		src := &fakeLatest{
			name: "x",
			caps: api.Capabilities{SupportsLatest: true},
			handle: func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
				return nil, nil, nil
			},
		}
		wrapped := GroupByQuoteCurrency(src)
		if _, ok := wrapped.(api.LatestSource); !ok {
			t.Errorf("LatestOnly: GroupByQuoteCurrency lost LatestSource")
		}
		if _, ok := wrapped.(api.AtSource); ok {
			t.Errorf("LatestOnly: GroupByQuoteCurrency unexpectedly added AtSource")
		}
		if _, ok := wrapped.(api.RangeSource); ok {
			t.Errorf("LatestOnly: GroupByQuoteCurrency unexpectedly added RangeSource")
		}
	})
	t.Run("RangeOnly", func(t *testing.T) {
		src := &fakeRange{
			name: "x",
			caps: api.Capabilities{SupportsRange: true},
			handle: func(context.Context, []api.SourceQuery, time.Time, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
				return nil, nil, nil
			},
		}
		wrapped := GroupByQuoteCurrency(src)
		if _, ok := wrapped.(api.RangeSource); !ok {
			t.Errorf("RangeOnly: GroupByQuoteCurrency lost RangeSource")
		}
		if _, ok := wrapped.(api.AtSource); ok {
			t.Errorf("RangeOnly: GroupByQuoteCurrency unexpectedly added AtSource")
		}
		if _, ok := wrapped.(api.LatestSource); ok {
			t.Errorf("RangeOnly: GroupByQuoteCurrency unexpectedly added LatestSource")
		}
	})
}

// fakeAll implements all three sub-interfaces for the all-shapes test.
type fakeAll struct {
	name         string
	caps         api.Capabilities
	handleLatest func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
	handleAt     func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error)
	handleRange  func(context.Context, []api.SourceQuery, time.Time, time.Time) ([]ast.Price, []ast.Diagnostic, error)
}

func (f *fakeAll) Name() string                   { return f.name }
func (f *fakeAll) Capabilities() api.Capabilities { return f.caps }
func (f *fakeAll) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handleLatest(ctx, q)
}
func (f *fakeAll) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handleAt(ctx, q, at)
}
func (f *fakeAll) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handleRange(ctx, q, start, end)
}

func TestGroupByQuoteCurrency_AllSubInterfaces(t *testing.T) {
	emit := func(q []api.SourceQuery, when time.Time) []ast.Price {
		out := make([]ast.Price, 0, len(q))
		for _, qq := range q {
			out = append(out, priceOne(qq, when))
		}
		return out
	}

	type observed struct {
		mu          sync.Mutex
		latestCalls [][]string
		atCalls     [][]string
		rangeCalls  [][]string
	}
	var obs observed
	recordQCs := func(q []api.SourceQuery) []string {
		seen := make(map[string]struct{})
		var qcs []string
		for _, qq := range q {
			if _, ok := seen[qq.Pair.QuoteCurrency]; !ok {
				seen[qq.Pair.QuoteCurrency] = struct{}{}
				qcs = append(qcs, qq.Pair.QuoteCurrency)
			}
		}
		return qcs
	}

	src := &fakeAll{
		name: "all",
		caps: api.Capabilities{SupportsLatest: true, SupportsAt: true, SupportsRange: true},
		handleLatest: func(_ context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			obs.mu.Lock()
			obs.latestCalls = append(obs.latestCalls, recordQCs(q))
			obs.mu.Unlock()
			return emit(q, time.Time{}), nil, nil
		},
		handleAt: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			obs.mu.Lock()
			obs.atCalls = append(obs.atCalls, recordQCs(q))
			obs.mu.Unlock()
			return emit(q, at), nil, nil
		},
		handleRange: func(_ context.Context, q []api.SourceQuery, start, _ time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			obs.mu.Lock()
			obs.rangeCalls = append(obs.rangeCalls, recordQCs(q))
			obs.mu.Unlock()
			return emit(q, start), nil, nil
		},
	}

	wrapped := GroupByQuoteCurrency(src)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "BMW", QuoteCurrency: "EUR"}},
		{Pair: api.Pair{Commodity: "TOYOTA", QuoteCurrency: "JPY"}},
	}

	asLatest := wrapped.(api.LatestSource)
	if prices, _, err := asLatest.QuoteLatest(context.Background(), queries); err != nil || len(prices) != 3 {
		t.Errorf("Latest: prices=%d err=%v, want 3 prices nil err", len(prices), err)
	}
	asAt := wrapped.(api.AtSource)
	if prices, _, err := asAt.QuoteAt(context.Background(), queries, utcDate(2024, time.January, 5)); err != nil || len(prices) != 3 {
		t.Errorf("At: prices=%d err=%v, want 3 prices nil err", len(prices), err)
	}
	asRange := wrapped.(api.RangeSource)
	if prices, _, err := asRange.QuoteRange(context.Background(), queries, utcDate(2024, time.January, 1), utcDate(2024, time.January, 5)); err != nil || len(prices) != 3 {
		t.Errorf("Range: prices=%d err=%v, want 3 prices nil err", len(prices), err)
	}

	// Each shape received three downstream calls (one per quote currency).
	check := func(label string, calls [][]string) {
		t.Helper()
		if len(calls) != 3 {
			t.Errorf("%s: got %d downstream calls, want 3", label, len(calls))
			return
		}
		var got []string
		for _, c := range calls {
			if len(c) != 1 {
				t.Errorf("%s: downstream call carried %d quote currencies, want 1: %+v", label, len(c), c)
				continue
			}
			got = append(got, c[0])
		}
		sort.Strings(got)
		want := []string{"EUR", "JPY", "USD"}
		for i := range want {
			if i >= len(got) || got[i] != want[i] {
				t.Errorf("%s: downstream qcs = %v, want %v", label, got, want)
				break
			}
		}
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	check("Latest", obs.latestCalls)
	check("At", obs.atCalls)
	check("Range", obs.rangeCalls)
}

func TestGroupByQuoteCurrency_CtxCancel_ReturnsPartials(t *testing.T) {
	// USD partition completes immediately; JPY partition blocks on hold.
	hold := make(chan struct{})
	jpyEntered := make(chan struct{})
	usdDone := make(chan struct{})

	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			isJPY := false
			for _, qq := range q {
				if qq.Pair.QuoteCurrency == "JPY" {
					isJPY = true
					break
				}
			}
			if isJPY {
				close(jpyEntered)
				select {
				case <-hold:
				case <-ctx.Done():
				}
				return nil, nil, ctx.Err()
			}
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, priceOne(qq, at))
			}
			close(usdDone)
			return out, nil, nil
		},
	}
	wrapped := GroupByQuoteCurrency(at).(api.AtSource)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "TOYOTA", QuoteCurrency: "JPY"}},
	}

	ctx, cancel := context.WithCancel(context.Background())

	type out struct {
		prices []ast.Price
		diags  []ast.Diagnostic
		err    error
	}
	done := make(chan out, 1)
	go func() {
		ps, ds, err := wrapped.QuoteAt(ctx, queries, utcDate(2024, time.January, 5))
		done <- out{prices: ps, diags: ds, err: err}
	}()

	// Wait for the JPY partition to actually be running and for the USD
	// partition to have returned its result before cancelling, so the
	// test reliably exercises in-flight cancellation with USD's result
	// already captured rather than depending on goroutine scheduling.
	select {
	case <-usdDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("USD partition did not complete before deadline")
	}
	select {
	case <-jpyEntered:
	case <-time.After(2 * time.Second):
		t.Fatalf("JPY partition did not enter wrapped source before deadline")
	}
	cancel()
	close(hold)

	select {
	case got := <-done:
		if got.err == nil {
			t.Errorf("err = nil, want ctx.Err() after cancel")
		}
		// USD partition completed before cancellation; its price must
		// still be returned alongside ctx.Err().
		var sawUSD bool
		for _, p := range got.prices {
			if p.Amount.Currency == "USD" {
				sawUSD = true
				break
			}
		}
		if !sawUSD {
			t.Errorf("missing USD partial price in cancellation result; got %+v", got.prices)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("QuoteAt did not return after ctx cancel")
	}
}

func TestGroupByQuoteCurrency_EmptyInput(t *testing.T) {
	var calls int
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			calls++
			return nil, nil, nil
		},
	}
	wrapped := GroupByQuoteCurrency(at).(api.AtSource)
	prices, diags, err := wrapped.QuoteAt(context.Background(), nil, utcDate(2024, time.January, 5))
	if err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
	if calls != 0 {
		t.Errorf("got %d downstream calls, want 0", calls)
	}
	if len(prices) != 0 {
		t.Errorf("got %d prices, want 0", len(prices))
	}
	if len(diags) != 0 {
		t.Errorf("got %d diags, want 0", len(diags))
	}
}
