package validations

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
)

// mustDefaults returns a typed *options.Values with all defaults applied.
// Centralised here so every test avoids a duplicate FromRaw(nil) boilerplate.
func mustDefaults(t *testing.T) *options.Values {
	t.Helper()
	v, errs := options.FromRaw(nil)
	if len(errs) != 0 {
		t.Fatalf("options.FromRaw(nil): unexpected errors: %v", errs)
	}
	return v
}

// mustOpts returns a typed *options.Values built from raw key/value pairs,
// failing the test on any parse error.
func mustOpts(t *testing.T, raw map[string]string) *options.Values {
	t.Helper()
	v, errs := options.FromRaw(raw)
	if len(errs) != 0 {
		t.Fatalf("options.FromRaw(%v): unexpected errors: %v", raw, errs)
	}
	return v
}

// amtStrDec parses s into an apd-backed ast.Amount. Preserves exponent,
// which matters for tolerance inference.
func amtStrDec(t *testing.T, s, cur string) ast.Amount {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return ast.Amount{Number: *d, Currency: cur}
}

func day(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func TestTransactionBalances_Name(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	if got, want := v.Name(), "transaction_balances"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestTransactionBalances_FinishIsNoOp(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	if got := v.Finish(); got != nil {
		t.Errorf("Finish() = %v, want nil", got)
	}
}

func TestTransactionBalances_BalancedSingleCurrency(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	pos := amtDec(100, "USD")
	neg := amtDec(-100, "USD")
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry: got %v, want no errors", errs)
	}
}

func TestTransactionBalances_UnbalancedSingleCurrency(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	pos := amtDec(1, "USD")
	neg := amtDec(-2, "USD")
	span := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 12}}
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Span: span,
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	errs := v.ProcessEntry(txn)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	e := errs[0]
	if e.Code != string(validation.CodeUnbalancedTransaction) {
		t.Errorf("Code = %q, want %q", e.Code, validation.CodeUnbalancedTransaction)
	}
	if e.Span != span {
		t.Errorf("Span = %#v, want %#v", e.Span, span)
	}
	// Legacy wording: "transaction does not balance: non-zero residual in [USD]".
	if want := `transaction does not balance: non-zero residual in [USD]`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

// TestTransactionBalances_BookedAutoPosting pins the booked-AST happy
// path: when the booking pass has already filled in every posting's
// Amount, the validator sees a balanced transaction and emits no
// diagnostics. This is the path that runs in the full pipeline, where
// booking precedes validation.
func TestTransactionBalances_BookedAutoPosting(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	pos := amtDec(100, "USD")
	neg := amtDec(-100, "USD")
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg}, // booked auto
		},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry: got %v, want no errors", errs)
	}
}

// TestTransactionBalances_AutoPostingNotBookedReports pins the
// defensive path: a posting reaching the validator with a nil Amount
// signals that the booking pass was skipped (or regressed). The
// validator must emit one CodeAutoPostingUnresolved per such posting
// rather than attempting to infer the missing amount itself.
func TestTransactionBalances_AutoPostingNotBookedReports(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	pos := amtDec(100, "USD")
	txnSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 7}}
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Span: txnSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food"},
			{Account: "Expenses:Misc"},
		},
	}
	errs := v.ProcessEntry(txn)
	// Both nil-Amount postings are skipped from the balance sum (with a
	// CodeAutoPostingUnresolved diagnostic each), leaving the +100 USD on
	// Assets:Cash unbalanced. ProcessEntry then emits
	// CodeUnbalancedTransaction for the residual, so all three
	// diagnostics are asserted together to pin both behaviors precisely.
	wantCodes := []string{
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeUnbalancedTransaction),
	}
	if len(errs) != len(wantCodes) {
		t.Fatalf("ProcessEntry() got %d diagnostics, want %d; errs = %v", len(errs), len(wantCodes), errs)
	}
	for i, want := range wantCodes {
		if errs[i].Code != want {
			t.Errorf("errs[%d].Code = %q, want %q", i, errs[i].Code, want)
		}
	}
	for i := range errs {
		if errs[i].Span != txnSpan {
			t.Errorf("ProcessEntry() errs[%d].Span = %#v, want %#v (txn.Span)", i, errs[i].Span, txnSpan)
		}
	}
}

func TestTransactionBalances_MultiCurrencyPricedBalances(t *testing.T) {
	// 10 STOCK @ 100 USD => 1000 USD, offset by -1000 USD cash.
	v := newTransactionBalances(mustDefaults(t))
	units := amtDec(10, "STOCK")
	price := amtDec(100, "USD")
	cash := amtDec(-1000, "USD")
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{
				Account: "Assets:Stocks",
				Amount:  &units,
				Price:   &ast.PriceAnnotation{Amount: price, IsTotal: false},
			},
			{Account: "Assets:Cash", Amount: &cash},
		},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry: got %v, want no errors", errs)
	}
}

func TestTransactionBalances_MultiCurrencyAutoPostingIsUnbalanced(t *testing.T) {
	// A nil-Amount posting in the validator means booking was skipped;
	// the validator emits CodeAutoPostingUnresolved for the offending
	// posting and additionally reports the multi-currency residual as
	// CodeUnbalancedTransaction. The legacy "cannot infer auto-posting"
	// path is gone because validators no longer infer.
	v := newTransactionBalances(mustDefaults(t))
	usd := amtDec(100, "USD")
	eur := amtDec(50, "EUR")
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Assets:EurCash", Amount: &eur},
			{Account: "Expenses:Food"},
		},
	}
	errs := v.ProcessEntry(txn)
	wantCodes := []string{
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeUnbalancedTransaction),
	}
	if len(errs) != len(wantCodes) {
		t.Fatalf("ProcessEntry() got %d diagnostics, want %d; errs = %v", len(errs), len(wantCodes), errs)
	}
	for i, want := range wantCodes {
		if errs[i].Code != want {
			t.Errorf("errs[%d].Code = %q, want %q", i, errs[i].Code, want)
		}
	}
	if want := `transaction does not balance: non-zero residual in [EUR USD]`; errs[1].Message != want {
		t.Errorf("errs[1].Message = %q, want %q", errs[1].Message, want)
	}
}

func TestTransactionBalances_IgnoresNonTransactionDirectives(t *testing.T) {
	v := newTransactionBalances(mustDefaults(t))
	for _, d := range []ast.Directive{
		&ast.Balance{Date: day(2024, 1, 1), Account: "Assets:Cash", Amount: amtDec(0, "USD")},
		&ast.Open{Date: day(2024, 1, 1), Account: "Assets:Cash"},
		&ast.Note{Date: day(2024, 1, 1), Account: "Assets:Cash"},
		&ast.Price{Date: day(2024, 1, 1), Commodity: "USD", Amount: amtDec(1, "EUR")},
	} {
		if errs := v.ProcessEntry(d); len(errs) != 0 {
			t.Errorf("ProcessEntry(%T) = %v, want no errors", d, errs)
		}
	}
}

// TestTransactionBalances_MultiplierAffectsResidualTolerance verifies
// that a residual of 0.01 USD fails at multiplier 0.5 (default) and
// passes at multiplier 1. This exercises the tolerance.Infer
// integration against v.opts.
func TestTransactionBalances_MultiplierAffectsResidualTolerance(t *testing.T) {
	build := func(withOption bool) (*ast.Transaction, *options.Values) {
		pos := amtStrDec(t, "100.00", "USD")
		neg := amtStrDec(t, "-99.99", "USD")
		txn := &ast.Transaction{
			Date: day(2024, 2, 1),
			Span: ast.Span{Start: ast.Position{Line: 1}},
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &pos},
				{Account: "Expenses:Food", Amount: &neg},
			},
		}
		var opts *options.Values
		if withOption {
			opts = mustOpts(t, map[string]string{"inferred_tolerance_multiplier": "1"})
		} else {
			opts = mustDefaults(t)
		}
		return txn, opts
	}

	// Default multiplier 0.5 → USD tolerance 0.005 → 0.01 residual fails.
	txn, opts := build(false)
	v := newTransactionBalances(opts)
	errs := v.ProcessEntry(txn)
	if len(errs) != 1 {
		t.Fatalf("default multiplier: got %d errors, want 1; errs = %v", len(errs), errs)
	}
	if errs[0].Code != string(validation.CodeUnbalancedTransaction) {
		t.Errorf("default multiplier: Code = %q, want %q", errs[0].Code, validation.CodeUnbalancedTransaction)
	}

	// Multiplier 1 → USD tolerance 0.01 → 0.01 residual within tolerance.
	txn2, opts2 := build(true)
	v2 := newTransactionBalances(opts2)
	if errs := v2.ProcessEntry(txn2); len(errs) != 0 {
		t.Errorf("multiplier=1: got %v, want no errors", errs)
	}
}

// TestTransactionBalances_InferToleranceFromCost verifies that with
// infer_tolerance_from_cost enabled, a per-unit cost spec broadens the
// cost-currency tolerance enough to absorb a residual that would
// otherwise be reported as unbalanced.
func TestTransactionBalances_InferToleranceFromCost(t *testing.T) {
	build := func(withOption bool) (*ast.Transaction, *options.Values) {
		units := amtDec(1000, "XYZ")
		costAmt := amtStrDec(t, "1.0001", "USD")
		cash := amtStrDec(t, "-1000.15", "USD")
		txn := &ast.Transaction{
			Date: day(2024, 2, 1),
			Span: ast.Span{Start: ast.Position{Line: 1}},
			Postings: []ast.Posting{
				{
					Account: "Assets:Inv",
					Amount:  &units,
					Cost:    &ast.CostSpec{PerUnit: &costAmt},
				},
				{Account: "Assets:Cash", Amount: &cash},
			},
		}
		var opts *options.Values
		if withOption {
			opts = mustOpts(t, map[string]string{"infer_tolerance_from_cost": "TRUE"})
		} else {
			opts = mustDefaults(t)
		}
		return txn, opts
	}

	// Disabled → |-0.05| > 0.005 → unbalanced.
	txn, opts := build(false)
	v := newTransactionBalances(opts)
	errs := v.ProcessEntry(txn)
	if len(errs) != 1 || errs[0].Code != string(validation.CodeUnbalancedTransaction) {
		t.Errorf("disabled: got errs = %v, want one CodeUnbalancedTransaction", errs)
	}

	// Enabled → max(0.005, 0.05) = 0.05, |-0.05| == 0.05 → balanced.
	txn2, opts2 := build(true)
	v2 := newTransactionBalances(opts2)
	if errs := v2.ProcessEntry(txn2); len(errs) != 0 {
		t.Errorf("enabled: got %v, want no errors", errs)
	}
}
