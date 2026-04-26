package onecommodity

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// errorCmpOpts compares api.Error values structurally while leaving the
// human-readable Message field to per-test substring assertions.
var errorCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(api.Error{}, "Message"),
}

// testPluginDir is a non-zero *ast.Plugin used as the api.Input.Directive
// fallback span in tests.
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "o.beancount", Line: 1}}}

func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

func amt(n int64, cur string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: cur}
}

// openSpan is a non-zero span attached to Open directives so tests can
// distinguish "anchored at Open" from "fell back to plugin span".
var openSpan = ast.Span{Start: ast.Position{Filename: "o.beancount", Line: 100}}

// TestSingleCurrency: an account that only ever sees one currency
// produces no diagnostic.
func TestSingleCurrency(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	openI := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary", Span: openSpan}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openI, tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestMultiCurrencyUnits: an account that sees two unit currencies
// produces exactly one diagnostic anchored at its Open's span. The
// credit legs use distinct income accounts so only Assets:Cash sees
// both currencies.
func TestMultiCurrencyUnits(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:JPY"}
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Income:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openIU, openIJ, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []api.Error{{Code: "multi-commodity-account", Span: openSpan}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "More than one currency in account 'Assets:Cash': JPY,USD"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
}

// TestMultiCurrencyCosts: an account that holds positions denominated in
// two cost currencies produces a separate "More than one cost currency"
// diagnostic.
func TestMultiCurrencyCosts(t *testing.T) {
	openInv := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Inv", Span: openSpan}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:JPY"}
	share := amt(1, "AAPL")
	usdCost := amt(150, "USD")
	jpyCost := amt(20000, "JPY")
	negUSD := amt(-150, "USD")
	negJPY := amt(-20000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Inv", Amount: &share, Cost: &ast.CostSpec{PerUnit: &usdCost}},
			{Account: "Assets:Cash:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Inv", Amount: &share, Cost: &ast.CostSpec{PerUnit: &jpyCost}},
			{Account: "Assets:Cash:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openInv, openIU, openIJ, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Units side sees only AAPL, no diag there. Cost side sees USD+JPY.
	want := []api.Error{{Code: "multi-commodity-account", Span: openSpan}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "More than one cost currency in account 'Assets:Inv': JPY,USD"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
}

// TestUnitAndCostBothFlagged: a single account that violates both the
// unit and the cost rule produces two diagnostics, both anchored at its
// Open span.
func TestUnitAndCostBothFlagged(t *testing.T) {
	openInv := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Inv", Span: openSpan}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:JPY"}
	aapl := amt(1, "AAPL")
	googl := amt(1, "GOOGL")
	usdCost := amt(150, "USD")
	jpyCost := amt(20000, "JPY")
	negUSD := amt(-150, "USD")
	negJPY := amt(-20000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Inv", Amount: &aapl, Cost: &ast.CostSpec{PerUnit: &usdCost}},
			{Account: "Assets:Cash:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Inv", Amount: &googl, Cost: &ast.CostSpec{PerUnit: &jpyCost}},
			{Account: "Assets:Cash:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openInv, openIU, openIJ, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("len(res.Errors) = %d, want 2 (unit + cost); errors = %v", len(res.Errors), res.Errors)
	}
	for i, e := range res.Errors {
		if e.Span != openSpan {
			t.Errorf("res.Errors[%d].Span = %#v, want openSpan %#v", i, e.Span, openSpan)
		}
	}
}

// TestNilAmountSkipped: a posting with a nil Amount (auto-balancing
// posting) is skipped silently and does not contribute to the unit set.
func TestNilAmountSkipped(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	openI := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	usd := amt(100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:Salary"}, // nil Amount, auto-balanced
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openI, tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestOptOutBoolFalse: an Open with metadata "onecommodity: FALSE"
// (MetaBool{Bool:false}) excludes the account from the check.
func TestOptOutBoolFalse(t *testing.T) {
	openA := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    openSpan,
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"onecommodity": {Kind: ast.MetaBool, Bool: false}}},
	}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:JPY"}
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Income:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openIU, openIJ, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (opted out); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestOptOutStringFalseCaseInsensitive: a MetaString value of "FALSE"
// also disables the check, matching upstream's case-insensitive parse.
func TestOptOutStringFalseCaseInsensitive(t *testing.T) {
	openA := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    openSpan,
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"onecommodity": {Kind: ast.MetaString, String: "FALSE"}}},
	}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:JPY"}
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Income:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openIU, openIJ, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (opted out via string FALSE); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestMultiCurrencyOpenIsImplicitOptOut: an Open declaring more than
// one constraint currency is an implicit opt-out — the user has stated
// the account is intentionally multi-currency.
func TestMultiCurrencyOpenIsImplicitOptOut(t *testing.T) {
	openA := &ast.Open{
		Date:       time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Cash",
		Span:       openSpan,
		Currencies: []string{"USD", "JPY"},
	}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:JPY"}
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Income:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openIU, openIJ, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (multi-currency Open is implicit opt-out); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestRegexFilterMiss: when Config restricts the check to a subtree
// that excludes the offender, no diagnostic is emitted.
func TestRegexFilterMiss(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	// Use distinct credit accounts so each one only sees a single
	// currency — without this, the credit leg account itself would
	// also match the multi-currency rule.
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:JPY"}
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Equity:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Equity:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Config:     "Income:.*",
		Directives: seqOf([]ast.Directive{openA, openIU, openIJ, tx1, tx2}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (regex excludes offender); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestInvalidRegexEmitsDiagnostic: an invalid regex produces an
// "invalid-regexp" diagnostic AND disables the filter, so the offending
// multi-currency account is still flagged.
func TestInvalidRegexEmitsDiagnostic(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	openIU := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:USD"}
	openIJ := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:JPY"}
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:USD", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Income:JPY", Amount: &negJPY},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Config:     "[",
		Directives: seqOf([]ast.Directive{openA, openIU, openIJ, tx1, tx2}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 2 {
		t.Fatalf("len(res.Errors) = %d, want 2 (invalid-regexp + flagged account); errors = %v", len(res.Errors), res.Errors)
	}
	codes := map[string]int{}
	for _, e := range res.Errors {
		codes[e.Code]++
	}
	if codes["invalid-regexp"] != 1 {
		t.Errorf("invalid-regexp count = %d, want 1; errors = %v", codes["invalid-regexp"], res.Errors)
	}
	if codes["multi-commodity-account"] != 1 {
		t.Errorf("multi-commodity-account count = %d, want 1 (filter disabled); errors = %v", codes["multi-commodity-account"], res.Errors)
	}
}

// TestNoOpenStillCheckedFallbackSpan: a referenced-only account (no
// Open directive) that sees two currencies is still flagged, with the
// diagnostic anchored at the triggering plugin directive's span.
func TestNoOpenStillCheckedFallbackSpan(t *testing.T) {
	usd := amt(100, "USD")
	jpy := amt(10000, "JPY")
	negUSD := amt(-100, "USD")
	negJPY := amt(-10000, "JPY")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Income:Salary", Amount: &negUSD},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &jpy},
			{Account: "Income:Salary", Amount: &negJPY},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both Assets:Cash and Income:Salary see two currencies; expect two
	// diagnostics, both anchored at the plugin directive's span.
	if len(res.Errors) != 2 {
		t.Fatalf("len(res.Errors) = %d, want 2; errors = %v", len(res.Errors), res.Errors)
	}
	for i, e := range res.Errors {
		if e.Span != testPluginDir.Span {
			t.Errorf("res.Errors[%d].Span = %#v, want testPluginDir.Span %#v (fallback)", i, e.Span, testPluginDir.Span)
		}
	}
}

// TestEmptyInput: an empty directive sequence yields a zero-valued
// Result.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil for empty input", res.Directives)
	}
	if res.Errors != nil {
		t.Errorf("res.Errors = %v, want nil for empty input", res.Errors)
	}
}

// TestCanceledContext: the plugin respects a canceled context and
// returns the context's error along with a zero Result.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := apply(ctx, api.Input{})
	if err != ctx.Err() {
		t.Fatalf("apply error = %v, want ctx.Err() = %v", err, ctx.Err())
	}
	var zero api.Result
	if !cmp.Equal(res, zero) {
		t.Errorf("apply result = %#v, want zero api.Result", res)
	}
}
