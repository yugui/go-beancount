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

// amt returns a *ast.Amount with the given decimal string and currency.
// It panics if num is not a valid decimal string, which is acceptable
// inside test inputs.
func amt(num string, cur string) *ast.Amount {
	return &ast.Amount{Number: dec(num), Currency: cur}
}

// seqOf adapts a directive slice into the iter.Seq2[int, ast.Directive]
// shape required by api.Input.Directives.
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
// in diags. Warning-level diagnostics are intentionally excluded.
func errorSeverityCount(diags []ast.Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.Severity == ast.Error {
			n++
		}
	}
	return n
}

// TestApply_DateOnlyCostReducesWithoutImbalance is the regression test
// for the failing case that motivated this plugin: a reducing posting
// whose CostSpec carries only an acquisition date must be resolved
// into a concrete Total by the booking pass, with no error-severity
// diagnostics surfaced.
func TestApply_DateOnlyCostReducesWithoutImbalance(t *testing.T) {
	openGC := &ast.Open{
		Date:       day(2024, 5, 14),
		Account:    "Assets:Gift-Certificates",
		Currencies: []string{"DISCOUNT"},
	}
	openExp := &ast.Open{Date: day(2024, 5, 14), Account: "Expenses:Misc"}
	openInc := &ast.Open{Date: day(2024, 5, 14), Account: "Income:Gifts"}
	buy := &ast.Transaction{
		Date:      day(2024, 5, 14),
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:Gift-Certificates",
				Amount:  amt("2", "DISCOUNT"),
				Cost: &ast.CostSpec{
					PerUnit: amt("1300", "JPY"),
					Date:    dayp(2024, 5, 14),
				},
			},
			{Account: "Income:Gifts", Amount: amt("-2600", "JPY")},
		},
	}
	sell := &ast.Transaction{
		Date:      day(2025, 5, 31),
		Flag:      '*',
		Narration: "失効",
		Postings: []ast.Posting{
			{
				Account: "Assets:Gift-Certificates",
				Amount:  amt("-2", "DISCOUNT"),
				Cost: &ast.CostSpec{
					Date: dayp(2024, 5, 14),
				},
			},
			{Account: "Expenses:Misc", Amount: amt("2600", "JPY")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openGC, openExp, openInc, buy, sell})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: unexpected error %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("booking.Apply error-severity diagnostics = %d, want 0", got)
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

// TestApply_AugmentationPreservesTotalCostSpec pins the precision-
// preserving contract of the augmentation writeback for the {{ T CUR }}
// form: the user's exact total must survive booking, because
// converting it into a per-unit number (T/|units|) is generally a
// non-terminating decimal that rounds in apd's 34-digit context and
// makes the transaction-balance validator's units × per re-derivation
// miss a true zero residual.
func TestApply_AugmentationPreservesTotalCostSpec(t *testing.T) {
	openA := &ast.Open{
		Date:       day(2025, 1, 1),
		Account:    "Assets:A",
		Currencies: []string{"JPY", "STOCK"},
		Booking:    ast.BookingNone,
	}
	openCash := &ast.Open{Date: day(2025, 1, 1), Account: "Assets:Cash"}
	txn := &ast.Transaction{
		Date:      day(2025, 1, 1),
		Flag:      '*',
		Narration: "buy with total cost",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("4.1", "STOCK"),
				Cost: &ast.CostSpec{
					Total: amt("4.2", "JPY"),
				},
			},
			{Account: "Assets:Cash", Amount: amt("-4.2", "JPY")},
		},
	}

	in := api.Input{Directives: seqOf([]ast.Directive{openA, openCash, txn})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}

	var bookedTxn *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok {
			bookedTxn = tx
			break
		}
	}
	if bookedTxn == nil {
		t.Fatalf("booking.Apply: no transaction found")
	}
	cs := bookedTxn.Postings[0].Cost
	wantCS := &ast.CostSpec{
		Total: &ast.Amount{Number: dec("4.2"), Currency: "JPY"},
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
	openBrokerage := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Brokerage"}
	openCash := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Cash"}
	// openGain is included as realistic fixture context for a sale that
	// would book a gain; the booking pass itself does not require it.
	openGain := &ast.Open{Date: day(2024, 1, 1), Account: "Income:Gain"}
	buyA := &ast.Transaction{
		Date:      day(2024, 1, 1),
		Flag:      '*',
		Narration: "buy lot A",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("2", "ABC"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100.00", "USD"),
					Date:    dayp(2024, 1, 1),
				},
			},
			{Account: "Assets:Cash", Amount: amt("-200.00", "USD")},
		},
	}
	buyB := &ast.Transaction{
		Date:      day(2024, 2, 1),
		Flag:      '*',
		Narration: "buy lot B",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("3", "ABC"),
				Cost: &ast.CostSpec{
					PerUnit: amt("110.00", "USD"),
					Date:    dayp(2024, 2, 1),
				},
			},
			{Account: "Assets:Cash", Amount: amt("-330.00", "USD")},
		},
	}
	sell := &ast.Transaction{
		Date:      day(2024, 3, 1),
		Flag:      '*',
		Narration: "sell from lot A",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("-2", "ABC"),
				Cost: &ast.CostSpec{
					Date: dayp(2024, 1, 1),
				},
			},
			{Account: "Assets:Cash", Amount: amt("200.00", "USD")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openBrokerage, openCash, openGain, buyA, buyB, sell})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("booking.Apply error-severity diagnostics = %d, want 0", got)
	}

	// Find the sell transaction in the booked output and verify its
	// CostSpec.
	var bookedSell *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "sell from lot A" {
			bookedSell = tx
			break
		}
	}
	if bookedSell == nil {
		t.Fatalf("sell transaction not found in booked directives")
	}
	cs := bookedSell.Postings[0].Cost
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

// TestApply_StrictTotalMatchPerUnitCost pins upstream beancount's
// "total match" rule end-to-end: under STRICT booking, a reduction
// whose absolute magnitude equals the sum of all matching lots must
// succeed even when the matcher resolves to multiple lots, because
// the booking is unambiguous (every matching lot is consumed in
// full). Without this rule the reducer would emit an
// ambiguous-lot-match diagnostic.
//
// When the user wrote a concrete per-unit cost on the reducing
// posting, every matching lot has that same per-unit cost by
// construction, so the AST is preserved verbatim: PerUnit stays as
// the user wrote it and no Total is synthesized. Downstream weight
// computation reads units * PerUnit, which equals the per-step sum,
// so transaction balance still checks out without a per-lot rewrite.
func TestApply_StrictTotalMatchPerUnitCost(t *testing.T) {
	openA := &ast.Open{
		Date:       day(2025, 1, 1),
		Account:    "Assets:A",
		Currencies: []string{"JPY", "STOCK"},
		Booking:    ast.BookingStrict,
	}
	openOpening := &ast.Open{Date: day(2025, 1, 1), Account: "Equity:Opening-Balances"}
	openGain := &ast.Open{Date: day(2025, 1, 1), Account: "Income:Gain"}
	buyFirst := &ast.Transaction{
		Date:      day(2025, 1, 1),
		Flag:      '*',
		Narration: "first",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Label:   "first",
				},
			},
			{Account: "Equity:Opening-Balances", Amount: amt("-1000", "JPY")},
		},
	}
	buySecond := &ast.Transaction{
		Date:      day(2025, 1, 1),
		Flag:      '*',
		Narration: "second",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Label:   "second",
				},
			},
			{Account: "Equity:Opening-Balances", Amount: amt("-1000", "JPY")},
		},
	}
	transfer := &ast.Transaction{
		Date:      day(2025, 2, 17),
		Flag:      '*',
		Narration: "transfer",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("-20", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
				},
			},
			{Account: "Assets:A", Amount: amt("3000", "JPY")},
			{Account: "Income:Gain", Amount: amt("-1000", "JPY")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openA, openOpening, openGain, buyFirst, buySecond, transfer})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("booking.Apply error-severity diagnostics = %d, want 0", got)
	}

	// The booked transfer's reducing posting must keep the user's
	// per-unit cost spec verbatim; no Total rewrite is performed
	// because every matched lot shares that per-unit cost and the
	// validation layer's PostingWeight reads units * PerUnit directly.
	var bookedTransfer *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "transfer" {
			bookedTransfer = tx
			break
		}
	}
	if bookedTransfer == nil {
		t.Fatalf("transfer transaction not found in booked directives")
	}
	cs := bookedTransfer.Postings[0].Cost
	if cs == nil {
		t.Fatalf("CostSpec on reducing posting is nil")
	}
	wantCS := &ast.CostSpec{
		PerUnit: &ast.Amount{Number: dec("100"), Currency: "JPY"},
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(wantCS, cs, opts...); diff != "" {
		t.Errorf("booking.Apply() CostSpec mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_StrictTotalMatchEmptyCost mirrors
// TestApply_StrictTotalMatchPerUnitCost but with an empty `{}` cost
// spec on the reducing posting. The empty matcher accepts every lot,
// so under STRICT this reduction is also a total match: requested
// 20 STOCK == 10 + 10 across the two held lots. After booking, the
// reducing posting's CostSpec must be filled with a concrete Total
// so the transaction-balance validator no longer sees an unbalanced
// residual in STOCK.
func TestApply_StrictTotalMatchEmptyCost(t *testing.T) {
	openA := &ast.Open{
		Date:       day(2025, 1, 1),
		Account:    "Assets:A",
		Currencies: []string{"JPY", "STOCK"},
		Booking:    ast.BookingStrict,
	}
	openOpening := &ast.Open{Date: day(2025, 1, 1), Account: "Equity:Opening-Balances"}
	openGain := &ast.Open{Date: day(2025, 1, 1), Account: "Income:Gain"}
	buyFirst := &ast.Transaction{
		Date:      day(2025, 1, 1),
		Flag:      '*',
		Narration: "first",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Label:   "first",
				},
			},
			{Account: "Equity:Opening-Balances", Amount: amt("-1000", "JPY")},
		},
	}
	buySecond := &ast.Transaction{
		Date:      day(2025, 1, 1),
		Flag:      '*',
		Narration: "second",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Label:   "second",
				},
			},
			{Account: "Equity:Opening-Balances", Amount: amt("-1000", "JPY")},
		},
	}
	transfer := &ast.Transaction{
		Date:      day(2025, 2, 17),
		Flag:      '*',
		Narration: "transfer",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("-20", "STOCK"),
				Cost:    &ast.CostSpec{}, // empty `{}`
			},
			{Account: "Assets:A", Amount: amt("3000", "JPY")},
			{Account: "Income:Gain", Amount: amt("-1000", "JPY")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openA, openOpening, openGain, buyFirst, buySecond, transfer})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("booking.Apply error-severity diagnostics = %d, want 0", got)
	}

	var bookedTransfer *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "transfer" {
			bookedTransfer = tx
			break
		}
	}
	if bookedTransfer == nil {
		t.Fatalf("transfer transaction not found in booked directives")
	}
	cs := bookedTransfer.Postings[0].Cost
	if cs == nil {
		t.Fatalf("CostSpec on reducing posting is nil")
	}
	wantCS := &ast.CostSpec{
		Total: &ast.Amount{Number: dec("2000"), Currency: "JPY"},
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
	openBank := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Bank"}
	openEquity := &ast.Open{Date: day(2024, 1, 1), Account: "Equity:Opening"}
	deposit := &ast.Transaction{
		Date:      day(2024, 1, 15),
		Flag:      '*',
		Narration: "deposit",
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: amt("100", "USD")},
			{Account: "Equity:Opening"}, // auto-posting
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openBank, openEquity, deposit})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("booking.Apply error-severity diagnostics = %d, want 0", got)
	}
	var txn *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "deposit" {
			txn = tx
			break
		}
	}
	if txn == nil {
		t.Fatalf("deposit transaction not found in booked directives")
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
	const wantCode = "no-matching-lot"
	openBrokerage := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Brokerage"}
	openCash := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Cash"}
	buy := &ast.Transaction{
		Date:      day(2024, 1, 15),
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("2", "ABC"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100.00", "USD"),
					Date:    dayp(2024, 1, 1),
				},
			},
			{Account: "Assets:Cash", Amount: amt("-200.00", "USD")},
		},
	}
	sell := &ast.Transaction{
		Date:      day(2024, 2, 15),
		Flag:      '*',
		Narration: "sell from non-existent lot",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("-1", "ABC"),
				Cost: &ast.CostSpec{
					PerUnit: amt("50.00", "USD"),
					Date:    dayp(2099, 12, 31),
				},
			},
			{Account: "Assets:Cash", Amount: amt("50.00", "USD")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openBrokerage, openCash, buy, sell})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatalf("Diagnostics = empty, want >= 1 (%s)", wantCode)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == wantCode {
			found = true
			break
		}
	}
	if !found {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("no diagnostic with Code = %q found", wantCode)
	}
}

// TestApply_Idempotent confirms that running the booking pass twice on
// the same ledger produces the same booked AST and the same
// diagnostics. The reducer's outputs must be a function of the input
// AST alone, with no aggregation drift across repeated runs.
func TestApply_Idempotent(t *testing.T) {
	openBrokerage := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Brokerage"}
	openCash := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Cash"}
	buy := &ast.Transaction{
		Date:      day(2024, 1, 1),
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("2", "ABC"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100.00", "USD"),
					Date:    dayp(2024, 1, 1),
				},
			},
			{Account: "Assets:Cash", Amount: amt("-200.00", "USD")},
		},
	}
	sell := &ast.Transaction{
		Date:      day(2024, 2, 1),
		Flag:      '*',
		Narration: "sell",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("-2", "ABC"),
				Cost: &ast.CostSpec{
					Date: dayp(2024, 1, 1),
				},
			},
			{Account: "Assets:Cash", Amount: amt("200.00", "USD")},
		},
	}

	directives := []ast.Directive{openBrokerage, openCash, buy, sell}

	// First booking pass.
	first, err := booking.Apply(context.Background(), api.Input{Directives: seqOf(directives)})
	if err != nil {
		t.Fatalf("booking.Apply (first run): %v", err)
	}
	if got := errorSeverityCount(first.Diagnostics); got != 0 {
		for _, d := range first.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("first-run error-severity diagnostics = %d, want 0", got)
	}

	// Second booking pass over the already-booked directives. The
	// reducer's outputs must be a function of the input AST alone, so
	// the booked CostSpec on the reducing posting must not drift.
	second, err := booking.Apply(context.Background(), api.Input{Directives: seqOf(first.Directives)})
	if err != nil {
		t.Fatalf("booking.Apply (second run): %v", err)
	}
	if got := errorSeverityCount(second.Diagnostics); got != 0 {
		for _, d := range second.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("second-run error-severity diagnostics = %d, want 0", got)
	}

	findSell := func(directives []ast.Directive) *ast.Transaction {
		for _, d := range directives {
			if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "sell" {
				return tx
			}
		}
		return nil
	}
	firstSell := findSell(first.Directives)
	if firstSell == nil {
		t.Fatalf("sell transaction not found after first booking pass")
	}
	secondSell := findSell(second.Directives)
	if secondSell == nil {
		t.Fatalf("sell transaction not found after second booking pass")
	}

	wantCS := &ast.CostSpec{
		Total: &ast.Amount{Number: dec("200.00"), Currency: "USD"},
		Date:  dayp(2024, 1, 1),
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(wantCS, firstSell.Postings[0].Cost, opts...); diff != "" {
		t.Errorf("booking.Apply() first-run CostSpec mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantCS, secondSell.Postings[0].Cost, opts...); diff != "" {
		t.Errorf("booking.Apply() second-run CostSpec mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(firstSell.Postings[0].Cost, secondSell.Postings[0].Cost, opts...); diff != "" {
		t.Errorf("booking.Apply() CostSpec drift across runs (-first +second):\n%s", diff)
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

// TestApply_DoesNotCloneCostlessTransaction pins the on-demand clone
// optimization: a Transaction with no CostSpec on any posting and no
// auto-posting cannot be mutated by the booking pass, so the plugin
// must thread the caller's pointer through unchanged rather than spend
// a deep clone on it.
func TestApply_DoesNotCloneCostlessTransaction(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Cash"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Expenses:Misc"}
	txn := &ast.Transaction{
		Date:      day(2025, 1, 1),
		Flag:      '*',
		Narration: "ordinary",
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: amt("-10", "USD")},
			{Account: "Expenses:Misc", Amount: amt("10", "USD")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openA, openB, txn})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	var got *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok {
			got = tx
			break
		}
	}
	if got == nil {
		t.Fatalf("booking.Apply() Directives: no transaction found, want one")
	}
	if got != txn {
		t.Errorf("booking.Apply() cloned a costless transaction; want input pointer threaded through")
	}
}

// TestApply_CashReductionExceedingInventoryIsAllowed pins the cash
// overdraft rule: when a reducing posting consumes more units than the
// account currently holds, but every matched candidate is cash (no
// CostSpec), the booking pass must NOT raise a
// reduction-exceeds-inventory diagnostic. Cash positions have no lot
// identity (currency units are fully fungible), so an overdraft is the
// balance assertion's concern, not booking's. This regression check
// covers a ledger like:
//
//	2025-01-02 *
//	  Assets:Cash 500 JPY
//	  Income:Misc
//	2025-01-03 *
//	  Assets:Cash -1000 JPY
//	  Expenses:Misc
//
// where pad would later synthesize the missing units, but pad runs
// after booking and therefore cannot help booking see them.
func TestApply_CashReductionExceedingInventoryIsAllowed(t *testing.T) {
	openCash := &ast.Open{Date: day(2025, 1, 1), Account: "Assets:Cash", Currencies: []string{"JPY"}}
	openInc := &ast.Open{Date: day(2025, 1, 1), Account: "Income:Misc"}
	openExp := &ast.Open{Date: day(2025, 1, 1), Account: "Expenses:Misc"}
	deposit := &ast.Transaction{
		Date:      day(2025, 1, 2),
		Flag:      '*',
		Narration: "deposit",
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: amt("500", "JPY")},
			{Account: "Income:Misc", Amount: amt("-500", "JPY")},
		},
	}
	withdraw := &ast.Transaction{
		Date:      day(2025, 1, 3),
		Flag:      '*',
		Narration: "withdraw",
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: amt("-1000", "JPY")},
			{Account: "Expenses:Misc", Amount: amt("1000", "JPY")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openCash, openInc, openExp, deposit, withdraw})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: unexpected error %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("booking.Apply error-severity diagnostics = %d, want 0", got)
	}
}

// TestApply_ClonesCostBearingTransaction pins the other branch of the
// on-demand clone predicate: a Transaction with a CostSpec on any
// posting can be mutated by the write-back step, so the plugin must
// emit a distinct clone rather than the caller's pointer.
func TestApply_ClonesCostBearingTransaction(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Brokerage"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:Cash"}
	txn := &ast.Transaction{
		Date: day(2024, 1, 15),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amt("10", "AAPL"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100.00", "USD"),
				},
			},
			{Account: "Assets:Cash", Amount: amt("-1000.00", "USD")},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openA, openB, txn})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	var got *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok {
			got = tx
			break
		}
	}
	if got == nil {
		t.Fatalf("booking.Apply() Directives: no transaction found, want one")
	}
	if got == txn {
		t.Errorf("booking.Apply() returned input pointer for cost-bearing transaction; want a distinct clone")
	}
}

// TestApply_DeferredAugmentationInterpolated covers the upstream
// "transfer with partial cost spec" pattern through the loader-level
// booking pass: an augmenting posting with a cost spec that lacks a
// concrete number (`{date, "label"}` or bare `{}`) is filled in by
// the reducer's interpolation pass, and writeAugmentationCost folds
// the resolved per-unit number back into the AST. The pre-existing
// Date and Label on the spec are preserved so downstream consumers
// (printer, BQL) still see the user's lot identity.
func TestApply_DeferredAugmentationInterpolated(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:A"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:B"}
	openEq := &ast.Open{Date: day(2024, 1, 1), Account: "Equity:Opening"}

	buyDate := day(2025, 1, 1)
	buy := &ast.Transaction{
		Date:      buyDate,
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:B",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Date:    &buyDate,
					Label:   "label",
				},
			},
			{Account: "Equity:Opening", Amount: amt("-1000", "JPY")},
		},
	}
	xfer := &ast.Transaction{
		Date:      day(2025, 2, 17),
		Flag:      '*',
		Narration: "xfer",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("5", "STOCK"),
				Cost: &ast.CostSpec{
					Date:  &buyDate,
					Label: "label",
				},
			},
			{
				Account: "Assets:B",
				Amount:  amt("-5", "STOCK"),
				Cost: &ast.CostSpec{
					Date:  &buyDate,
					Label: "label",
				},
			},
		},
	}

	in := api.Input{Directives: seqOf([]ast.Directive{openA, openB, openEq, buy, xfer})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}

	var bookedXfer *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "xfer" {
			bookedXfer = tx
			break
		}
	}
	if bookedXfer == nil {
		t.Fatalf("xfer transaction not found in booked directives")
	}
	// Augmenting posting (Assets:A): partial spec is filled with
	// PerUnit=100 JPY, Date and Label preserved.
	cs := bookedXfer.Postings[0].Cost
	wantCS := &ast.CostSpec{
		PerUnit: &ast.Amount{Number: dec("100"), Currency: "JPY"},
		Date:    dayp(2025, 1, 1),
		Label:   "label",
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(wantCS, cs, opts...); diff != "" {
		t.Errorf("Assets:A interpolated CostSpec mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_EmptyBracesAugmentationInterpolated is the bare `{}`
// variant: the user signals "lot-tracked, fill the cost from
// context" without providing a date or label, and the booking pass
// must still resolve a concrete per-unit number from the residual.
func TestApply_EmptyBracesAugmentationInterpolated(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:A"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:B"}
	openEq := &ast.Open{Date: day(2024, 1, 1), Account: "Equity:Opening"}

	buyDate := day(2025, 1, 1)
	buy := &ast.Transaction{
		Date:      buyDate,
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:B",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Date:    &buyDate,
				},
			},
			{Account: "Equity:Opening", Amount: amt("-1000", "JPY")},
		},
	}
	xfer := &ast.Transaction{
		Date:      day(2025, 2, 17),
		Flag:      '*',
		Narration: "xfer",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("5", "STOCK"),
				Cost:    &ast.CostSpec{}, // bare {}
			},
			{
				Account: "Assets:B",
				Amount:  amt("-5", "STOCK"),
				Cost: &ast.CostSpec{
					Date: &buyDate,
				},
			},
		},
	}

	in := api.Input{Directives: seqOf([]ast.Directive{openA, openB, openEq, buy, xfer})}
	res, err := booking.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("booking.Apply: %v", err)
	}
	if got := errorSeverityCount(res.Diagnostics); got != 0 {
		for _, d := range res.Diagnostics {
			t.Logf("diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("error-severity diagnostics = %d, want 0", got)
	}
	var bookedXfer *ast.Transaction
	for _, d := range res.Directives {
		if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "xfer" {
			bookedXfer = tx
			break
		}
	}
	if bookedXfer == nil {
		t.Fatalf("Apply: xfer not found")
	}
	cs := bookedXfer.Postings[0].Cost
	if cs == nil || cs.PerUnit == nil {
		t.Fatalf("Apply: Assets:A CostSpec.PerUnit is nil; want interpolated 100 JPY")
	}
	want := dec("100")
	if cs.PerUnit.Number.Cmp(&want) != 0 || cs.PerUnit.Currency != "JPY" {
		t.Errorf("Apply: Assets:A PerUnit = %+v, want 100 JPY", cs.PerUnit)
	}
	// writeAugmentationCost only fills PerUnit/Total; the user wrote
	// no Date and no Label on `{}` so the spec keeps both empty. The
	// txnDate fallback the resolver applied lives on the booked
	// inventory.Cost record, not on the AST.
	if cs.Date != nil {
		t.Errorf("Apply: Assets:A Cost.Date = %v, want nil (preserved from \"{}\" input)", cs.Date)
	}
	if cs.Label != "" {
		t.Errorf("Apply: Assets:A Cost.Label = %q, want \"\" (preserved from \"{}\" input)", cs.Label)
	}
}

// TestApply_IdempotentInterpolatedAugmentation pins idempotency for
// the new cost-interpolation path: re-running Apply over the
// already-interpolated AST must produce the same booked CostSpec
// without re-deferring or drifting the per-unit value. The first run
// fills PerUnit on the bare `{}` augmenting posting; the second run
// observes a concrete spec and books normally with no diagnostics
// and no further mutation.
func TestApply_IdempotentInterpolatedAugmentation(t *testing.T) {
	openA := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:A"}
	openB := &ast.Open{Date: day(2024, 1, 1), Account: "Assets:B"}
	openEq := &ast.Open{Date: day(2024, 1, 1), Account: "Equity:Opening"}

	buyDate := day(2025, 1, 1)
	buy := &ast.Transaction{
		Date:      buyDate,
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:B",
				Amount:  amt("10", "STOCK"),
				Cost: &ast.CostSpec{
					PerUnit: amt("100", "JPY"),
					Date:    &buyDate,
					Label:   "label",
				},
			},
			{Account: "Equity:Opening", Amount: amt("-1000", "JPY")},
		},
	}
	xfer := &ast.Transaction{
		Date:      day(2025, 2, 17),
		Flag:      '*',
		Narration: "xfer",
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  amt("5", "STOCK"),
				Cost: &ast.CostSpec{
					Date:  &buyDate,
					Label: "label",
				},
			},
			{
				Account: "Assets:B",
				Amount:  amt("-5", "STOCK"),
				Cost: &ast.CostSpec{
					Date:  &buyDate,
					Label: "label",
				},
			},
		},
	}

	directives := []ast.Directive{openA, openB, openEq, buy, xfer}

	first, err := booking.Apply(context.Background(), api.Input{Directives: seqOf(directives)})
	if err != nil {
		t.Fatalf("booking.Apply (first): %v", err)
	}
	if got := errorSeverityCount(first.Diagnostics); got != 0 {
		for _, d := range first.Diagnostics {
			t.Logf("first-run diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("first-run error-severity diagnostics = %d, want 0", got)
	}

	second, err := booking.Apply(context.Background(), api.Input{Directives: seqOf(first.Directives)})
	if err != nil {
		t.Fatalf("booking.Apply (second): %v", err)
	}
	if got := errorSeverityCount(second.Diagnostics); got != 0 {
		for _, d := range second.Diagnostics {
			t.Logf("second-run diagnostic: %s [%s]", d.Message, d.Code)
		}
		t.Fatalf("second-run error-severity diagnostics = %d, want 0", got)
	}

	findXfer := func(directives []ast.Directive) *ast.Transaction {
		for _, d := range directives {
			if tx, ok := d.(*ast.Transaction); ok && tx.Narration == "xfer" {
				return tx
			}
		}
		return nil
	}
	firstXfer := findXfer(first.Directives)
	secondXfer := findXfer(second.Directives)
	if firstXfer == nil || secondXfer == nil {
		t.Fatalf("xfer not found: first=%v second=%v", firstXfer, secondXfer)
	}
	opts := append(cmp.Options{
		cmpopts.IgnoreFields(ast.CostSpec{}, "Span"),
	}, astCmpOpts...)
	if diff := cmp.Diff(firstXfer.Postings[0].Cost, secondXfer.Postings[0].Cost, opts...); diff != "" {
		t.Errorf("Apply: interpolated CostSpec drifted across runs (-first +second):\n%s", diff)
	}
}
