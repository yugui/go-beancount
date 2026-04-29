//go:build live

package ecb

import (
	"context"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/quote/api"
)

// TestECB_Live_Daily hits the production ECB endpoint with the daily
// feed and verifies it parses without error. Gated behind the `live`
// build tag so CI stays hermetic; run locally with
// `go test -tags live ./pkg/quote/std/ecb/...`.
func TestECB_Live_Daily(t *testing.T) {
	s := &Source{} // zero value; uses defaults for Client/Now/BaseURL.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prices, diags, err := s.QuoteLatest(ctx, []api.SourceQuery{
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}},
		{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "JPY"}},
	})
	if err != nil {
		t.Fatalf("QuoteLatest err = %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want empty", diags)
	}
	if len(prices) == 0 {
		t.Errorf("got 0 prices from live ECB endpoint, want >= 1")
	}
}
