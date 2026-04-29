package ecb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// fixedNow is the simulated current time used by tests. It is one day
// after the most recent date in the fixtures, so daily/90d/hist
// dispatch decisions are deterministic.
func fixedNow() time.Time {
	return time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
}

// newTestServer returns an httptest server that maps the ECB feed
// filenames to the corresponding fixture files in testdata/. handler,
// if non-nil, is consulted first and may override the default
// behaviour (e.g. assert on the request URL or return errors).
func newTestServer(t *testing.T, override func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		base := strings.TrimPrefix(r.URL.Path, "/")
		var fixture string
		switch base {
		case dailyFile:
			fixture = "daily.xml"
		case hist90dFile:
			fixture = "hist-90d.xml"
		case histFile:
			fixture = "hist.xml"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestSource(srv *httptest.Server) *Source {
	return &Source{
		Client:  srv.Client(),
		Now:     fixedNow,
		BaseURL: srv.URL,
	}
}

// pairEUR builds a SourceQuery with EUR as the base.
func pairEUR(qccy string) api.SourceQuery {
	return api.SourceQuery{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: qccy}}
}

func TestECB_Name(t *testing.T) {
	if got := (&Source{}).Name(); got != "ecb" {
		t.Errorf("Name() = %q, want %q", got, "ecb")
	}
}

func TestECB_Capabilities(t *testing.T) {
	caps := (&Source{}).Capabilities()
	want := api.Capabilities{SupportsLatest: true, SupportsAt: true, SupportsRange: true}
	if diff := cmp.Diff(want, caps); diff != "" {
		t.Errorf("Capabilities() mismatch (-want +got):\n%s", diff)
	}
}

func TestECB_QuoteLatest(t *testing.T) {
	srv := newTestServer(t, nil)
	s := newTestSource(srv)

	prices, diags, err := s.QuoteLatest(context.Background(), []api.SourceQuery{
		pairEUR("USD"),
		pairEUR("JPY"),
	})
	if err != nil {
		t.Fatalf("QuoteLatest err = %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want empty", diags)
	}
	if len(prices) != 2 {
		t.Fatalf("prices length = %d, want 2", len(prices))
	}
	wantDate := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	for _, p := range prices {
		if p.Commodity != "EUR" {
			t.Errorf("Commodity = %q, want EUR", p.Commodity)
		}
		if !p.Date.Equal(wantDate) {
			t.Errorf("Date = %v, want %v", p.Date, wantDate)
		}
	}
	// Spot-check the USD rate.
	for _, p := range prices {
		if p.Amount.Currency == "USD" {
			if got := p.Amount.Number.String(); got != "1.0823" {
				t.Errorf("USD rate = %q, want 1.0823", got)
			}
		}
	}
}

func TestECB_QuoteLatest_NonEURBase(t *testing.T) {
	srv := newTestServer(t, nil)
	s := newTestSource(srv)

	prices, diags, err := s.QuoteLatest(context.Background(), []api.SourceQuery{
		{Pair: api.Pair{Commodity: "USD", QuoteCurrency: "JPY"}},
		pairEUR("USD"),
	})
	if err != nil {
		t.Fatalf("QuoteLatest err = %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %d, want 1: %v", len(diags), diags)
	}
	d := diags[0]
	if d.Code != "quote-source-mismatch" {
		t.Errorf("diag code = %q, want quote-source-mismatch", d.Code)
	}
	if d.Severity != ast.Warning {
		t.Errorf("diag severity = %v, want Warning", d.Severity)
	}
	// The non-EUR query is skipped; only one price (the EUR/USD one) is returned.
	if len(prices) != 1 {
		t.Fatalf("prices length = %d, want 1", len(prices))
	}
	if prices[0].Commodity != "EUR" || prices[0].Amount.Currency != "USD" {
		t.Errorf("price = %+v, want EUR/USD", prices[0])
	}
}

func TestECB_QuoteAt(t *testing.T) {
	srv := newTestServer(t, nil)
	s := newTestSource(srv)

	at := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	prices, diags, err := s.QuoteAt(context.Background(), []api.SourceQuery{
		pairEUR("USD"),
		pairEUR("JPY"),
	}, at)
	if err != nil {
		t.Fatalf("QuoteAt err = %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want empty", diags)
	}
	if len(prices) != 2 {
		t.Fatalf("prices length = %d, want 2", len(prices))
	}
	for _, p := range prices {
		if !p.Date.Equal(at) {
			t.Errorf("Date = %v, want %v", p.Date, at)
		}
	}

	// A date the fixture does not cover yields no prices and no diagnostic.
	missing := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	prices, diags, err = s.QuoteAt(context.Background(), []api.SourceQuery{pairEUR("USD")}, missing)
	if err != nil {
		t.Fatalf("QuoteAt(missing) err = %v", err)
	}
	if len(prices) != 0 {
		t.Errorf("prices = %d, want 0", len(prices))
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want empty", diags)
	}
}

func TestECB_QuoteAt_OlderThan90d(t *testing.T) {
	var requested []string
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		requested = append(requested, strings.TrimPrefix(r.URL.Path, "/"))
		// Defer to the default static-file handler.
		return false
	})
	s := newTestSource(srv)

	// 2024-06-03 is well over 90 days before fixedNow (2026-04-26).
	at := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)
	prices, diags, err := s.QuoteAt(context.Background(), []api.SourceQuery{
		pairEUR("USD"),
		pairEUR("JPY"),
	}, at)
	if err != nil {
		t.Fatalf("QuoteAt err = %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want empty", diags)
	}
	if len(prices) != 2 {
		t.Fatalf("prices length = %d, want 2", len(prices))
	}
	if len(requested) != 1 || requested[0] != histFile {
		t.Errorf("fetched URLs = %v, want [%s]", requested, histFile)
	}
}

func TestECB_QuoteRange(t *testing.T) {
	srv := newTestServer(t, nil)
	s := newTestSource(srv)

	start := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC) // half-open: 23, 24, 25
	prices, diags, err := s.QuoteRange(context.Background(), []api.SourceQuery{
		pairEUR("USD"),
		pairEUR("JPY"),
	}, start, end)
	if err != nil {
		t.Fatalf("QuoteRange err = %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want empty", diags)
	}
	if len(prices) != 6 {
		t.Fatalf("prices length = %d, want 6", len(prices))
	}
	// Verify that GBP (which we did not request) is not included.
	for _, p := range prices {
		if p.Amount.Currency == "GBP" {
			t.Errorf("unexpected GBP price: %+v", p)
		}
	}
	// Half-open exclusivity: 2026-04-26 must not appear (no fixture
	// covers it anyway, but check that 2026-04-23 inclusively does).
	sawStart := false
	for _, p := range prices {
		if p.Date.Equal(start) {
			sawStart = true
		}
		if !p.Date.Before(end) {
			t.Errorf("price beyond end: %+v", p)
		}
	}
	if !sawStart {
		t.Errorf("missing start-of-range prices")
	}
}

func TestECB_HTTPNon2xx(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		http.Error(w, "boom", http.StatusInternalServerError)
		return true
	})
	s := newTestSource(srv)

	_, _, err := s.QuoteLatest(context.Background(), []api.SourceQuery{pairEUR("USD")})
	if err == nil {
		t.Fatalf("QuoteLatest err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want it to mention HTTP 500", err)
	}
}

func TestECB_CtxCancelled(t *testing.T) {
	// ready is closed by the handler the moment the request reaches
	// the server; the cancel goroutine waits for that signal so the
	// cancel happens deterministically after the request has begun,
	// independent of dialer/scheduler timing.
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		// Block until the request context is cancelled; the client
		// then observes context.Canceled from http.Client.Do.
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	s := &Source{
		Client:  srv.Client(),
		Now:     fixedNow,
		BaseURL: srv.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ready
		cancel()
	}()
	_, _, err := s.QuoteLatest(ctx, []api.SourceQuery{pairEUR("USD")})
	<-done
	if err == nil {
		t.Fatalf("QuoteLatest err = nil, want context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
}
