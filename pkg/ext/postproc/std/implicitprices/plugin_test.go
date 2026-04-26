package implicitprices

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// astCmpOpts is the standard option set for comparing AST values
// produced by the plugin. apd.Decimal carries an internal
// representation (BigInt with unexported fields) that cmp.Diff cannot
// inspect by default, and time.Time has unexported monotonic-clock
// state — both need a custom comparer that defers to the type's own
// equality semantics.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

// dec parses a decimal literal, failing the test on a parse error.
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("apd.SetString(%q): %v", s, err)
	}
	return d
}

// amt builds an Amount from a decimal-literal string and currency.
func amt(t *testing.T, n, cur string) ast.Amount {
	t.Helper()
	return ast.Amount{Number: dec(t, n), Currency: cur}
}

// filterPrices returns only the *ast.Price directives in ds, in order.
func filterPrices(ds []ast.Directive) []*ast.Price {
	var out []*ast.Price
	for _, d := range ds {
		if p, ok := d.(*ast.Price); ok {
			out = append(out, p)
		}
	}
	return out
}

// TestSimpleCostBased: a buy posting carrying a per-unit cost
// annotation ({564.20 USD}) yields one Price directive on the
// transaction date with the cost as price.
func TestSimpleCostBased(t *testing.T) {
	units := amt(t, "10", "HOOL")
	per := amt(t, "564.20", "USD")
	txDate := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{PerUnit: &per},
			},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}

	want := []*ast.Price{{
		Date:      txDate,
		Commodity: "HOOL",
		Amount:    amt(t, "564.20", "USD"),
	}}
	if diff := cmp.Diff(want, filterPrices(res.Directives), astCmpOpts); diff != "" {
		t.Errorf("synthesized Prices mismatch (-want +got):\n%s", diff)
	}
}

// TestExplicitPriceAnnotation: an `@ X CUR` posting yields a Price
// with the annotation's per-unit value, on the transaction date.
func TestExplicitPriceAnnotation(t *testing.T) {
	units := amt(t, "100", "USD")
	pa := &ast.PriceAnnotation{
		Amount:  amt(t, "1.10", "CAD"),
		IsTotal: false,
	}
	txDate := time.Date(2024, 2, 14, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &units, Price: pa},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []*ast.Price{{
		Date:      txDate,
		Commodity: "USD",
		Amount:    amt(t, "1.10", "CAD"),
	}}
	if diff := cmp.Diff(want, filterPrices(res.Directives), astCmpOpts); diff != "" {
		t.Errorf("synthesized Prices mismatch (-want +got):\n%s", diff)
	}
}

// TestTotalPriceAnnotation: an `@@ X CUR` total-price annotation
// yields a Price with per-unit derived from total/|units|.
func TestTotalPriceAnnotation(t *testing.T) {
	units := amt(t, "100", "USD")
	pa := &ast.PriceAnnotation{
		Amount:  amt(t, "110", "CAD"),
		IsTotal: true,
	}
	txDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &units, Price: pa},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Fatalf("len(prices) = %d, want 1; res = %#v", len(prices), res.Directives)
	}
	if prices[0].Commodity != "USD" || prices[0].Amount.Currency != "CAD" {
		t.Errorf("price commodities = (%s, %s), want (USD, CAD)", prices[0].Commodity, prices[0].Amount.Currency)
	}
	want := dec(t, "1.10")
	if prices[0].Amount.Number.Cmp(&want) != 0 {
		t.Errorf("price number = %s, want 1.10 (== 110/100)", prices[0].Amount.Number.String())
	}
	if !prices[0].Date.Equal(txDate) {
		t.Errorf("price date = %v, want %v", prices[0].Date, txDate)
	}
}

// TestTotalCostAnnotation: a `{{T CUR}}` total-form cost annotation
// yields a Price with per-unit derived from total/|units|.
func TestTotalCostAnnotation(t *testing.T) {
	units := amt(t, "10", "HOOL")
	tot := amt(t, "5642", "USD")
	txDate := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{Total: &tot},
			},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Fatalf("len(prices) = %d, want 1; res = %#v", len(prices), res.Directives)
	}
	want := dec(t, "564.2")
	if prices[0].Amount.Number.Cmp(&want) != 0 {
		t.Errorf("price number = %s, want 564.2 (== 5642/10)", prices[0].Amount.Number.String())
	}
	if prices[0].Commodity != "HOOL" || prices[0].Amount.Currency != "USD" {
		t.Errorf("price commodities = (%s, %s), want (HOOL, USD)", prices[0].Commodity, prices[0].Amount.Currency)
	}
}

// TestNoCostNoPriceNoEmission: a plain posting (no cost, no price
// annotation) emits no Price directive — the output contains exactly
// the original directives.
func TestNoCostNoPriceNoEmission(t *testing.T) {
	pos := amt(t, "100", "USD")
	neg := amt(t, "-100", "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(filterPrices(res.Directives)); got != 0 {
		t.Errorf("len(filterPrices) = %d, want 0 (no annotations on either posting)", got)
	}
	if len(res.Directives) != 1 {
		t.Errorf("len(res.Directives) = %d, want 1 (only the original tx)", len(res.Directives))
	}
}

// TestMultiplePostingsMultipleEmissions: a transaction whose postings
// each carry a price/cost annotation yields one Price per posting,
// preserving posting order.
func TestMultiplePostingsMultipleEmissions(t *testing.T) {
	units1 := amt(t, "10", "HOOL")
	per1 := amt(t, "100", "USD")
	units2 := amt(t, "5", "AAPL")
	pa2 := &ast.PriceAnnotation{Amount: amt(t, "150", "USD"), IsTotal: false}
	txDate := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units1, Cost: &ast.CostSpec{PerUnit: &per1}},
			{Account: "Assets:Brokerage", Amount: &units2, Price: pa2},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []*ast.Price{
		{Date: txDate, Commodity: "HOOL", Amount: amt(t, "100", "USD")},
		{Date: txDate, Commodity: "AAPL", Amount: amt(t, "150", "USD")},
	}
	if diff := cmp.Diff(want, filterPrices(res.Directives), astCmpOpts); diff != "" {
		t.Errorf("synthesized Prices mismatch (-want +got):\n%s", diff)
	}
}

// TestPreservesOriginalDirectives: every input directive — Open,
// Price, Transaction, Note — appears in the output slice in addition
// to any synthesized Prices.
func TestPreservesOriginalDirectives(t *testing.T) {
	openDir := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	priceDir := &ast.Price{
		Date:      time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
		Commodity: "EUR",
		Amount:    amt(t, "1.10", "USD"),
	}
	noteDir := &ast.Note{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Comment: "year start",
	}
	units := amt(t, "10", "HOOL")
	per := amt(t, "100", "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units, Cost: &ast.CostSpec{PerUnit: &per}},
		},
	}
	input := []ast.Directive{openDir, priceDir, noteDir, tx}
	in := api.Input{Directives: seqOf(input)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All four originals must appear by pointer identity.
	for _, want := range input {
		found := false
		for _, got := range res.Directives {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("input directive %T %p not preserved in output", want, want)
		}
	}
	// At least one synthesized Price (the cost-derived one for the
	// transaction).
	prices := filterPrices(res.Directives)
	if len(prices) < 2 { // priceDir + 1 synthesized
		t.Errorf("len(filterPrices) = %d, want >= 2 (input Price + synthesized)", len(prices))
	}
}

// TestEmptyInput: an Input with no directives yields a Result with
// nil Directives (the no-change signal). A non-nil empty slice would
// instruct the runner to clear the ledger, which is the wrong
// semantics for a no-op pass.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directives: seqOf(nil)}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for empty input", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestNilDirectivesIterator: an Input with nil Directives is also a
// no-op.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestCanceledContext: the plugin honors context cancellation at
// entry. The exact error returned mirrors ctx.Err().
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
	if err != ctx.Err() {
		t.Errorf("apply error = %v, want ctx.Err() = %v", err, ctx.Err())
	}
}

// TestNoDirectiveMutation: input directives must not be mutated;
// synthesized prices must not share pointers with input data.
func TestNoDirectiveMutation(t *testing.T) {
	units := amt(t, "10", "HOOL")
	per := amt(t, "100", "USD")
	cost := &ast.CostSpec{PerUnit: &per}
	tx := &ast.Transaction{
		Date: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units, Cost: cost},
		},
	}

	origPostingsLen := len(tx.Postings)
	origAccount := tx.Postings[0].Account
	origCostPtr := tx.Postings[0].Cost
	origAmtPtr := tx.Postings[0].Amount
	origPerNum := per.Number

	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tx.Postings) != origPostingsLen {
		t.Errorf("apply mutated tx.Postings length: %d -> %d", origPostingsLen, len(tx.Postings))
	}
	if tx.Postings[0].Account != origAccount {
		t.Errorf("apply mutated tx.Postings[0].Account: %q -> %q", origAccount, tx.Postings[0].Account)
	}
	if tx.Postings[0].Cost != origCostPtr {
		t.Errorf("apply mutated tx.Postings[0].Cost pointer")
	}
	if tx.Postings[0].Amount != origAmtPtr {
		t.Errorf("apply mutated tx.Postings[0].Amount pointer")
	}
	if per.Number.Cmp(&origPerNum) != 0 {
		t.Errorf("apply mutated cost per-unit number: %s -> %s", origPerNum.String(), per.Number.String())
	}

	// The synthesized Price's Amount.Number must be a freshly
	// allocated decimal — we mutate it and verify the original
	// posting cost is untouched.
	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Fatalf("len(prices) = %d, want 1", len(prices))
	}
	prices[0].Amount.Number.SetInt64(99999)
	if per.Number.Cmp(&origPerNum) != 0 {
		t.Errorf("mutating synthesized Price.Amount.Number leaked into input cost: %s vs original %s", per.Number.String(), origPerNum.String())
	}
}

// TestExactPriceFields: the synthesized Price has the exact Date,
// Commodity, and Amount expected — locking the field-mapping contract
// upstream relies on (units.currency → base; cost/price.currency →
// quote; transaction date → emission date).
func TestExactPriceFields(t *testing.T) {
	units := amt(t, "10", "HOOL")
	per := amt(t, "564.20", "USD")
	txDate := time.Date(2024, 7, 4, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units, Cost: &ast.CostSpec{PerUnit: &per}},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Fatalf("len(prices) = %d, want 1", len(prices))
	}
	got := prices[0]
	if !got.Date.Equal(txDate) {
		t.Errorf("Price.Date = %v, want %v", got.Date, txDate)
	}
	if got.Commodity != "HOOL" {
		t.Errorf("Price.Commodity = %q, want %q (== units.currency)", got.Commodity, "HOOL")
	}
	if got.Amount.Currency != "USD" {
		t.Errorf("Price.Amount.Currency = %q, want %q (== cost.currency)", got.Amount.Currency, "USD")
	}
	wantNum := dec(t, "564.20")
	if got.Amount.Number.Cmp(&wantNum) != 0 {
		t.Errorf("Price.Amount.Number = %s, want 564.20", got.Amount.Number.String())
	}
}

// TestZeroUnitsHandledGracefully: a posting whose units number is
// zero with a total-form annotation is skipped silently — no panic,
// no diagnostic, no synthesized Price (would be a divide-by-zero).
// A posting whose units currency is empty is similarly skipped.
func TestZeroUnitsHandledGracefully(t *testing.T) {
	zeroUnits := amt(t, "0", "HOOL")
	tot := amt(t, "0", "USD")
	emptyUnits := ast.Amount{Number: dec(t, "1"), Currency: ""}
	pa := &ast.PriceAnnotation{Amount: amt(t, "1.10", "CAD"), IsTotal: true}
	txDate := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			// Zero units with a total-cost annotation.
			{Account: "Assets:Brokerage", Amount: &zeroUnits, Cost: &ast.CostSpec{Total: &tot}},
			// Zero units with a total-price annotation.
			{Account: "Assets:Brokerage", Amount: &zeroUnits, Price: pa},
			// Empty-currency units (any annotation is skipped).
			{Account: "Assets:Brokerage", Amount: &emptyUnits, Price: pa},
		},
	}

	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error (must not panic on zero units): %v", err)
	}
	if got := len(filterPrices(res.Directives)); got != 0 {
		t.Errorf("len(filterPrices) = %d, want 0 (zero/empty-currency units skip silently); prices = %#v", got, filterPrices(res.Directives))
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestSynthesizedPriceSpan: each synthesized Price carries its
// originating transaction's Span so downstream consumers can trace
// provenance back to the source posting in the absence of
// `__implicit_prices__` metadata.
func TestSynthesizedPriceSpan(t *testing.T) {
	units1 := amt(t, "10", "HOOL")
	per1 := amt(t, "100", "USD")
	units2 := amt(t, "5", "AAPL")
	pa2 := &ast.PriceAnnotation{Amount: amt(t, "150", "USD"), IsTotal: false}
	txSpan := ast.Span{
		Start: ast.Position{Filename: "ledger.beancount", Line: 42, Column: 1},
		End:   ast.Position{Filename: "ledger.beancount", Line: 45, Column: 1},
	}
	tx := &ast.Transaction{
		Span: txSpan,
		Date: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units1, Cost: &ast.CostSpec{PerUnit: &per1}},
			{Account: "Assets:Brokerage", Amount: &units2, Price: pa2},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prices := filterPrices(res.Directives)
	if len(prices) != 2 {
		t.Fatalf("len(prices) = %d, want 2", len(prices))
	}
	for i, p := range prices {
		if p.Span != txSpan {
			t.Errorf("prices[%d].Span = %+v, want %+v (originating tx span)", i, p.Span, txSpan)
		}
	}
}

// TestCombinedCostForm: combined-form cost ({X # T CUR}) yields a
// synthesized per-unit price of X + T/|units|. With X = 1 USD,
// T = 2 USD, units = 10 HOOL, the expected per-unit price is 1.2 USD.
func TestCombinedCostForm(t *testing.T) {
	units := amt(t, "10", "HOOL")
	per := amt(t, "1", "USD")
	tot := amt(t, "2", "USD")
	txDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{PerUnit: &per, Total: &tot},
			},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}

	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Fatalf("len(prices) = %d, want 1", len(prices))
	}
	want := dec(t, "1.2")
	if prices[0].Amount.Number.Cmp(&want) != 0 {
		t.Errorf("price number = %s, want 1.2 (== 1 + 2/10)", prices[0].Amount.Number.String())
	}
	if prices[0].Commodity != "HOOL" || prices[0].Amount.Currency != "USD" {
		t.Errorf("price commodities = (%s, %s), want (HOOL, USD)", prices[0].Commodity, prices[0].Amount.Currency)
	}
}

// TestNegativeUnitsTotalForm: a posting with negative units and a
// total-form cost annotation derives the per-unit price using the
// absolute value of units. With units = -10 HOOL and total = 200 USD,
// the expected per-unit price is +20 USD.
func TestNegativeUnitsTotalForm(t *testing.T) {
	units := amt(t, "-10", "HOOL")
	tot := amt(t, "200", "USD")
	txDate := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{Total: &tot},
			},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Fatalf("len(prices) = %d, want 1", len(prices))
	}
	want := dec(t, "20")
	if prices[0].Amount.Number.Cmp(&want) != 0 {
		t.Errorf("price number = %s, want +20 (== 200/|-10|)", prices[0].Amount.Number.String())
	}
	if prices[0].Amount.Number.Sign() <= 0 {
		t.Errorf("price number sign = %d, want >0 (absolute-value path)", prices[0].Amount.Number.Sign())
	}
}

// TestCanceledContextMidLoop: a context canceled before apply runs
// returns ctx.Err() promptly even when the input contains directives,
// exercising the in-loop cancellation poll.
func TestCanceledContextMidLoop(t *testing.T) {
	units := amt(t, "10", "HOOL")
	per := amt(t, "100", "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units, Cost: &ast.CostSpec{PerUnit: &per}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{Directives: seqOf([]ast.Directive{tx})})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
	if err != ctx.Err() {
		t.Errorf("apply error = %v, want ctx.Err() = %v", err, ctx.Err())
	}
}

// TestDeduplication: when two postings would emit the same
// (date, base, quote, number) Price, only one is synthesized.
// Upstream maintains the same dedup map; this test pins the behavior.
func TestDeduplication(t *testing.T) {
	units := amt(t, "10", "HOOL")
	per := amt(t, "100", "USD")
	txDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Brokerage", Amount: &units, Cost: &ast.CostSpec{PerUnit: &per}},
			{Account: "Assets:Brokerage", Amount: &units, Cost: &ast.CostSpec{PerUnit: &per}},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prices := filterPrices(res.Directives)
	if len(prices) != 1 {
		t.Errorf("len(prices) = %d, want 1 (dedup on identical (date, base, quote, number)); prices = %#v", len(prices), prices)
	}
}
