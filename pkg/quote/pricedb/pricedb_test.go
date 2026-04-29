package pricedb_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/printer"
	"github.com/yugui/go-beancount/pkg/quote/pricedb"
)

func decimal(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return *d
}

func amount(num, cur string) ast.Amount {
	return ast.Amount{Number: decimal(num), Currency: cur}
}

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// dateLoc parses a YYYY-MM-DD date as midnight in the given location.
// It is used by TestDedup_TimezoneNormalisedToUTC to construct two
// timestamps that disagree on the wall-clock date but represent the
// same UTC day.
func dateLoc(s string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDedup_NoDuplicates(t *testing.T) {
	prices := []ast.Price{
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-02"), Commodity: "AAPL", Amount: amount("101.00", "USD")},
		{Date: date("2024-01-03"), Commodity: "GOOG", Amount: amount("200.00", "USD")},
	}
	kept, diags := pricedb.Dedup(prices, true)
	if len(kept) != 3 {
		t.Errorf("kept len = %d, want 3", len(kept))
	}
	if len(diags) != 0 {
		t.Errorf("diags len = %d, want 0; diags = %+v", len(diags), diags)
	}
}

func TestDedup_SameKeyKeepsFirst(t *testing.T) {
	prices := []ast.Price{
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("999.00", "USD")},
	}
	kept, diags := pricedb.Dedup(prices, true)
	if len(kept) != 1 {
		t.Fatalf("kept len = %d, want 1", len(kept))
	}
	if got := kept[0].Amount.Number.Text('f'); got != "100.00" {
		t.Errorf("kept[0].Number = %s, want 100.00", got)
	}
	if len(diags) != 1 {
		t.Fatalf("diags len = %d, want 1", len(diags))
	}
	if diags[0].Code != pricedb.CodeDuplicate {
		t.Errorf("diags[0].Code = %q, want %q", diags[0].Code, pricedb.CodeDuplicate)
	}
	if diags[0].Severity != ast.Warning {
		t.Errorf("diags[0].Severity = %v, want Warning", diags[0].Severity)
	}
}

func TestDedup_SameKeyKeepsLast(t *testing.T) {
	prices := []ast.Price{
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("999.00", "USD")},
	}
	kept, diags := pricedb.Dedup(prices, false)
	if len(kept) != 1 {
		t.Fatalf("kept len = %d, want 1", len(kept))
	}
	if got := kept[0].Amount.Number.Text('f'); got != "999.00" {
		t.Errorf("kept[0].Number = %s, want 999.00", got)
	}
	if len(diags) != 1 {
		t.Fatalf("diags len = %d, want 1", len(diags))
	}
	if diags[0].Code != pricedb.CodeDuplicate {
		t.Errorf("diags[0].Code = %q, want %q", diags[0].Code, pricedb.CodeDuplicate)
	}
}

func TestDedup_DifferentQuoteNotDuplicate(t *testing.T) {
	prices := []ast.Price{
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("15000.00", "JPY")},
	}
	kept, diags := pricedb.Dedup(prices, true)
	if len(kept) != 2 {
		t.Errorf("kept len = %d, want 2", len(kept))
	}
	if len(diags) != 0 {
		t.Errorf("diags len = %d, want 0; diags = %+v", len(diags), diags)
	}
}

func TestDedup_DifferentDayNotDuplicate(t *testing.T) {
	prices := []ast.Price{
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-02"), Commodity: "AAPL", Amount: amount("101.00", "USD")},
	}
	kept, diags := pricedb.Dedup(prices, true)
	if len(kept) != 2 {
		t.Errorf("kept len = %d, want 2", len(kept))
	}
	if len(diags) != 0 {
		t.Errorf("diags len = %d, want 0; diags = %+v", len(diags), diags)
	}
}

func TestDedup_TimezoneNormalisedToUTC(t *testing.T) {
	// Two timestamps that print with different wall-clock dates but
	// refer to the same UTC calendar day (2024-01-01):
	//
	//   Tokyo (+09:00):    2024-01-02 00:00 JST == 2024-01-01 15:00 UTC.
	//   New York (-05:00): 2024-01-01 00:00 EST == 2024-01-01 05:00 UTC.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("LoadLocation Asia/Tokyo: %v", err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("LoadLocation America/New_York: %v", err)
	}
	prices := []ast.Price{
		{
			Date:      dateLoc("2024-01-02", tokyo),
			Commodity: "AAPL",
			Amount:    amount("100.00", "USD"),
		},
		{
			Date:      dateLoc("2024-01-01", ny),
			Commodity: "AAPL",
			Amount:    amount("999.00", "USD"),
		},
	}
	kept, diags := pricedb.Dedup(prices, true)
	if len(kept) != 1 {
		t.Fatalf("kept len = %d, want 1", len(kept))
	}
	if len(diags) != 1 {
		t.Fatalf("diags len = %d, want 1", len(diags))
	}
	if diags[0].Code != pricedb.CodeDuplicate {
		t.Errorf("diags[0].Code = %q, want %q", diags[0].Code, pricedb.CodeDuplicate)
	}
}

func TestFormatStream_Order(t *testing.T) {
	// Inputs are deliberately scrambled across all three sort axes.
	prices := []ast.Price{
		{Date: date("2024-01-02"), Commodity: "GOOG", Amount: amount("200.00", "USD")},
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-02"), Commodity: "AAPL", Amount: amount("101.00", "USD")},
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("15000.00", "JPY")},
	}

	// Build the expected output by passing a hand-sorted slice through
	// printer.Fprint directly; that way later printer formatting tweaks
	// won't break this test.
	wantSorted := []ast.Price{
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("15000.00", "JPY")},
		{Date: date("2024-01-01"), Commodity: "AAPL", Amount: amount("100.00", "USD")},
		{Date: date("2024-01-02"), Commodity: "AAPL", Amount: amount("101.00", "USD")},
		{Date: date("2024-01-02"), Commodity: "GOOG", Amount: amount("200.00", "USD")},
	}
	var wantBuf bytes.Buffer
	for i := range wantSorted {
		if err := printer.Fprint(&wantBuf, &wantSorted[i]); err != nil {
			t.Fatalf("Fprint expected: %v", err)
		}
	}

	var gotBuf bytes.Buffer
	if err := pricedb.FormatStream(&gotBuf, prices); err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	if diff := cmp.Diff(wantBuf.String(), gotBuf.String()); diff != "" {
		t.Errorf("FormatStream output mismatch (-want +got):\n%s", diff)
	}
}

func TestFormatStream_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := pricedb.FormatStream(&buf, nil); err != nil {
		t.Fatalf("FormatStream(nil): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("FormatStream(nil) wrote %d bytes, want 0", buf.Len())
	}

	if err := pricedb.FormatStream(&buf, []ast.Price{}); err != nil {
		t.Fatalf("FormatStream(empty): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("FormatStream(empty) wrote %d bytes, want 0", buf.Len())
	}
}
