package validations

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// amtDec builds an ast.Amount from a small integer and currency code.
// Mirrors pkg/validation.amt so local tests stay self-contained.
func amtDec(n int64, currency string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: currency}
}

func TestCurrencyConstraints_Name(t *testing.T) {
	v := newCurrencyConstraints(nil)
	if got, want := v.Name(), "currency_constraints"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestCurrencyConstraints_FinishIsNoOp(t *testing.T) {
	v := newCurrencyConstraints(nil)
	if got := v.Finish(); got != nil {
		t.Errorf("Finish() = %v, want nil", got)
	}
}

func TestCurrencyConstraints_EmptyStatePasses(t *testing.T) {
	// Without an open directive the account is absent from state; the
	// currency validator defers to activeAccounts and emits nothing.
	v := newCurrencyConstraints(nil)
	amt := amtDec(10, "EUR")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &amt},
		},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry: got %v, want no errors", errs)
	}
}

func TestCurrencyConstraints_AccountWithoutCurrenciesAllowsAll(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {OpenDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	v := newCurrencyConstraints(state)
	for _, cur := range []string{"USD", "EUR", "JPY"} {
		a := amtDec(1, cur)
		txn := &ast.Transaction{
			Date:     time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Postings: []ast.Posting{{Account: "Assets:Cash", Amount: &a}},
		}
		if errs := v.ProcessEntry(txn); len(errs) != 0 {
			t.Errorf("ProcessEntry(%s): got %v, want no errors", cur, errs)
		}
	}
}

func TestCurrencyConstraints_AllowedCurrencyPasses(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD", "EUR"},
		},
	}
	v := newCurrencyConstraints(state)
	a := amtDec(5, "USD")
	txn := &ast.Transaction{
		Date:     time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Postings: []ast.Posting{{Account: "Assets:Cash", Amount: &a}},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry: got %v, want no errors", errs)
	}
}

func TestCurrencyConstraints_DisallowedCurrencyEmits(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
	}
	v := newCurrencyConstraints(state)
	eur := amtDec(5, "EUR")
	postingSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 9}}
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 8}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &eur, Span: postingSpan},
		},
	}
	// Message wording must match upstream beancount's require-open
	// path verbatim:
	// fmt.Sprintf("currency %q not allowed for account %q", currency, account).
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeCurrencyNotAllowed),
		Span:     postingSpan,
		Message:  `currency "EUR" not allowed for account "Assets:Cash"`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestCurrencyConstraints_PostingSpanFallsBackToTxnSpan(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
	}
	v := newCurrencyConstraints(state)
	eur := amtDec(1, "EUR")
	txnSpan := ast.Span{Start: ast.Position{Line: 42}}
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: txnSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &eur}, // no posting span
		},
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeCurrencyNotAllowed),
		Span:     txnSpan,
		Message:  `currency "EUR" not allowed for account "Assets:Cash"`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry: posting had zero span; want fallback to txn span (-want +got):\n%s", diff)
	}
}

func TestCurrencyConstraints_AccountMissingFromStateIgnored(t *testing.T) {
	// When an account has no lifecycle entry (e.g. never opened),
	// activeAccounts handles the missing-open case and the currency
	// validator must remain silent. This mirrors upstream beancount's
	// require-open dispatch, where the currency check is skipped via
	// early-return once the account lookup fails.
	state := map[ast.Account]*accountstate.State{
		"Assets:Open": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
	}
	v := newCurrencyConstraints(state)
	eur := amtDec(5, "EUR")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Unopened", Amount: &eur},
		},
	}
	if errs := v.ProcessEntry(txn); len(errs) != 0 {
		t.Errorf("ProcessEntry on unopened account: got %v, want no errors", errs)
	}
}

// TestCurrencyConstraints_AutoPostingReports pins the defensive path:
// a posting reaching the validator with a nil Amount is a sign that the
// booking pass did not run. The validator emits
// CodeAutoPostingUnresolved rather than silently skipping, so a
// regression in booking is visible at validation time.
func TestCurrencyConstraints_AutoPostingReports(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
	}
	v := newCurrencyConstraints(state)
	txnSpan := ast.Span{Start: ast.Position{Line: 1}}
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: txnSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash"}, // auto-posting
		},
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAutoPostingUnresolved),
		Span:     txnSpan,
		Message:  `posting on account "Assets:Cash" has no amount; booking pass should have resolved it`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

// TestCurrencyConstraints_BookedPostingChecked pins the booked-AST
// happy path: a posting with an explicit Amount and an allowed currency
// emits no diagnostic. This is the path that runs in the full pipeline,
// where booking precedes validation and the resulting posting carries a
// concrete Amount on a currency the account permits.
func TestCurrencyConstraints_BookedPostingChecked(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
	}
	v := newCurrencyConstraints(state)
	usd := amtDec(5, "USD")
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
		},
	}
	errs := v.ProcessEntry(txn)
	if len(errs) != 0 {
		t.Errorf("ProcessEntry() got %d diagnostics, want 0; errs = %v", len(errs), errs)
	}
}

func TestCurrencyConstraints_MultiplePostingsMixed(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:USD": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
		"Assets:EUR": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"EUR"},
		},
		"Assets:Any": {
			OpenDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			// no currency restriction
		},
	}
	v := newCurrencyConstraints(state)
	usd := amtDec(1, "USD")
	bad := amtDec(2, "GBP") // not allowed on Assets:EUR
	any := amtDec(3, "JPY")
	bad2 := amtDec(4, "EUR") // not allowed on Assets:USD
	sp1 := ast.Span{Start: ast.Position{Line: 2}}
	sp2 := ast.Span{Start: ast.Position{Line: 3}}
	sp3 := ast.Span{Start: ast.Position{Line: 4}}
	sp4 := ast.Span{Start: ast.Position{Line: 5}}
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:USD", Amount: &usd, Span: sp1},  // allowed
			{Account: "Assets:EUR", Amount: &bad, Span: sp2},  // disallowed (GBP)
			{Account: "Assets:Any", Amount: &any, Span: sp3},  // unrestricted
			{Account: "Assets:USD", Amount: &bad2, Span: sp4}, // disallowed (EUR)
		},
	}
	want := []ast.Diagnostic{
		{
			Code:     string(validation.CodeCurrencyNotAllowed),
			Span:     sp2,
			Message:  `currency "GBP" not allowed for account "Assets:EUR"`,
			Severity: ast.Error,
		},
		{
			Code:     string(validation.CodeCurrencyNotAllowed),
			Span:     sp4,
			Message:  `currency "EUR" not allowed for account "Assets:USD"`,
			Severity: ast.Error,
		},
	}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestCurrencyConstraints_IgnoresNonTransactionDirectives(t *testing.T) {
	state := map[ast.Account]*accountstate.State{
		"Assets:Cash": {
			OpenDate:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Currencies: []string{"USD"},
		},
	}
	v := newCurrencyConstraints(state)
	for _, d := range []ast.Directive{
		&ast.Balance{Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Amount: amtDec(0, "EUR")},
		&ast.Note{Date: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"},
		&ast.Open{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"},
	} {
		if errs := v.ProcessEntry(d); len(errs) != 0 {
			t.Errorf("ProcessEntry(%T) = %v, want no errors", d, errs)
		}
	}
}
