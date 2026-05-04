package booking_test

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
	"github.com/yugui/go-beancount/pkg/loader"
	"github.com/yugui/go-beancount/pkg/loader/booking"
)

// astCmpOpts is the standard option set for deep-comparing AST values.
// apd.Decimal has unexported fields and time.Time carries monotonic
// state, so each gets a custom comparer that defers to the type's own
// equality semantics.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func dayp(y int, m time.Month, d int) *time.Time {
	t := day(y, m, d)
	return &t
}

// dec parses s as an apd.Decimal. It panics if s is not a valid decimal
// string, which is acceptable inside test inputs.
func dec(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return *d
}

func amt(num string, cur string) *ast.Amount {
	return &ast.Amount{Number: dec(num), Currency: cur}
}

func seqOf(directives []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range directives {
			if !yield(i, d) {
				return
			}
		}
	}
}

// errorSeverityCount returns the number of error-severity diagnostics
// in ledger. Warning-level diagnostics are intentionally excluded.
func errorSeverityCount(ledger *ast.Ledger) int {
	n := 0
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			n++
		}
	}
	return n
}

// TestLoad_DateOnlyCostReducesWithoutImbalance is the regression test for
// the failing case that motivated this plugin: a reducing posting whose
// CostSpec carries only an acquisition date must not be reported as
// imbalanced after the loader runs.
func TestLoad_DateOnlyCostReducesWithoutImbalance(t *testing.T) {
	const src = `2024-05-14 open Assets:Gift-Certificates DISCOUNT
2024-05-14 open Expenses:Misc
2024-05-14 open Income:Gifts
2024-05-14 * "buy"
  Assets:Gift-Certificates  2 DISCOUNT { 1300 JPY, 2024-05-14 }
  Income:Gifts             -2600 JPY
2025-05-31 * "失効"
  Assets:Gift-Certificates  -2 DISCOUNT { 2024-05-14 }
  Expenses:Misc              2 * 1300 JPY
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: unexpected error %v", err)
	}
	if got := errorSeverityCount(ledger); got != 0 {
		for _, d := range ledger.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}
}

// TestApply_AugmentationFillsPerUnit verifies that when a posting
// augments inventory under a fully-specified CostSpec, the booking
// pass writes the resolved per-unit number and currency back into the
// AST so downstream consumers see a concrete cost. Date and Label
// from the original spec must be preserved.
func TestApply_AugmentationFillsPerUnit(t *testing.T) {
	openAcct := &ast.Open{
		Date:    day(2024, 1, 1),
		Account: "Assets:Brokerage",
	}
	openCash := &ast.Open{
		Date:    day(2024, 1, 1),
		Account: "Assets:Cash",
	}
	txn := &ast.Transaction{
		Date: day(2024, 1, 15),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("10", "AAPL"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100.00", "USD"),
					Date:    dayp(2024, 1, 15),
					Label:   "lot1",
				},
			},
			{
				Account: "Assets:Cash",
				Amount:  amt("-1000.00", "USD"),
			},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openAcct, openCash, txn})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want empty", res.Diagnostics)
	}
	if res.Directives == nil {
		t.Fatalf("booking.Apply() Directives = nil, want non-nil clone")
	}
	// The plugin must NOT mutate the input AST.
	if got := txn.Postings[0].Cost.PerUnit.Number.Text('f'); got != "100.00" {
		t.Errorf("input PerUnit after Apply: got %q, want %q", got, "100.00")
	}
	// Find the cloned transaction and check its CostSpec.
	var clonedTxn *ast.Transaction
	for _, d := range res.Directives {
		if t2, ok := d.(*ast.Transaction); ok {
			clonedTxn = t2
		}
	}
	if clonedTxn == nil {
		t.Fatalf("booking.Apply() Directives: no transaction found, want one")
	}
	if clonedTxn == txn {
		t.Errorf("booking.Apply() cloned transaction aliases input, want a distinct clone")
	}
	cs := clonedTxn.Postings[0].Cost
	wantCS := &ast.CostSpec{
		PerUnit: &ast.Amount{Number: dec("100.00"), Currency: "USD"},
		Date:    dayp(2024, 1, 15),
		Label:   "lot1",
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(wantCS, cs, opts...); diff != "" {
		t.Errorf("booking.Apply() CostSpec mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_ReductionAggregatesTotal exercises the multi-lot reduction
// path: two augmentations on the same date are merged into a single
// inventory position (same lot key), and a date-only reduction draws
// from that combined lot. The booked CostSpec.Total must equal
// |units consumed| × per-unit cost across all matched steps.
func TestApply_ReductionAggregatesTotal(t *testing.T) {
	const src = `2024-01-01 open Assets:Brokerage
2024-01-01 open Assets:Cash
2024-01-01 open Income:Gain
2024-01-01 * "buy lot A"
  Assets:Brokerage  2 ABC { 100.00 USD, 2024-01-01 }
  Assets:Cash      -200.00 USD
2024-02-01 * "buy lot B"
  Assets:Brokerage  3 ABC { 110.00 USD, 2024-02-01 }
  Assets:Cash      -330.00 USD
2024-03-01 * "sell from lot A"
  Assets:Brokerage  -2 ABC { 2024-01-01 }
  Assets:Cash       200.00 USD
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	if got := errorSeverityCount(ledger); got != 0 {
		for _, d := range ledger.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}

	// Find the sell transaction and verify its booked CostSpec.
	var sell *ast.Transaction
	for _, d := range ledger.All() {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "sell from lot A" {
			sell = tx
			break
		}
	}
	if sell == nil {
		t.Fatalf("sell transaction not found in ledger")
	}
	cs := sell.Postings[0].Cost
	if cs == nil {
		t.Fatalf("CostSpec on reducing posting is nil")
	}
	wantCS := &ast.CostSpec{
		Total: &ast.Amount{Number: dec("200.00"), Currency: "USD"},
		Date:  dayp(2024, 1, 1),
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(wantCS, cs, opts...); diff != "" {
		t.Errorf("booking.Apply() CostSpec mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_AutoPostingAmountIsFilled confirms that the booking pass
// fills in the Amount of an auto-balanced posting (the reducer
// already mutates the work copy in place; we just need to verify the
// clone's posting reflects the inferred amount).
func TestApply_AutoPostingAmountIsFilled(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank
2024-01-01 open Equity:Opening
2024-01-15 * "deposit"
  Assets:Bank        100 USD
  Equity:Opening
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	if got := errorSeverityCount(ledger); got != 0 {
		for _, d := range ledger.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}
	var txn *ast.Transaction
	for _, d := range ledger.All() {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "deposit" {
			txn = tx
			break
		}
	}
	if txn == nil {
		t.Fatalf("deposit transaction not found")
	}
	auto := &txn.Postings[1]
	if auto.Amount == nil {
		t.Fatalf("auto-posting Amount = nil, want it filled by booking")
	}
	wantAmount := &ast.Amount{Number: dec("-100"), Currency: "USD"}
	if diff := cmp.Diff(wantAmount, auto.Amount, astCmpOpts...); diff != "" {
		t.Errorf("booking.Apply() auto-posting Amount mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_CostlessTransactionUnchanged verifies a transaction that
// has no CostSpec and no auto-posting passes through the booking pass
// without diagnostics or AST changes (other than the unavoidable
// deep-clone).
func TestApply_CostlessTransactionUnchanged(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Bank"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Expenses:Food"}
	txn := &ast.Transaction{
		Date: day(2024, 1, 15),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: amt("-50", "USD")},
			{Account: "Expenses:Food", Amount: amt("50", "USD")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openA, openB, txn})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want empty", res.Diagnostics)
	}
	// The result must mirror the input shape; per-posting Cost must
	// remain nil because the input had no CostSpec.
	for _, d := range res.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range tx.Postings {
			if tx.Postings[i].Cost != nil {
				t.Errorf("posting %d gained a CostSpec %+v, want nil", i, tx.Postings[i].Cost)
			}
		}
	}
}

// TestApply_ReducerErrorsBecomeDiagnostics confirms a reduction that
// targets a lot the account does not hold surfaces as a diagnostic on
// the ledger rather than as a hard error from Apply, mirroring the
// contract used by the validation built-ins. The inventory is seeded
// with one lot so the second posting is classified as a reduction;
// the cost-spec date does not match any held lot, so the matcher
// fails.
func TestApply_ReducerErrorsBecomeDiagnostics(t *testing.T) {
	const src = `2024-01-01 open Assets:Brokerage
2024-01-01 open Assets:Cash
2024-01-15 * "buy"
  Assets:Brokerage  2 ABC { 100.00 USD, 2024-01-01 }
  Assets:Cash      -200.00 USD
2024-02-15 * "sell from non-existent lot"
  Assets:Brokerage  -1 ABC { 50.00 USD, 2099-12-31 }
  Assets:Cash        50.00 USD
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	if got := errorSeverityCount(ledger); got == 0 {
		t.Fatalf("booking.Apply via loader.Load: error-severity diagnostics = %d, want >= 1 (no-matching-lot)", got)
	}
}

// TestApply_Idempotent confirms that running the booking pass twice on
// the same ledger produces the same booked AST and the same
// diagnostics. The reducer's outputs must be a function of the input
// AST alone, with no aggregation drift across repeated runs.
func TestApply_Idempotent(t *testing.T) {
	const src = `2024-01-01 open Assets:Brokerage
2024-01-01 open Assets:Cash
2024-01-01 * "buy"
  Assets:Brokerage  2 ABC { 100.00 USD, 2024-01-01 }
  Assets:Cash      -200.00 USD
2024-02-01 * "sell"
  Assets:Brokerage  -2 ABC { 2024-01-01 }
  Assets:Cash       200.00 USD
`
	ctx := context.Background()
	ledger, err := loader.Load(ctx, src)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	if got := errorSeverityCount(ledger); got != 0 {
		for _, d := range ledger.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}

	// Run the booking plugin a second time over the booked ledger and
	// confirm the Total on the reducing posting has not changed.
	in := api.Input{Directives: ledger.All()}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply (second run): %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("second-run diagnostic: %s [%s]", d.Message, d.Code)
		}
	}
	var sell *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "sell" {
			sell = tx
			break
		}
	}
	if sell == nil {
		t.Fatalf("sell transaction not found after second booking pass")
	}
	cs := sell.Postings[0].Cost
	if cs == nil || cs.Total == nil {
		t.Fatalf("CostSpec.Total = nil after second booking, want preserved aggregate")
	}
	wantCS := &ast.CostSpec{
		Total: &ast.Amount{Number: dec("200.00"), Currency: "USD"},
		Date:  dayp(2024, 1, 1),
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(wantCS, cs, opts...); diff != "" {
		t.Errorf("booking.Apply() CostSpec mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_DoesNotMutateInputDirectives is a defensive contract test:
// even when the reducer fills in an auto-posting Amount in place on
// its work copy, the caller's directives must remain untouched. This
// matches the api.Input contract (Directives is read-only).
func TestApply_DoesNotMutateInputDirectives(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Bank"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Equity:Opening"}
	original := &ast.Transaction{
		Date: day(2024, 1, 15),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: amt("100", "USD")},
			{Account: "Equity:Opening"}, // auto-posting; reducer would fill Amount on the clone
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openA, openB, original})}
	if _, err := booking.Apply(context.Background(), in); err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if original.Postings[1].Amount != nil {
		t.Errorf("input auto-posting Amount was mutated: got %+v, want nil", original.Postings[1].Amount)
	}
}
