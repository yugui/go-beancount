package quote

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// mockSource implements api.Source plus all three sub-interfaces. The
// per-method callbacks default to panicking so that tests fail loudly
// when the orchestrator dispatches to a method the test did not set
// up.
type mockSource struct {
	name    string
	caps    api.Capabilities
	latest  func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
	at      func(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error)
	rangeFn func(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error)
}

func (m *mockSource) Name() string                   { return m.name }
func (m *mockSource) Capabilities() api.Capabilities { return m.caps }

func (m *mockSource) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	if m.latest == nil {
		panic("mockSource.QuoteLatest called but not set on " + m.name)
	}
	return m.latest(ctx, q)
}

func (m *mockSource) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	if m.at == nil {
		panic("mockSource.QuoteAt called but not set on " + m.name)
	}
	return m.at(ctx, q, at)
}

func (m *mockSource) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	if m.rangeFn == nil {
		panic("mockSource.QuoteRange called but not set on " + m.name)
	}
	return m.rangeFn(ctx, q, start, end)
}

// mapRegistry is a Registry backed by a plain map; tests use it
// instead of touching the package-global registry.
type mapRegistry map[string]api.Source

func (r mapRegistry) Lookup(name string) (api.Source, bool) {
	s, ok := r[name]
	return s, ok
}

// mkPrice constructs an ast.Price for a Pair with a small decimal
// value; the actual numeric value is irrelevant for these tests.
func mkPrice(t *testing.T, pair api.Pair) ast.Price {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString("1"); err != nil {
		t.Fatalf("apd.Decimal.SetString(%q): %v", "1", err)
	}
	return ast.Price{
		Date:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Commodity: pair.Commodity,
		Amount:    ast.Amount{Number: d, Currency: pair.QuoteCurrency},
	}
}

func mkRequest(commodity, currency string, refs ...api.SourceRef) api.PriceRequest {
	return api.PriceRequest{
		Pair:    api.Pair{Commodity: commodity, QuoteCurrency: currency},
		Sources: refs,
	}
}

func ref(source, symbol string) api.SourceRef {
	return api.SourceRef{Source: source, Symbol: symbol}
}

// utcDay is a TZ-naïve calendar date at 0:00 UTC, used for At/Start/End.
func utcDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestFetch_LatestOnly_OneSource(t *testing.T) {
	pair := api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return []ast.Price{mkPrice(t, pair)}, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{mkRequest("AAPL", "USD", ref("yahoo", "AAPL"))}
	prices, diags, err := FetchLatest(context.Background(), reg, requests)
	if err != nil {
		t.Fatalf("FetchLatest returned error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1", len(prices))
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diags: %+v", diags)
	}
}

func TestFetch_AtMode_DemotedFromLatest_InWindow(t *testing.T) {
	pair := api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}
	at := utcDay(2024, time.March, 1)
	now := at.Add(2 * time.Hour) // within [at, at+24h)
	var called int32
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt32(&called, 1)
			return []ast.Price{mkPrice(t, pair)}, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{mkRequest("AAPL", "USD", ref("yahoo", "AAPL"))}
	prices, diags, err := FetchAt(context.Background(), reg, requests, at, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("FetchAt returned error: %v", err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("QuoteLatest called %d times, want 1", called)
	}
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1", len(prices))
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diags: %+v", diags)
	}
}

func TestFetch_AtMode_DemotedFromLatest_OutOfWindow(t *testing.T) {
	at := utcDay(2024, time.March, 1)
	now := at.Add(48 * time.Hour) // outside [at, at+24h)
	var called int32
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt32(&called, 1)
			return nil, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{mkRequest("AAPL", "USD", ref("yahoo", "AAPL"))}
	prices, diags, err := FetchAt(context.Background(), reg, requests, at, WithClock(func() time.Time { return now }))
	if !errors.Is(err, ErrZeroPrices) {
		t.Errorf("err = %v, want ErrZeroPrices", err)
	}
	if len(prices) != 0 {
		t.Errorf("len(prices) = %d, want 0", len(prices))
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("QuoteLatest unexpectedly called (%d times)", called)
	}
	if len(diags) == 0 {
		t.Fatalf("expected at least one diagnostic, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "quote-mode-unsupported" && d.Severity == ast.Warning {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("did not find quote-mode-unsupported Warning in diags: %+v", diags)
	}
}

func TestFetch_AlwaysBatchesPerSource(t *testing.T) {
	at := utcDay(2024, time.March, 1)
	var (
		mu      sync.Mutex
		atCalls [][]api.SourceQuery
	)
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsAt: true},
		at: func(ctx context.Context, q []api.SourceQuery, _ time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			cp := append([]api.SourceQuery(nil), q...)
			atCalls = append(atCalls, cp)
			mu.Unlock()
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, mkPrice(t, qq.Pair))
			}
			return out, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL")),
		mkRequest("GOOG", "USD", ref("yahoo", "GOOG")),
	}
	prices, _, err := FetchAt(context.Background(), reg, requests, at)
	if err != nil {
		t.Fatalf("FetchAt returned error: %v", err)
	}
	if len(prices) != 2 {
		t.Errorf("len(prices) = %d, want 2", len(prices))
	}
	if len(atCalls) != 1 {
		t.Fatalf("source.QuoteAt invoked %d times, want 1", len(atCalls))
	}
	if len(atCalls[0]) != 2 {
		t.Errorf("batched call had %d queries, want 2", len(atCalls[0]))
	}
}

func TestFetch_RangeMode_PassesFullRange(t *testing.T) {
	start := utcDay(2024, time.March, 1)
	end := utcDay(2024, time.March, 15)
	var (
		mu       sync.Mutex
		gotStart time.Time
		gotEnd   time.Time
		gotCalls int
	)
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsRange: true},
		rangeFn: func(ctx context.Context, q []api.SourceQuery, s, e time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			mu.Lock()
			gotStart, gotEnd = s, e
			gotCalls++
			mu.Unlock()
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				out = append(out, mkPrice(t, qq.Pair))
			}
			return out, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL")),
	}
	prices, _, err := FetchRange(context.Background(), reg, requests, start, end)
	if err != nil {
		t.Fatalf("FetchRange returned error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1", len(prices))
	}
	if gotCalls != 1 {
		t.Errorf("source.QuoteRange invoked %d times, want 1", gotCalls)
	}
	if !gotStart.Equal(start) || !gotEnd.Equal(end) {
		t.Errorf("got [%v, %v), want [%v, %v)", gotStart, gotEnd, start, end)
	}
}

func TestFetchRange_StartNotBeforeEnd_ReturnsError(t *testing.T) {
	day := utcDay(2024, time.March, 1)
	reg := mapRegistry{}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL")),
	}

	// start == end: empty interval.
	if _, _, err := FetchRange(context.Background(), reg, requests, day, day); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("FetchRange(start==end) err = %v, want ErrInvalidRange", err)
	}
	// start > end: inverted interval.
	if _, _, err := FetchRange(context.Background(), reg, requests, day.AddDate(0, 0, 1), day); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("FetchRange(start>end) err = %v, want ErrInvalidRange", err)
	}
}

func TestFetch_PrimaryFails_FallbackSucceeds(t *testing.T) {
	pair := api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}
	primary := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	fallback := &mockSource{
		name: "google",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return []ast.Price{mkPrice(t, pair)}, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": primary, "google": fallback}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL"), ref("google", "AAPL")),
	}
	prices, _, err := FetchLatest(context.Background(), reg, requests)
	if err != nil {
		t.Fatalf("FetchLatest returned error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1", len(prices))
	}
}

func TestFetch_PrimarySucceeds_FallbackNotCalled(t *testing.T) {
	pair := api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}
	primary := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return []ast.Price{mkPrice(t, pair)}, nil, nil
		},
	}
	fallback := &mockSource{
		name: "google",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			panic("fallback must not be called when primary succeeds")
		},
	}
	reg := mapRegistry{"yahoo": primary, "google": fallback}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL"), ref("google", "AAPL")),
	}
	prices, _, err := FetchLatest(context.Background(), reg, requests)
	if err != nil {
		t.Fatalf("FetchLatest returned error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1", len(prices))
	}
}

func TestFetch_DeadlockRegression_SharedBatchSources(t *testing.T) {
	pairA := api.Pair{Commodity: "A", QuoteCurrency: "USD"}
	pairB := api.Pair{Commodity: "B", QuoteCurrency: "USD"}

	makeSource := func(name string, fail map[api.Pair]bool) *mockSource {
		var calls int32
		s := &mockSource{
			name: name,
			caps: api.Capabilities{SupportsLatest: true},
		}
		s.latest = func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt32(&calls, 1)
			out := []ast.Price{}
			for _, qq := range q {
				if fail[qq.Pair] {
					continue
				}
				out = append(out, mkPrice(t, qq.Pair))
			}
			return out, nil, nil
		}
		return s
	}

	// yahoo fails for A at level 0 (A has yahoo as primary), succeeds for B at level 1.
	yahoo := makeSource("yahoo", map[api.Pair]bool{pairA: true})
	// google fails for B at level 0 (B has google as primary), succeeds for A at level 1.
	google := makeSource("google", map[api.Pair]bool{pairB: true})

	var yahooCalls, googleCalls int32
	yahooFn := yahoo.latest
	yahoo.latest = func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
		atomic.AddInt32(&yahooCalls, 1)
		return yahooFn(ctx, q)
	}
	googleFn := google.latest
	google.latest = func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
		atomic.AddInt32(&googleCalls, 1)
		return googleFn(ctx, q)
	}

	reg := mapRegistry{"yahoo": yahoo, "google": google}
	requests := []api.PriceRequest{
		mkRequest("A", "USD", ref("yahoo", "A"), ref("google", "A")),
		mkRequest("B", "USD", ref("google", "B"), ref("yahoo", "B")),
	}

	done := make(chan struct{})
	var (
		prices []ast.Price
		err    error
	)
	go func() {
		prices, _, err = FetchLatest(context.Background(), reg, requests)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("FetchLatest deadlocked under shared-batch-source fallback")
	}

	if err != nil {
		t.Fatalf("FetchLatest returned error: %v", err)
	}
	if len(prices) != 2 {
		t.Errorf("len(prices) = %d, want 2", len(prices))
	}
	if got := atomic.LoadInt32(&yahooCalls); got != 2 {
		t.Errorf("yahoo called %d times, want 2", got)
	}
	if got := atomic.LoadInt32(&googleCalls); got != 2 {
		t.Errorf("google called %d times, want 2", got)
	}
}

func TestFetch_CtxCancellation(t *testing.T) {
	block := make(chan struct{})
	ready := make(chan struct{})
	var readyOnce sync.Once
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			readyOnce.Do(func() { close(ready) })
			select {
			case <-block:
				return nil, nil, nil
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{mkRequest("AAPL", "USD", ref("yahoo", "AAPL"))}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		_, _, err := FetchLatest(ctx, reg, requests)
		resCh <- result{err: err}
	}()
	// Wait for the source goroutine to signal it has entered its
	// blocking select before cancelling.
	<-ready
	cancel()
	close(block)

	select {
	case r := <-resCh:
		if r.err == nil {
			t.Fatal("FetchLatest returned nil error after ctx cancellation")
		}
		if !errors.Is(r.err, context.Canceled) {
			t.Errorf("FetchLatest err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchLatest did not return after ctx cancellation")
	}
}

func TestFetch_UnknownSource(t *testing.T) {
	reg := mapRegistry{}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("nope", "AAPL")),
	}
	prices, diags, err := FetchLatest(context.Background(), reg, requests)
	if err == nil {
		t.Fatal("FetchLatest returned nil error, want non-nil")
	}
	if len(prices) != 0 {
		t.Errorf("len(prices) = %d, want 0", len(prices))
	}
	found := false
	for _, d := range diags {
		if d.Code == "quote-source-unknown" {
			found = true
			if !strings.Contains(d.Message, "nope") {
				t.Errorf("diag message %q does not mention source name", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("missing quote-source-unknown diag, got: %+v", diags)
	}
}

func TestFetch_ZeroPrices_ReturnsError(t *testing.T) {
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, errors.New("boom")
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL")),
	}
	prices, _, err := FetchLatest(context.Background(), reg, requests)
	if err == nil {
		t.Fatal("FetchLatest returned nil error, want non-nil")
	}
	if len(prices) != 0 {
		t.Errorf("len(prices) = %d, want 0", len(prices))
	}
}

func TestFetch_OneOfManySucceeds_ReturnsNil(t *testing.T) {
	pair := api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}
	good := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return []ast.Price{mkPrice(t, pair)}, nil, nil
		},
	}
	bad := &mockSource{
		name: "google",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, errors.New("boom")
		},
	}
	reg := mapRegistry{"yahoo": good, "google": bad}
	requests := []api.PriceRequest{
		mkRequest("AAPL", "USD", ref("yahoo", "AAPL")),
		mkRequest("GOOG", "USD", ref("google", "GOOG")),
	}
	prices, diags, err := FetchLatest(context.Background(), reg, requests)
	if err != nil {
		t.Fatalf("FetchLatest returned error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1", len(prices))
	}
	if len(diags) == 0 {
		t.Errorf("expected diagnostics for the failing chain, got none")
	}
}

func TestFetch_ConcurrencyCap(t *testing.T) {
	const capN = 2
	const numSources = 5

	var (
		mu          sync.Mutex
		inFlight    int
		maxInFlight int
	)
	release := make(chan struct{})

	reg := mapRegistry{}
	requests := make([]api.PriceRequest, 0, numSources)
	for i := 0; i < numSources; i++ {
		name := string(rune('a' + i))
		commodity := string(rune('A' + i))
		pair := api.Pair{Commodity: commodity, QuoteCurrency: "USD"}
		src := &mockSource{
			name: name,
			caps: api.Capabilities{SupportsLatest: true},
			latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
				mu.Lock()
				inFlight++
				if inFlight > maxInFlight {
					maxInFlight = inFlight
				}
				mu.Unlock()
				<-release
				mu.Lock()
				inFlight--
				mu.Unlock()
				out := make([]ast.Price, 0, len(q))
				for _, qq := range q {
					out = append(out, mkPrice(t, qq.Pair))
				}
				return out, nil, nil
			},
		}
		reg[name] = src
		requests = append(requests, api.PriceRequest{
			Pair:    pair,
			Sources: []api.SourceRef{ref(name, commodity)},
		})
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := FetchLatest(context.Background(), reg, requests, WithConcurrency(capN))
		done <- err
	}()

	// Wait until we observe the cap reached, then unblock everyone.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		current := inFlight
		mu.Unlock()
		if current >= capN {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never observed in-flight reaching %d (max so far %d)", capN, current)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("FetchLatest returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchLatest did not return")
	}

	mu.Lock()
	gotMax := maxInFlight
	mu.Unlock()
	if gotMax > capN {
		t.Errorf("max in-flight = %d, want <= %d", gotMax, capN)
	}
	if gotMax < 1 {
		t.Errorf("max in-flight = %d, want >= 1", gotMax)
	}
}

func TestFetch_Observer_LevelEvents(t *testing.T) {
	pair := api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}
	src := &mockSource{
		name: "yahoo",
		caps: api.Capabilities{SupportsLatest: true},
		latest: func(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return []ast.Price{mkPrice(t, pair)}, nil, nil
		},
	}
	reg := mapRegistry{"yahoo": src}
	requests := []api.PriceRequest{mkRequest("AAPL", "USD", ref("yahoo", "AAPL"))}

	var (
		mu     sync.Mutex
		events []Event
	)
	obs := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	if _, _, err := FetchLatest(context.Background(), reg, requests, WithObserver(obs)); err != nil {
		t.Fatalf("FetchLatest returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	var gotStart, gotCallStart, gotCallDone, gotEnd bool
	for _, e := range events {
		switch e.Kind {
		case EventLevelStart:
			gotStart = true
		case EventCallStart:
			if gotEnd {
				t.Errorf("EventCallStart after EventLevelEnd")
			}
			gotCallStart = true
		case EventCallDone:
			if !gotCallStart {
				t.Errorf("EventCallDone before EventCallStart")
			}
			gotCallDone = true
		case EventLevelEnd:
			if !gotStart {
				t.Errorf("EventLevelEnd before EventLevelStart")
			}
			gotEnd = true
		}
	}
	if !gotStart {
		t.Error("missing EventLevelStart")
	}
	if !gotCallStart {
		t.Error("missing EventCallStart")
	}
	if !gotCallDone {
		t.Error("missing EventCallDone")
	}
	if !gotEnd {
		t.Error("missing EventLevelEnd")
	}
}
