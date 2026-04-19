package balance_test

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/balance"
)

// amtInt constructs an ast.Amount from a small int and currency code.
// The resulting decimal has Exponent == 0, so the inferred tolerance
// with the default multiplier of 0.5 is 0.5.
func amtInt(n int64, currency string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: currency}
}

// amtStr constructs an ast.Amount whose number is parsed from s. This
// preserves the decimal exponent, which matters for inferred tolerance.
func amtStr(t *testing.T, s, cur string) ast.Amount {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return ast.Amount{Number: *d, Currency: cur}
}

// seqOf adapts a slice of directives into an iter.Seq2[int, ast.Directive]
// compatible with api.Input.Directives without allocating a full ast.Ledger.
func seqOf(directives []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range directives {
			if !yield(i, d) {
				return
			}
		}
	}
}

func TestPlugin_EmptyLedger(t *testing.T) {
	res, err := balance.Plugin(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (plugin does not mutate the ledger)", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
}

// TestPlugin_BalanceMatches feeds one balancing transaction and a
// balance assertion that exactly matches the running total. No errors
// should be emitted.
func TestPlugin_BalanceMatches(t *testing.T) {
	pos := amtInt(100, "USD")
	neg := amtInt(-100, "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
}

// TestPlugin_BalanceMismatch feeds a balance assertion that differs
// from the running total by more than the inferred tolerance. Exactly
// one CodeBalanceMismatch must be emitted, carrying the established
// message wording.
func TestPlugin_BalanceMismatch(t *testing.T) {
	pos := amtStr(t, "100.00", "USD")
	neg := amtStr(t, "-100.00", "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	balSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 42, Column: 1}}
	bal := &ast.Balance{
		Span:    balSpan,
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtStr(t, "101.00", "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != string(validation.CodeBalanceMismatch) {
		t.Errorf("Code = %q, want %q", e.Code, string(validation.CodeBalanceMismatch))
	}
	if e.Span != balSpan {
		t.Errorf("Span = %#v, want %#v", e.Span, balSpan)
	}
	want := "balance assertion failed: account Assets:Cash: expected 101.00 USD, got 100.00 USD"
	if e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

// TestPlugin_BalanceWithinTolerance feeds a balance assertion that
// differs from the running total by less than the inferred tolerance.
// No errors should be emitted. The running balance is 100.004 and the
// assertion is 100.00 (tol=0.005); diff=0.004 is within tolerance.
func TestPlugin_BalanceWithinTolerance(t *testing.T) {
	pos := amtStr(t, "100.004", "USD")
	neg := amtStr(t, "-100.004", "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtStr(t, "100.00", "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
}

// TestPlugin_MultipleAccounts interleaves transactions that touch two
// accounts, each with a subsequent balance assertion. Both assertions
// must be checked and pass independently.
func TestPlugin_MultipleAccounts(t *testing.T) {
	pos1 := amtInt(100, "USD")
	neg1 := amtInt(-100, "USD")
	txn1 := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos1},
			{Account: "Income:Salary", Amount: &neg1},
		},
	}
	pos2 := amtInt(50, "USD")
	neg2 := amtInt(-50, "USD")
	txn2 := &ast.Transaction{
		Date: time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos2},
			{Account: "Income:Salary", Amount: &neg2},
		},
	}
	balCash := &ast.Balance{
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(150, "USD"),
	}
	balSalary := &ast.Balance{
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Income:Salary",
		Amount:  amtInt(-150, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn1, txn2, balCash, balSalary})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
}

// TestPlugin_BalanceOnUnopenedAccount_NoError confirms the plugin does
// NOT emit account-open errors (those are owned by the validations
// plugin). A zero-assertion on an account with no prior transactions
// passes; a non-zero assertion on the same account emits only a
// CodeBalanceMismatch.
func TestPlugin_BalanceOnUnopenedAccount_NoError(t *testing.T) {
	t.Run("zero assertion passes", func(t *testing.T) {
		bal := &ast.Balance{
			Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtInt(0, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{bal})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 0 {
			t.Errorf("Result.Errors = %v, want empty", res.Errors)
		}
	})

	t.Run("non-zero assertion yields only balance-mismatch", func(t *testing.T) {
		bal := &ast.Balance{
			Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtInt(100, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{bal})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 1 {
			t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
		}
		if got, want := res.Errors[0].Code, string(validation.CodeBalanceMismatch); got != want {
			t.Errorf("Code = %q, want %q (account-open diagnostics must NOT be emitted)", got, want)
		}
	})
}

// TestPlugin_ExplicitTolerance feeds a Balance directive with an
// explicit Tolerance field. The plugin must honor it rather than fall
// back to the inferred tolerance derived from the amount's exponent.
func TestPlugin_ExplicitTolerance(t *testing.T) {
	pos := amtStr(t, "100.0009", "USD")
	neg := amtStr(t, "-100.0009", "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	tol, _, err := apd.NewFromString("0.001")
	if err != nil {
		t.Fatalf("parse tolerance: %v", err)
	}
	bal := &ast.Balance{
		Date:      time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account:   "Assets:Cash",
		Amount:    amtStr(t, "100.00", "USD"),
		Tolerance: tol,
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty (diff 0.0009 within explicit tol 0.001)", res.Errors)
	}
}

// TestPlugin_CanceledContext asserts the plugin respects a canceled
// context and returns a non-nil error without running.
func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := balance.Plugin(ctx, api.Input{})
	if err == nil {
		t.Fatalf("Apply on canceled ctx returned nil error, want non-nil")
	}
}

// TestPlugin_AutoPostingInferredOnDifferentAccount mirrors upstream
// beancount's posting-weight application: a transaction with one
// explicit posting and one auto-posting (no Amount) infers the
// auto-posting's amount as the negation of the residual and applies
// it to the auto-posting's account. A subsequent Balance directive
// against the auto-posting's account must see the inferred amount,
// not zero.
func TestPlugin_AutoPostingInferredOnDifferentAccount(t *testing.T) {
	expl := amtInt(100, "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &expl},
			{Account: "Assets:Cash"}, // auto-posting
		},
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty (auto-posting should be inferred as -100 USD on Assets:Cash)", res.Errors)
	}
}

// TestPlugin_AutoPostingNoInferenceWhenMultiCurrency verifies that
// when a transaction has multiple currencies with non-zero residuals
// AND an auto-posting, the plugin does NOT attempt inference and does
// NOT touch the auto-posting's account running balance. The
// validations plugin owns CodeUnbalancedTransaction in that case.
func TestPlugin_AutoPostingNoInferenceWhenMultiCurrency(t *testing.T) {
	usd := amtInt(100, "USD")
	eur := amtInt(50, "EUR")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &usd},
			{Account: "Expenses:Travel", Amount: &eur},
			{Account: "Assets:Cash"}, // auto-posting; residual ambiguous
		},
	}
	// Assets:Cash must still read as zero in USD: no inference should
	// have been applied. A non-zero balance assertion therefore emits
	// exactly one CodeBalanceMismatch.
	balUSD := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	// A zero assertion on the same account must pass, since the
	// auto-posting's account has no running balance in USD.
	balZero := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(0, "EUR"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, balUSD, balZero})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1 (only balUSD should mismatch); errors = %v", len(res.Errors), res.Errors)
	}
	if got, want := res.Errors[0].Code, string(validation.CodeBalanceMismatch); got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
	// The running balance for (Assets:Cash, USD) must be 0 — the
	// auto-posting was NOT inferred despite the USD residual.
	wantMsg := "balance assertion failed: account Assets:Cash: expected -100 USD, got 0 USD"
	if res.Errors[0].Message != wantMsg {
		t.Errorf("Message = %q, want %q", res.Errors[0].Message, wantMsg)
	}
}

// TestPlugin_AutoPostingNoInferenceWhenMultipleAutos verifies that
// transactions with more than one auto-posting (malformed per the
// validations plugin) do NOT trigger inference in the balance plugin.
// A subsequent balance assertion against either auto-posting's
// account must read zero: posting-weight application is skipped once
// CodeMultipleAutoPostings has been flagged.
func TestPlugin_AutoPostingNoInferenceWhenMultipleAutos(t *testing.T) {
	expl := amtInt(100, "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &expl},
			{Account: "Assets:Cash"},    // auto #1
			{Account: "Assets:Savings"}, // auto #2
		},
	}
	// Neither auto-posting's account was updated, so a zero balance
	// assertion passes.
	balCashZero := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(0, "USD"),
	}
	balSavingsZero := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Savings",
		Amount:  amtInt(0, "USD"),
	}
	// And a non-zero assertion mismatches, confirming the running
	// balance stayed at zero.
	balCashNonZero := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, balCashZero, balSavingsZero, balCashNonZero})}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1 (only balCashNonZero should mismatch); errors = %v", len(res.Errors), res.Errors)
	}
	if got, want := res.Errors[0].Code, string(validation.CodeBalanceMismatch); got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
	wantMsg := "balance assertion failed: account Assets:Cash: expected -100 USD, got 0 USD"
	if res.Errors[0].Message != wantMsg {
		t.Errorf("Message = %q, want %q", res.Errors[0].Message, wantMsg)
	}
}

// TestPlugin_ToleranceMultiplierZero pins the contract that setting
// inferred_tolerance_multiplier to "0" disables inferred tolerance
// entirely: any non-zero residual must surface as a mismatch, even
// one that would be within tolerance under the default 0.5
// multiplier. The plugin computes inferred tolerance from the
// ASSERTION's amount exponent (tolerance.ForAmount called on
// bal.Amount in checkBalance), not the posting's. Here
// bal.Amount.Number is "100.00" so exp(bal.Amount.Number) = -2, so:
//   - default multiplier 0.5 -> tol = 0.5 * 10^-2 = 0.005
//   - multiplier 0           -> tol = 0   * 10^-2 = 0
//
// The running balance is 100.004 USD; diff = |100.00 - 100.004| =
// 0.004, which is within 0.005 (default) but strictly greater than
// 0 (multiplier=0), so exactly one CodeBalanceMismatch must fire.
func TestPlugin_ToleranceMultiplierZero(t *testing.T) {
	pos := amtStr(t, "100.004", "USD")
	neg := amtStr(t, "-100.004", "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	balSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 10, Column: 1}}
	bal := &ast.Balance{
		Span:    balSpan,
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtStr(t, "100.00", "USD"),
	}
	in := api.Input{
		Options:    map[string]string{"inferred_tolerance_multiplier": "0"},
		Directives: seqOf([]ast.Directive{txn, bal}),
	}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != string(validation.CodeBalanceMismatch) {
		t.Errorf("Code = %q, want %q", e.Code, string(validation.CodeBalanceMismatch))
	}
	if e.Span != balSpan {
		t.Errorf("Span = %#v, want %#v", e.Span, balSpan)
	}
	wantMsg := "balance assertion failed: account Assets:Cash: expected 100.00 USD, got 100.004 USD"
	if e.Message != wantMsg {
		t.Errorf("Message = %q, want %q", e.Message, wantMsg)
	}
}

// TestPlugin_ToleranceMultiplierRelaxed pins the contract that
// setting inferred_tolerance_multiplier > default relaxes the
// inferred tolerance. The plugin computes inferred tolerance from
// the ASSERTION's amount exponent (tolerance.ForAmount called on
// bal.Amount in checkBalance), not the posting's. Here
// bal.Amount.Number is "100.00" so exp(bal.Amount.Number) = -2, so:
//   - default multiplier 0.5 -> tol = 0.5 * 10^-2 = 0.005
//   - multiplier 2.0         -> tol = 2.0 * 10^-2 = 0.02
//
// The running balance is 100.009 USD; diff = |100.00 - 100.009| =
// 0.009, which is strictly greater than 0.005 (fails under the
// default) but strictly less than 0.02 (passes under multiplier=2.0).
// The "default_multiplier_rejects" sub-case is a negative control
// that proves the same inputs would fail without the option, so the
// relaxed sub-case actually exercises the option rather than being a
// trivial pass.
func TestPlugin_ToleranceMultiplierRelaxed(t *testing.T) {
	// Shared numeric inputs: the running balance is 100.009 USD and
	// the assertion is 100.00 USD, so diff = 0.009 at exp -2.
	t.Run("default_multiplier_rejects", func(t *testing.T) {
		pos := amtStr(t, "100.009", "USD")
		neg := amtStr(t, "-100.009", "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &pos},
				{Account: "Expenses:Food", Amount: &neg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtStr(t, "100.00", "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 1 {
			t.Fatalf("len(Result.Errors) = %d, want 1 (diff 0.009 exceeds default tol 0.005); errors = %v", len(res.Errors), res.Errors)
		}
		if got, want := res.Errors[0].Code, string(validation.CodeBalanceMismatch); got != want {
			t.Errorf("Code = %q, want %q", got, want)
		}
	})

	t.Run("relaxed_multiplier_accepts", func(t *testing.T) {
		pos := amtStr(t, "100.009", "USD")
		neg := amtStr(t, "-100.009", "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &pos},
				{Account: "Expenses:Food", Amount: &neg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtStr(t, "100.00", "USD"),
		}
		in := api.Input{
			Options:    map[string]string{"inferred_tolerance_multiplier": "2.0"},
			Directives: seqOf([]ast.Directive{txn, bal}),
		}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 0 {
			t.Errorf("Result.Errors = %v, want empty (diff 0.009 within relaxed tol 0.02)", res.Errors)
		}
	})
}

// TestPlugin_ExplicitToleranceZero confirms that an explicit
// Tolerance of zero on the Balance directive requires an exact
// match, regardless of what the inferred tolerance would have been.
// Two sub-ledgers exercise the two sides of the boundary.
func TestPlugin_ExplicitToleranceZero(t *testing.T) {
	t.Run("exact match passes with explicit zero tolerance", func(t *testing.T) {
		pos := amtStr(t, "100.00", "USD")
		neg := amtStr(t, "-100.00", "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &pos},
				{Account: "Expenses:Food", Amount: &neg},
			},
		}
		tol := new(apd.Decimal) // zero
		bal := &ast.Balance{
			Date:      time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account:   "Assets:Cash",
			Amount:    amtStr(t, "100.00", "USD"),
			Tolerance: tol,
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 0 {
			t.Errorf("Result.Errors = %v, want empty (exact match must pass with tol=0)", res.Errors)
		}
	})

	t.Run("tiny residual fails with explicit zero tolerance", func(t *testing.T) {
		pos := amtStr(t, "100.001", "USD")
		neg := amtStr(t, "-100.001", "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &pos},
				{Account: "Expenses:Food", Amount: &neg},
			},
		}
		tol := new(apd.Decimal) // zero
		balSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 77, Column: 1}}
		bal := &ast.Balance{
			Span:      balSpan,
			Date:      time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account:   "Assets:Cash",
			Amount:    amtStr(t, "100.000", "USD"),
			Tolerance: tol,
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 1 {
			t.Fatalf("len(Result.Errors) = %d, want 1 (tol=0 must reject any non-zero diff); errors = %v", len(res.Errors), res.Errors)
		}
		e := res.Errors[0]
		if e.Code != string(validation.CodeBalanceMismatch) {
			t.Errorf("Code = %q, want %q", e.Code, string(validation.CodeBalanceMismatch))
		}
		if e.Span != balSpan {
			t.Errorf("Span = %#v, want %#v", e.Span, balSpan)
		}
		wantMsg := "balance assertion failed: account Assets:Cash: expected 100.000 USD, got 100.001 USD"
		if e.Message != wantMsg {
			t.Errorf("Message = %q, want %q", e.Message, wantMsg)
		}
	})
}

// TestPlugin_MultiCurrencyIsolation confirms the per-(account,
// currency) bucketing of the running balance: depositing 100 USD
// and 50 EUR into the same account makes a 100 USD assertion pass
// without being "diluted" by the EUR balance, and a 999 EUR
// assertion on the same account fails independently. Crucially,
// the USD assertion is NOT swept against any aggregate that
// includes EUR, and the EUR mismatch does NOT mask the USD check.
//
// Each sub-test constructs its own *ast.Transaction values so no
// pointer state is shared across sub-tests.
func TestPlugin_MultiCurrencyIsolation(t *testing.T) {
	t.Run("USD assertion unaffected by EUR balance", func(t *testing.T) {
		usd := amtInt(100, "USD")
		usdNeg := amtInt(-100, "USD")
		txnUSD := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &usd},
				{Account: "Income:Salary", Amount: &usdNeg},
			},
		}
		eur := amtInt(50, "EUR")
		eurNeg := amtInt(-50, "EUR")
		txnEUR := &ast.Transaction{
			Date: time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &eur},
				{Account: "Income:Salary", Amount: &eurNeg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtInt(100, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txnUSD, txnEUR, bal})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 0 {
			t.Errorf("Result.Errors = %v, want empty (USD bucket must be isolated from EUR)", res.Errors)
		}
	})

	t.Run("EUR mismatch does not mask passing USD", func(t *testing.T) {
		usd := amtInt(100, "USD")
		usdNeg := amtInt(-100, "USD")
		txnUSD := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &usd},
				{Account: "Income:Salary", Amount: &usdNeg},
			},
		}
		eur := amtInt(50, "EUR")
		eurNeg := amtInt(-50, "EUR")
		txnEUR := &ast.Transaction{
			Date: time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &eur},
				{Account: "Income:Salary", Amount: &eurNeg},
			},
		}
		balUSD := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtInt(100, "USD"),
		}
		balSpanEUR := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 55, Column: 1}}
		balEUR := &ast.Balance{
			Span:    balSpanEUR,
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtInt(999, "EUR"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txnUSD, txnEUR, balUSD, balEUR})}
		res, err := balance.Plugin(context.Background(), in)
		if err != nil {
			t.Fatalf("Apply: unexpected error %v", err)
		}
		if len(res.Errors) != 1 {
			t.Fatalf("len(Result.Errors) = %d, want 1 (only EUR assertion should fail); errors = %v", len(res.Errors), res.Errors)
		}
		e := res.Errors[0]
		if e.Code != string(validation.CodeBalanceMismatch) {
			t.Errorf("Code = %q, want %q", e.Code, string(validation.CodeBalanceMismatch))
		}
		if e.Span != balSpanEUR {
			t.Errorf("Span = %#v, want %#v (the EUR assertion, not the USD one)", e.Span, balSpanEUR)
		}
		wantMsg := "balance assertion failed: account Assets:Cash: expected 999 EUR, got 50 EUR"
		if e.Message != wantMsg {
			t.Errorf("Message = %q, want %q", e.Message, wantMsg)
		}
	})
}

// TestPlugin_OptionsFromRawParseError confirms malformed options
// surface as api.Error{Code: "invalid-option"}, matching the
// validations plugin's contract.
func TestPlugin_OptionsFromRawParseError(t *testing.T) {
	in := api.Input{
		Options: map[string]string{
			"inferred_tolerance_multiplier": "not-a-decimal",
		},
	}
	res, err := balance.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "invalid-option" {
		t.Errorf("Code = %q, want %q", e.Code, "invalid-option")
	}
	if !strings.Contains(e.Message, "inferred_tolerance_multiplier") {
		t.Errorf("Message = %q, want it to mention the option key", e.Message)
	}
}
