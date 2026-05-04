package balance_test

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
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
	res, err := balance.Apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (plugin does not mutate the ledger)", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
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
// assertion is 100.00; under the doubled-factor balance-assertion
// tolerance tol = 0.5 * 2 * 10^-2 = 0.01, so diff=0.004 is within
// tolerance.
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
		}
	})

	t.Run("non-zero assertion yields only balance-mismatch", func(t *testing.T) {
		bal := &ast.Balance{
			Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Cash",
			Amount:  amtInt(100, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 1 {
			t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
		}
		if got, want := res.Diagnostics[0].Code, string(validation.CodeBalanceMismatch); got != want {
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty (diff 0.0009 within explicit tol 0.001)", res.Diagnostics)
	}
}

// TestPlugin_CanceledContext asserts the plugin respects a canceled
// context and returns a non-nil error without running.
func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := balance.Apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("balance.Apply on canceled ctx returned nil error, want non-nil")
	}
}

// TestPlugin_AutoPostingNotBookedReports pins the defensive path:
// when raw AST slips through, the validator emits
// CodeAutoPostingUnresolved rather than silently inferring. The
// nil-Amount posting is skipped from the running balance, so a
// subsequent balance assertion against that account sees zero rather
// than an inferred amount, and a non-zero assertion therefore reports
// CodeBalanceMismatch in addition to the per-posting unresolved
// diagnostic.
func TestPlugin_AutoPostingNotBookedReports(t *testing.T) {
	expl := amtInt(100, "USD")
	txnSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 5, Column: 1}}
	txn := &ast.Transaction{
		Span: txnSpan,
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &expl},
			{Account: "Assets:Cash"}, // auto-posting; raw AST slipped past booking
		},
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	wantCodes := []string{
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeBalanceMismatch),
	}
	if len(res.Diagnostics) != len(wantCodes) {
		t.Fatalf("len(Result.Diagnostics) = %d, want %d; diagnostics = %v", len(res.Diagnostics), len(wantCodes), res.Diagnostics)
	}
	for i, want := range wantCodes {
		if res.Diagnostics[i].Code != want {
			t.Errorf("balance.Apply: Diagnostics[%d].Code = %q, want %q", i, res.Diagnostics[i].Code, want)
		}
	}
	// The unresolved diagnostic falls back to the transaction Span when
	// the posting Span is zero.
	if got := res.Diagnostics[0].Span; got != txnSpan {
		t.Errorf("balance.Apply: Diagnostics[0].Span = %#v, want txn.Span %#v", got, txnSpan)
	}
	// Inventory was NOT updated for the nil-Amount posting, so the
	// balance assertion against the auto-posting's account must see
	// zero rather than the negated residual.
	wantMsg := "balance assertion failed: account Assets:Cash: expected -100 USD, got 0 USD"
	if got := res.Diagnostics[1].Message; got != wantMsg {
		t.Errorf("balance.Apply: Diagnostics[1].Message = %q, want %q", got, wantMsg)
	}
}

// TestPlugin_AutoPostingMultiCurrencyReports pins the defensive path:
// when raw AST slips through, the validator emits
// CodeAutoPostingUnresolved rather than silently inferring. With
// multiple currencies among explicit postings the previous
// implementation declined to infer; under the booked-AST contract the
// same input now produces an explicit diagnostic and the nil-Amount
// account stays at zero.
func TestPlugin_AutoPostingMultiCurrencyReports(t *testing.T) {
	usd := amtInt(100, "USD")
	eur := amtInt(50, "EUR")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &usd},
			{Account: "Expenses:Travel", Amount: &eur},
			{Account: "Assets:Cash"}, // auto-posting
		},
	}
	// Assets:Cash must still read as zero in USD: no inference should
	// have been applied. A non-zero balance assertion therefore emits
	// exactly one CodeBalanceMismatch alongside the unresolved
	// diagnostic.
	balUSD := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	// A zero assertion on the same account must pass, since the
	// auto-posting's account has no running balance in EUR either.
	balZero := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(0, "EUR"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, balUSD, balZero})}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	wantCodes := []string{
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeBalanceMismatch),
	}
	if len(res.Diagnostics) != len(wantCodes) {
		t.Fatalf("len(Result.Diagnostics) = %d, want %d; diagnostics = %v", len(res.Diagnostics), len(wantCodes), res.Diagnostics)
	}
	for i, want := range wantCodes {
		if res.Diagnostics[i].Code != want {
			t.Errorf("balance.Apply: Diagnostics[%d].Code = %q, want %q", i, res.Diagnostics[i].Code, want)
		}
	}
	// The running balance for (Assets:Cash, USD) must be 0 — the
	// auto-posting was NOT inferred.
	wantMsg := "balance assertion failed: account Assets:Cash: expected -100 USD, got 0 USD"
	if res.Diagnostics[1].Message != wantMsg {
		t.Errorf("balance.Apply: Diagnostics[1].Message = %q, want %q", res.Diagnostics[1].Message, wantMsg)
	}
}

// TestPlugin_MultipleAutoPostingsReport pins the defensive path:
// when raw AST slips through, the validator emits
// CodeAutoPostingUnresolved rather than silently inferring. With more
// than one nil-Amount posting the validator reports each one
// independently and leaves both accounts' running balances at zero.
func TestPlugin_MultipleAutoPostingsReport(t *testing.T) {
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	wantCodes := []string{
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeAutoPostingUnresolved),
		string(validation.CodeBalanceMismatch),
	}
	if len(res.Diagnostics) != len(wantCodes) {
		t.Fatalf("len(Result.Diagnostics) = %d, want %d; diagnostics = %v", len(res.Diagnostics), len(wantCodes), res.Diagnostics)
	}
	for i, want := range wantCodes {
		if res.Diagnostics[i].Code != want {
			t.Errorf("balance.Apply: Diagnostics[%d].Code = %q, want %q", i, res.Diagnostics[i].Code, want)
		}
	}
	wantMsg := "balance assertion failed: account Assets:Cash: expected -100 USD, got 0 USD"
	if res.Diagnostics[2].Message != wantMsg {
		t.Errorf("balance.Apply: Diagnostics[2].Message = %q, want %q", res.Diagnostics[2].Message, wantMsg)
	}
}

// TestPlugin_BookedTransactionUpdatesInventory exercises the
// booked-AST happy path: a single-currency transaction whose postings
// all carry an explicit Amount must update the running balance so a
// downstream assertion against the offset account passes without
// emitting any diagnostics. This is the case the deleted inference
// path used to handle implicitly; under the booking invariant the
// fixture is constructed in already-booked shape.
func TestPlugin_BookedTransactionUpdatesInventory(t *testing.T) {
	expl := amtInt(100, "USD")
	booked := amtInt(-100, "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &expl},
			{Account: "Assets:Cash", Amount: &booked},
		},
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty (booked transaction must populate Assets:Cash)", res.Diagnostics)
	}
}

// TestPlugin_BookedMultiCurrencyTransaction exercises the booked-AST
// happy path: a multi-currency transaction with explicit Amounts on
// every leg must update each per-(account, currency) bucket
// independently, so subsequent balance assertions on the offset
// account succeed in both currencies.
func TestPlugin_BookedMultiCurrencyTransaction(t *testing.T) {
	usd := amtInt(100, "USD")
	eur := amtInt(50, "EUR")
	usdOff := amtInt(-100, "USD")
	eurOff := amtInt(-50, "EUR")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &usd},
			{Account: "Expenses:Travel", Amount: &eur},
			{Account: "Assets:Cash", Amount: &usdOff},
			{Account: "Assets:Cash", Amount: &eurOff},
		},
	}
	balUSD := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-100, "USD"),
	}
	balEUR := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-50, "EUR"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, balUSD, balEUR})}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty (per-currency buckets must each be populated)", res.Diagnostics)
	}
}

// TestPlugin_BookedMultiplePostings exercises the booked-AST happy
// path: a transaction whose offset is split across multiple postings
// (e.g., separate cash and savings legs) updates each account's
// running balance independently when every Amount is explicit.
func TestPlugin_BookedMultiplePostings(t *testing.T) {
	expl := amtInt(100, "USD")
	cashLeg := amtInt(-60, "USD")
	savingsLeg := amtInt(-40, "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: &expl},
			{Account: "Assets:Cash", Amount: &cashLeg},
			{Account: "Assets:Savings", Amount: &savingsLeg},
		},
	}
	balCash := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amtInt(-60, "USD"),
	}
	balSavings := &ast.Balance{
		Date:    time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Savings",
		Amount:  amtInt(-40, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, balCash, balSavings})}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty (each posting's account bucket must be updated)", res.Diagnostics)
	}
}

// TestPlugin_ToleranceMultiplierZero pins the contract that setting
// inferred_tolerance_multiplier to "0" disables inferred tolerance
// entirely: any non-zero residual must surface as a mismatch, even
// one that would be within tolerance under the default 0.5
// multiplier. The plugin computes inferred tolerance from the
// ASSERTION's amount exponent (tolerance.ForBalanceAssertion called
// on bal.Amount in checkBalance), not the posting's, and applies
// the doubled factor for balance assertions. Here bal.Amount.Number
// is "100.00" so exp(bal.Amount.Number) = -2, so:
//   - default multiplier 0.5 -> tol = 0.5 * 2 * 10^-2 = 0.01
//   - multiplier 0           -> tol = 0   * 2 * 10^-2 = 0
//
// The running balance is 100.004 USD; diff = |100.00 - 100.004| =
// 0.004, which is within 0.01 (default) but strictly greater than
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
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
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
// the ASSERTION's amount exponent (tolerance.ForBalanceAssertion
// called on bal.Amount in checkBalance), not the posting's, and
// applies the doubled factor for balance assertions. Here
// bal.Amount.Number is "100.00" so exp(bal.Amount.Number) = -2, so:
//   - default multiplier 0.5 -> tol = 0.5 * 2 * 10^-2 = 0.01
//   - multiplier 2.0         -> tol = 2.0 * 2 * 10^-2 = 0.04
//
// The running balance is 100.015 USD; diff = |100.00 - 100.015| =
// 0.015, which is strictly greater than 0.01 (fails under the
// default) but strictly less than 0.04 (passes under multiplier=2.0).
// The "default_multiplier_rejects" sub-case is a negative control
// that proves the same inputs would fail without the option, so the
// relaxed sub-case actually exercises the option rather than being a
// trivial pass.
func TestPlugin_ToleranceMultiplierRelaxed(t *testing.T) {
	// Shared numeric inputs: the running balance is 100.015 USD and
	// the assertion is 100.00 USD, so diff = 0.015 at exp -2.
	t.Run("default_multiplier_rejects", func(t *testing.T) {
		pos := amtStr(t, "100.015", "USD")
		neg := amtStr(t, "-100.015", "USD")
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 1 {
			t.Fatalf("len(Result.Diagnostics) = %d, want 1 (diff 0.015 exceeds default tol 0.01); diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
		}
		if got, want := res.Diagnostics[0].Code, string(validation.CodeBalanceMismatch); got != want {
			t.Errorf("Code = %q, want %q", got, want)
		}
	})

	t.Run("relaxed_multiplier_accepts", func(t *testing.T) {
		pos := amtStr(t, "100.015", "USD")
		neg := amtStr(t, "-100.015", "USD")
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (diff 0.015 within relaxed tol 0.04)", res.Diagnostics)
		}
	})
}

// TestPlugin_BalanceAssertionDoubledFactor exercises upstream
// beancount's get_balance_tolerance: balance assertions use a doubled
// inferred-tolerance factor (2 * inferred_tolerance_multiplier *
// 10^expo) because users hand-write the asserted amount and rounding
// noise can exceed transaction-internal precision. With the default
// multiplier 0.5 and an assertion of "-17.775" (exp -3), the doubled
// tolerance is 0.001. A running balance differing by 0.0008 (within
// 0.001) must pass, while a running balance differing by 0.0020
// (above 0.001) must reject.
func TestPlugin_BalanceAssertionDoubledFactor(t *testing.T) {
	t.Run("accepts_under_doubled_factor", func(t *testing.T) {
		pos := amtStr(t, "-17.7758", "LONGCCY")
		neg := amtStr(t, "17.7758", "LONGCCY")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Position", Amount: &pos},
				{Account: "Equity:Opening", Amount: &neg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Position",
			Amount:  amtStr(t, "-17.775", "LONGCCY"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (diff 0.0008 within doubled tol 0.001)", res.Diagnostics)
		}
	})

	t.Run("rejects_when_diff_exceeds_doubled_factor", func(t *testing.T) {
		pos := amtStr(t, "-17.7770", "LONGCCY")
		neg := amtStr(t, "17.7770", "LONGCCY")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Position", Amount: &pos},
				{Account: "Equity:Opening", Amount: &neg},
			},
		}
		balSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 99, Column: 1}}
		bal := &ast.Balance{
			Span:    balSpan,
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Position",
			Amount:  amtStr(t, "-17.775", "LONGCCY"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 1 {
			t.Fatalf("len(Result.Diagnostics) = %d, want 1 (diff 0.0020 exceeds doubled tol 0.001); diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
		}
		e := res.Diagnostics[0]
		if e.Code != string(validation.CodeBalanceMismatch) {
			t.Errorf("Code = %q, want %q", e.Code, string(validation.CodeBalanceMismatch))
		}
		if e.Span != balSpan {
			t.Errorf("Span = %#v, want %#v", e.Span, balSpan)
		}
		wantMsg := "balance assertion failed: account Assets:Position: expected -17.775 LONGCCY, got -17.7770 LONGCCY"
		if e.Message != wantMsg {
			t.Errorf("Message = %q, want %q", e.Message, wantMsg)
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (exact match must pass with tol=0)", res.Diagnostics)
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 1 {
			t.Fatalf("len(Result.Diagnostics) = %d, want 1 (tol=0 must reject any non-zero diff); diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
		}
		e := res.Diagnostics[0]
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (USD bucket must be isolated from EUR)", res.Diagnostics)
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
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 1 {
			t.Fatalf("len(Result.Diagnostics) = %d, want 1 (only EUR assertion should fail); diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
		}
		e := res.Diagnostics[0]
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

// TestPlugin_SubtreeAggregation pins the upstream-compatible behavior
// that a balance assertion on a parent account aggregates over the
// entire subtree, not just postings to the parent itself. This mirrors
// upstream beancount's realization.compute_balance(real_account,
// leaf_only=False) semantics.
func TestPlugin_SubtreeAggregation(t *testing.T) {
	t.Run("parent assertion sums over leaf children", func(t *testing.T) {
		amtB := amtInt(10, "USD")
		amtC := amtInt(20, "USD")
		neg := amtInt(-30, "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:A:B", Amount: &amtB},
				{Account: "Assets:A:C", Amount: &amtC},
				{Account: "Income:Salary", Amount: &neg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A",
			Amount:  amtInt(30, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (parent must aggregate subtree)", res.Diagnostics)
		}
	})

	t.Run("user-reported case: cost-basis posting to grandchild", func(t *testing.T) {
		jpyNeg := amtInt(-9595, "JPY")
		nac := amtStr(t, "10.1", "NAC")
		jpyPos := amtInt(9595, "JPY")
		nacNeg := amtStr(t, "-10.1", "NAC")
		txn := &ast.Transaction{
			Date: time.Date(2025, 6, 17, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:A:B", Amount: &jpyNeg},
				{Account: "Assets:A:C", Amount: &nac},
				{Account: "Equity:Trading:NAC-JPY", Amount: &jpyPos},
				{Account: "Equity:Trading:NAC-JPY", Amount: &nacNeg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A",
			Amount:  amtStr(t, "10.1", "NAC"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (Assets:A subtree holds 10.1 NAC)", res.Diagnostics)
		}
	})

	t.Run("prefix-trap: sibling with shared string prefix excluded", func(t *testing.T) {
		// Posting 99 USD to Assets:Apple must NOT contribute to the
		// Assets:A subtree. The structural proof is that a 0 USD
		// assertion on Assets:A passes (subtree empty), independent
		// of any diagnostic message wording.
		apple := amtInt(99, "USD")
		neg := amtInt(-99, "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Apple", Amount: &apple},
				{Account: "Income:Salary", Amount: &neg},
			},
		}
		balZero := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A",
			Amount:  amtInt(0, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, balZero})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (Assets:Apple must not be aggregated under Assets:A)", res.Diagnostics)
		}
	})

	t.Run("parent assertion sums own postings together with children", func(t *testing.T) {
		// Postings to both Assets:A itself AND a child Assets:A:B.
		// The aggregation must include the asserted account's own
		// bucket (Covers identity branch) together with the child.
		own := amtInt(7, "USD")
		child := amtInt(3, "USD")
		neg := amtInt(-10, "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:A", Amount: &own},
				{Account: "Assets:A:B", Amount: &child},
				{Account: "Income:Salary", Amount: &neg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A",
			Amount:  amtInt(10, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (parent + child must both be aggregated)", res.Diagnostics)
		}
	})

	t.Run("multi-currency subtree: only matching currency aggregated", func(t *testing.T) {
		// Two children of Assets:A hold different currencies. A
		// USD assertion on Assets:A must sum only the USD child;
		// the EUR child must not be swept into the USD bucket.
		usd := amtInt(100, "USD")
		usdNeg := amtInt(-100, "USD")
		eur := amtInt(50, "EUR")
		eurNeg := amtInt(-50, "EUR")
		txnUSD := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:A:B", Amount: &usd},
				{Account: "Income:Salary", Amount: &usdNeg},
			},
		}
		txnEUR := &ast.Transaction{
			Date: time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:A:C", Amount: &eur},
				{Account: "Income:Salary", Amount: &eurNeg},
			},
		}
		balUSD := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A",
			Amount:  amtInt(100, "USD"),
		}
		balEUR := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A",
			Amount:  amtInt(50, "EUR"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txnUSD, txnEUR, balUSD, balEUR})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (per-currency subtree sums must be isolated)", res.Diagnostics)
		}
	})

	t.Run("leaf assertion still passes when parent has no own postings", func(t *testing.T) {
		pos := amtInt(50, "USD")
		neg := amtInt(-50, "USD")
		txn := &ast.Transaction{
			Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:A:B", Amount: &pos},
				{Account: "Income:Salary", Amount: &neg},
			},
		}
		bal := &ast.Balance{
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:A:B",
			Amount:  amtInt(50, "USD"),
		}
		in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
		res, err := balance.Apply(context.Background(), in)
		if err != nil {
			t.Fatalf("balance.Apply: unexpected error %v", err)
		}
		if len(res.Diagnostics) != 0 {
			t.Errorf("Result.Diagnostics = %v, want empty (leaf assertion sees only its own bucket)", res.Diagnostics)
		}
	})
}

// TestPlugin_OptionsFromRawParseError confirms malformed options
// surface as ast.Diagnostic{Code: "invalid-option"}, matching the
// validations plugin's contract.
func TestPlugin_OptionsFromRawParseError(t *testing.T) {
	in := api.Input{
		Options: map[string]string{
			"inferred_tolerance_multiplier": "not-a-decimal",
		},
	}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
	if e.Code != "invalid-option" {
		t.Errorf("Code = %q, want %q", e.Code, "invalid-option")
	}
	if !strings.Contains(e.Message, "inferred_tolerance_multiplier") {
		t.Errorf("Message = %q, want it to mention the option key", e.Message)
	}
}
