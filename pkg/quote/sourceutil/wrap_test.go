package sourceutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/quote/api"
)

func TestWrapSingleCellCapabilitiesAndName(t *testing.T) {
	src := WrapSingleCell("test", func(context.Context, api.Pair, time.Time) (apd.Decimal, error) {
		return apd.Decimal{}, nil
	})
	if got := src.Name(); got != "test" {
		t.Errorf("Name() = %q, want %q", got, "test")
	}
	caps := src.Capabilities()
	if !caps.SupportsAt {
		t.Errorf("Capabilities().SupportsAt = false, want true")
	}
	if caps.SupportsLatest || caps.SupportsRange {
		t.Errorf("unexpected sub-interface flags: %+v", caps)
	}
}

func TestWrapSingleCellInvokesPerQueryInOrder(t *testing.T) {
	var calls []api.Pair
	src := WrapSingleCell("test", func(_ context.Context, p api.Pair, at time.Time) (apd.Decimal, error) {
		calls = append(calls, p)
		var d apd.Decimal
		_, _, _ = d.SetString("1.5")
		return d, nil
	})
	at := utcDate(2024, time.January, 5)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}, Symbol: "AAPL"},
		{Pair: api.Pair{Commodity: "GOOG", QuoteCurrency: "USD"}, Symbol: "GOOG"},
		{Pair: api.Pair{Commodity: "MSFT", QuoteCurrency: "USD"}, Symbol: "MSFT"},
	}
	prices, diags, err := src.QuoteAt(context.Background(), queries, at)
	if err != nil {
		t.Fatalf("QuoteAt returned error: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
	if len(calls) != 3 || calls[0].Commodity != "AAPL" || calls[1].Commodity != "GOOG" || calls[2].Commodity != "MSFT" {
		t.Errorf("calls = %+v, want AAPL, GOOG, MSFT in order", calls)
	}
	if len(prices) != 3 {
		t.Fatalf("got %d prices, want 3", len(prices))
	}
	for i, p := range prices {
		if !p.Date.Equal(at) {
			t.Errorf("prices[%d].Date = %v, want %v", i, p.Date, at)
		}
		if p.Amount.Currency != "USD" {
			t.Errorf("prices[%d].Amount.Currency = %q, want USD", i, p.Amount.Currency)
		}
	}
}

func TestWrapSingleCellPerCellErrorBecomesDiagnostic(t *testing.T) {
	src := WrapSingleCell("test", func(_ context.Context, p api.Pair, _ time.Time) (apd.Decimal, error) {
		if p.Commodity == "BAD" {
			return apd.Decimal{}, errors.New("boom")
		}
		var d apd.Decimal
		_, _, _ = d.SetString("1")
		return d, nil
	})
	at := utcDate(2024, time.January, 5)
	queries := []api.SourceQuery{
		{Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"}, Symbol: "AAPL"},
		{Pair: api.Pair{Commodity: "BAD", QuoteCurrency: "USD"}, Symbol: "BAD"},
		{Pair: api.Pair{Commodity: "GOOG", QuoteCurrency: "USD"}, Symbol: "GOOG"},
	}
	prices, diags, err := src.QuoteAt(context.Background(), queries, at)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prices) != 2 {
		t.Errorf("got %d prices, want 2", len(prices))
	}
	if len(diags) != 1 {
		t.Errorf("got %d diags, want 1", len(diags))
	}
}

func TestWrapSingleCellHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := WrapSingleCell("test", func(context.Context, api.Pair, time.Time) (apd.Decimal, error) {
		t.Fatal("fn should not be called when ctx is already cancelled")
		return apd.Decimal{}, nil
	})
	queries := []api.SourceQuery{{Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"}}}
	_, _, err := src.QuoteAt(ctx, queries, utcDate(2024, time.January, 5))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
