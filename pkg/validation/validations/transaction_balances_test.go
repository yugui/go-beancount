package validations

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ast/asttest"
	"github.com/yugui/go-beancount/pkg/validation"
)

func mustDefaults() *ast.OptionValues { return ast.NewOptionValues() }

func mustOpts(t *testing.T, raw map[string]string) *ast.OptionValues {
	t.Helper()
	return asttest.MustOptions(t, raw)
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
	v := newTransactionBalances(mustDefaults())
	if got, want := v.Name(), "transaction_balances"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestTransactionBalances_FinishIsNoOp(t *testing.T) {
	v := newTransactionBalances(mustDefaults())
	if got := v.Finish(); got != nil {
		t.Errorf("Finish() = %v, want nil", got)
	}
}

func TestTransactionBalances_BalancedSingleCurrency(t *testing.T) {
	v := newTransactionBalances(mustDefaults())
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
	v := newTransactionBalances(mustDefaults())
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
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeUnbalancedTransaction),
		Span:     span,
		Message:  `transaction does not balance: non-zero residual -1 USD`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

// TestTransactionBalances_BookedAutoPosting pins the booked-AST happy
// path: when the booking pass has already filled in every posting's
// Amount, the validator sees a balanced transaction and emits no
// diagnostics. This is the path that runs in the full pipeline, where
// booking precedes validation.
func TestTransactionBalances_BookedAutoPosting(t *testing.T) {
	v := newTransactionBalances(mustDefaults())
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
	v := newTransactionBalances(mustDefaults())
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
	// Both nil-Amount postings are skipped from the balance sum (with a
	// CodeAutoPostingUnresolved diagnostic each), leaving the +100 USD on
	// Assets:Cash unbalanced. ProcessEntry then emits
	// CodeUnbalancedTransaction for the residual, so all three
	// diagnostics are asserted together to pin both behaviors precisely.
	want := []ast.Diagnostic{
		{
			Code:     string(validation.CodeAutoPostingUnresolved),
			Span:     txnSpan,
			Message:  `posting on account "Expenses:Food" has no amount; booking pass should have resolved it`,
			Severity: ast.Error,
		},
		{
			Code:     string(validation.CodeAutoPostingUnresolved),
			Span:     txnSpan,
			Message:  `posting on account "Expenses:Misc" has no amount; booking pass should have resolved it`,
			Severity: ast.Error,
		},
		{
			Code:     string(validation.CodeUnbalancedTransaction),
			Span:     txnSpan,
			Message:  `transaction does not balance: non-zero residual 100 USD`,
			Severity: ast.Error,
		},
	}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestTransactionBalances_MultiCurrencyPricedBalances(t *testing.T) {
	// 10 STOCK @ 100 USD => 1000 USD, offset by -1000 USD cash.
	v := newTransactionBalances(mustDefaults())
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
	v := newTransactionBalances(mustDefaults())
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
	txnSpan := txn.Span
	want := []ast.Diagnostic{
		{
			Code:     string(validation.CodeAutoPostingUnresolved),
			Span:     txnSpan,
			Message:  `posting on account "Expenses:Food" has no amount; booking pass should have resolved it`,
			Severity: ast.Error,
		},
		{
			Code:     string(validation.CodeUnbalancedTransaction),
			Span:     txnSpan,
			Message:  `transaction does not balance: non-zero residual 50 EUR, 100 USD`,
			Severity: ast.Error,
		},
	}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestTransactionBalances_IgnoresNonTransactionDirectives(t *testing.T) {
	v := newTransactionBalances(mustDefaults())
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
	build := func(withOption bool) (*ast.Transaction, *ast.OptionValues) {
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
		var opts *ast.OptionValues
		if withOption {
			opts = mustOpts(t, map[string]string{"inferred_tolerance_multiplier": "1"})
		} else {
			opts = mustDefaults()
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

// TestTransactionBalances_MixedPrecisionResidualWithinCoarseTolerance
// pins the upstream-compatible "looser tolerance wins" rule. The
// transaction mixes a coarse JPY posting (exp=-4) with a high-precision
// JPY posting (exp=-14); the residual sits at exp=-5, which lies above
// the inferred tolerance derived from the coarse posting (5e-5 JPY) but
// vastly above the one derived from the precise posting (5e-15 JPY).
// Upstream beancount uses mode="max" (looser tolerance) for balance
// checks, so the transaction balances; an earlier go-beancount revision
// used mode="min" and erroneously rejected it. Picking the cost form
// "{{ total CUR }}" keeps the cost-bearing posting's weight equal to
// the explicit total without introducing per-unit cost arithmetic.
func TestTransactionBalances_MixedPrecisionResidualWithinCoarseTolerance(t *testing.T) {
	v := newTransactionBalances(mustDefaults())
	stockUnits := amtStrDec(t, "-10", "STOCK")
	totalCost := amtStrDec(t, "100.1111", "JPY").Number
	cashHigh := amtStrDec(t, "200.22222222222222", "JPY")
	gain := amtStrDec(t, "-100.1111", "JPY")
	txn := &ast.Transaction{
		Date: day(2026, 5, 2),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{
				Account: "Assets:A",
				Amount:  &stockUnits,
				Cost:    &ast.CostSpec{Total: &totalCost, Currency: "JPY"},
			},
			{Account: "Assets:A", Amount: &cashHigh},
			{Account: "Income:Gain", Amount: &gain},
		},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry: got %v, want no errors (residual must fit within coarse-posting tolerance)", errs)
	}
}

// TestTransactionBalances_InferredToleranceDefault exercises the
// inferred_tolerance_default option via the full Infer path. The
// per-currency default only applies when a residual currency has no
// contributing amount posting — i.e. the residual arises from price
// conversion rather than from a literal amount in that currency.
//
// Shape: two STOCK legs annotated with slightly different USD prices.
// The weights are 2×0.502 USD = 1.004 USD and -2×0.500 USD = -1.000 USD,
// so the USD weight residual is 0.004 USD. STOCK is balanced (2−2=0).
// Because no posting has Amount.Currency=="USD", Infer finds no USD
// exponent in the posting scan and falls through to the per-currency
// default. With "USD:0.005" the 0.004 residual is within tolerance;
// without the option the fallback is zero and it fails.
func TestTransactionBalances_InferredToleranceDefault(t *testing.T) {
	// price-conversion residual: STOCK is balanced; USD residual is 0.004
	// from the difference in per-unit prices. No USD amount posting exists,
	// so posting-level tolerance inference produces no USD exponent.
	makeTxn := func(t *testing.T) *ast.Transaction {
		t.Helper()
		buyAmt := amtStrDec(t, "2", "STOCK")
		buyPrice := amtStrDec(t, "0.502", "USD")
		sellAmt := amtStrDec(t, "-2", "STOCK")
		sellPrice := amtStrDec(t, "0.500", "USD")
		return &ast.Transaction{
			Date: day(2024, 2, 1),
			Span: ast.Span{Start: ast.Position{Line: 1}},
			Postings: []ast.Posting{
				{Account: "Assets:Brokerage", Amount: &buyAmt, Price: &ast.PriceAnnotation{Amount: buyPrice}},
				{Account: "Assets:Brokerage", Amount: &sellAmt, Price: &ast.PriceAnnotation{Amount: sellPrice}},
			},
		}
	}

	t.Run("without option USD residual 0.004 fails (zero fallback)", func(t *testing.T) {
		txn := makeTxn(t)
		v := newTransactionBalances(mustDefaults())
		errs := v.ProcessEntry(txn)
		if len(errs) != 1 || errs[0].Code != string(validation.CodeUnbalancedTransaction) {
			t.Errorf("got errs = %v, want one CodeUnbalancedTransaction (USD residual 0.004 > zero tol)", errs)
		}
	})

	t.Run("with USD:0.005 default USD residual 0.004 passes", func(t *testing.T) {
		txn := makeTxn(t)
		opts := mustOpts(t, map[string]string{"inferred_tolerance_default": "USD:0.005"})
		v := newTransactionBalances(opts)
		if errs := v.ProcessEntry(txn); len(errs) != 0 {
			t.Errorf("got %v, want no errors (USD residual 0.004 within USD default tol 0.005)", errs)
		}
	})

	t.Run("with USD:0.003 default USD residual 0.004 still fails", func(t *testing.T) {
		txn := makeTxn(t)
		opts := mustOpts(t, map[string]string{"inferred_tolerance_default": "USD:0.003"})
		v := newTransactionBalances(opts)
		errs := v.ProcessEntry(txn)
		if len(errs) != 1 || errs[0].Code != string(validation.CodeUnbalancedTransaction) {
			t.Errorf("got errs = %v, want one CodeUnbalancedTransaction (USD residual 0.004 > USD default tol 0.003)", errs)
		}
	})

	t.Run("EUR default irrelevant: USD residual still fails", func(t *testing.T) {
		// Default is EUR:1.0 (large, but wrong currency). The USD residual
		// of 0.004 has no per-currency default entry, so the fallback is zero
		// and the transaction must still be rejected.
		txn := makeTxn(t)
		opts := mustOpts(t, map[string]string{"inferred_tolerance_default": "EUR:1.0"})
		v := newTransactionBalances(opts)
		errs := v.ProcessEntry(txn)
		if len(errs) != 1 || errs[0].Code != string(validation.CodeUnbalancedTransaction) {
			t.Errorf("got errs = %v, want one CodeUnbalancedTransaction (EUR default must not affect USD residual)", errs)
		}
	})
}

// TestTransactionBalances_InferToleranceFromCost verifies that with
// infer_tolerance_from_cost enabled, a per-unit cost spec broadens the
// cost-currency tolerance enough to absorb a residual that would
// otherwise be reported as unbalanced.
func TestTransactionBalances_InferToleranceFromCost(t *testing.T) {
	build := func(withOption bool) (*ast.Transaction, *ast.OptionValues) {
		units := amtDec(1000, "XYZ")
		costAmt := amtStrDec(t, "1.0001", "USD").Number
		cash := amtStrDec(t, "-1000.15", "USD")
		txn := &ast.Transaction{
			Date: day(2024, 2, 1),
			Span: ast.Span{Start: ast.Position{Line: 1}},
			Postings: []ast.Posting{
				{
					Account: "Assets:Inv",
					Amount:  &units,
					Cost:    &ast.CostSpec{PerUnit: &costAmt, Currency: "USD"},
				},
				{Account: "Assets:Cash", Amount: &cash},
			},
		}
		var opts *ast.OptionValues
		if withOption {
			opts = mustOpts(t, map[string]string{"infer_tolerance_from_cost": "TRUE"})
		} else {
			opts = mustDefaults()
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
