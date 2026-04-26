package validations_test

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/validations"
)

// amtInt constructs an ast.Amount from a small int and currency code.
func amtInt(n int64, currency string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: currency}
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
	res, err := validations.Apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (plugin does not mutate the ledger)", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
}

func TestPlugin_NoValidatorsNoErrors(t *testing.T) {
	open := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{open}),
	}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty (no validators registered)", res.Errors)
	}
}

func TestPlugin_DuplicateOpen(t *testing.T) {
	// Two Open directives for the same account; the second must surface
	// as a single duplicate-open diagnostic via the openClose validator.
	d1 := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 1, Offset: 0}},
	}
	d2 := &ast.Open{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 2, Offset: 40}},
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{d1, d2}),
	}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "duplicate-open" {
		t.Errorf("Code = %q, want %q", e.Code, "duplicate-open")
	}
	if e.Span != d2.Span {
		t.Errorf("Span = %#v, want the second Open's span %#v", e.Span, d2.Span)
	}
	if want := `account "Assets:Cash" already opened`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestPlugin_ReferenceBeforeOpen(t *testing.T) {
	// Balance dated 2023-12-31 against an account opened 2024-01-01 must
	// emit exactly one account-not-yet-open diagnostic.
	open := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	bal := &ast.Balance{
		Date:    time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 2, Offset: 30}},
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{open, bal}),
	}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "account-not-yet-open" {
		t.Errorf("Code = %q, want %q", e.Code, "account-not-yet-open")
	}
	if e.Span != bal.Span {
		t.Errorf("Span = %#v, want balance span %#v", e.Span, bal.Span)
	}
	if want := `account "Assets:Cash" is not open on 2023-12-31`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestPlugin_ReferenceOnUnopenedAccount(t *testing.T) {
	// Balance referencing an account that has never been opened.
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{bal}),
	}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if got, want := res.Errors[0].Code, "account-not-open"; got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
}

func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := validations.Apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("validations.Apply on canceled ctx returned nil error, want non-nil")
	}
}

func TestPlugin_OptionsFromRawParseError(t *testing.T) {
	// "inferred_tolerance_multiplier" is a registered decimal-valued
	// option; a non-numeric value triggers a ParseError which the
	// plugin surfaces as api.Error{Code: "invalid-option"}.
	in := api.Input{
		Options: map[string]string{
			"inferred_tolerance_multiplier": "not-a-decimal",
		},
	}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "invalid-option" {
		t.Errorf("Error.Code = %q, want %q", e.Code, "invalid-option")
	}
	if !strings.Contains(e.Message, "inferred_tolerance_multiplier") {
		t.Errorf("Error.Message = %q, want it to mention the option key", e.Message)
	}
}

// TestPlugin_CurrencyNotAllowed feeds a ledger where Assets:Cash only
// allows USD but the transaction posts EUR. The plugin must emit one
// CodeCurrencyNotAllowed diagnostic from the currencyConstraints
// validator.
func TestPlugin_CurrencyNotAllowed(t *testing.T) {
	open1 := &ast.Open{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Cash",
		Currencies: []string{"USD"},
		Span:       ast.Span{Start: ast.Position{Line: 1}},
	}
	open2 := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Income:Salary",
		Span:    ast.Span{Start: ast.Position{Line: 2}},
	}
	eurPos := amtInt(10, "EUR")
	eurNeg := amtInt(-10, "EUR")
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: ast.Span{Start: ast.Position{Line: 3}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &eurPos, Span: ast.Span{Start: ast.Position{Line: 4}}},
			{Account: "Income:Salary", Amount: &eurNeg, Span: ast.Span{Start: ast.Position{Line: 5}}},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{open1, open2, txn})}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "currency-not-allowed" {
		t.Errorf("Code = %q, want %q", e.Code, "currency-not-allowed")
	}
	if want := `currency "EUR" not allowed for account "Assets:Cash"`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

// TestPlugin_UnbalancedTransaction feeds an unbalanced transaction and
// asserts the plugin emits exactly one CodeUnbalancedTransaction from
// the transactionBalances validator.
func TestPlugin_UnbalancedTransaction(t *testing.T) {
	open1 := &ast.Open{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash",
		Span: ast.Span{Start: ast.Position{Line: 1}},
	}
	open2 := &ast.Open{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Expenses:Food",
		Span: ast.Span{Start: ast.Position{Line: 2}},
	}
	pos := amtInt(100, "USD")
	neg := amtInt(-90, "USD")
	txnSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 3}}
	txn := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: txnSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{open1, open2, txn})}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "unbalanced-transaction" {
		t.Errorf("Code = %q, want %q", e.Code, "unbalanced-transaction")
	}
	if e.Span != txnSpan {
		t.Errorf("Span = %#v, want %#v", e.Span, txnSpan)
	}
	if want := `transaction does not balance: non-zero residual in [USD]`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

// TestPlugin_AllFourValidatorsInterleave feeds a single ledger that
// triggers exactly one diagnostic from each of the four validators:
//   - duplicate-open           (openClose)
//   - account-not-open         (activeAccounts: reference to a never-opened
//     account; dated-before-open would also work but would emit two codes)
//   - currency-not-allowed     (currencyConstraints)
//   - unbalanced-transaction   (transactionBalances)
//
// The assertion is that *all four codes* appear in the result set; the
// count may exceed four because accountstate's single-lifecycle view
// triggers cascading diagnostics (e.g. a never-opened account in a
// txn posting also yields a currency pass-through). We check code set
// membership, not exact count.
func TestPlugin_AllFourValidatorsInterleave(t *testing.T) {
	// Account 1: opened twice → duplicate-open.
	openA1 := &ast.Open{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash",
		Currencies: []string{"USD"},
		Span:       ast.Span{Start: ast.Position{Line: 1}},
	}
	openA2 := &ast.Open{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash",
		Span: ast.Span{Start: ast.Position{Line: 2}},
	}
	// Account 2: opened once, used for the other two errors.
	openB := &ast.Open{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Expenses:Food",
		Span: ast.Span{Start: ast.Position{Line: 3}},
	}

	// Currency-not-allowed transaction: EUR on Assets:Cash (USD-only).
	// Balanced, so it contributes exactly one currency-not-allowed diag
	// (and, by design of Assets:Cash's allowlist, one more for the other
	// leg if it were also USD-only; we keep Expenses:Food unrestricted).
	eurPos := amtInt(10, "EUR")
	eurNeg := amtInt(-10, "EUR")
	txnCur := &ast.Transaction{
		Date: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: ast.Span{Start: ast.Position{Line: 4}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &eurPos},
			{Account: "Expenses:Food", Amount: &eurNeg},
		},
	}

	// Unbalanced transaction: 1 USD vs -2 USD on two always-open
	// accounts.
	usdPos := amtInt(1, "USD")
	usdNeg := amtInt(-2, "USD")
	txnUnb := &ast.Transaction{
		Date: time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: ast.Span{Start: ast.Position{Line: 5}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usdPos},
			{Account: "Expenses:Food", Amount: &usdNeg},
		},
	}

	// Account-not-open: Note on a never-opened account.
	note := &ast.Note{
		Date:    time.Date(2024, 3, 3, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Ghost",
		Span:    ast.Span{Start: ast.Position{Line: 6}},
	}

	in := api.Input{Directives: seqOf([]ast.Directive{openA1, openA2, openB, txnCur, txnUnb, note})}
	res, err := validations.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("validations.Apply: unexpected error %v", err)
	}

	got := make(map[string]int, len(res.Errors))
	for _, e := range res.Errors {
		got[e.Code]++
	}
	wantCodes := []string{
		"duplicate-open",
		"account-not-open",
		"currency-not-allowed",
		"unbalanced-transaction",
	}
	for _, c := range wantCodes {
		if got[c] == 0 {
			t.Errorf("missing diagnostic for code %q; got codes = %v (full: %v)", c, got, res.Errors)
		}
	}
}
