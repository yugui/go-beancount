package validation

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

func parseDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

func amt(n int64, cur string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: cur}
}

func ledgerOf(dirs ...ast.Directive) *ast.Ledger {
	return &ast.Ledger{Directives: dirs}
}

func codes(errs []Error) []Code {
	out := make([]Code, len(errs))
	for i, e := range errs {
		out[i] = e.Code
	}
	return out
}

func wantCodes(t *testing.T, errs []Error, want ...Code) {
	t.Helper()
	got := codes(errs)
	if len(got) != len(want) {
		t.Fatalf("got errors %v, want codes %v (full: %v)", got, want, errs)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("error[%d] code = %v, want %v (full: %v)", i, got[i], want[i], errs)
		}
	}
}

func TestUseBeforeOpen(t *testing.T) {
	d := parseDay(t, "2024-01-02")
	a := amt(10, "USD")
	na := amt(-10, "USD")
	txn := &ast.Transaction{
		Date: d,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Income:Salary", Amount: &na},
		},
	}
	errs := Check(ledgerOf(txn))
	wantCodes(t, errs, CodeAccountNotOpen, CodeAccountNotOpen)
}

func TestDuplicateOpen(t *testing.T) {
	d1 := parseDay(t, "2024-01-01")
	d2 := parseDay(t, "2024-02-01")
	o1 := &ast.Open{Date: d1, Account: "Assets:Cash"}
	o2 := &ast.Open{Date: d2, Account: "Assets:Cash"}
	errs := Check(ledgerOf(o1, o2))
	wantCodes(t, errs, CodeDuplicateOpen)
}

func TestCloseThenUse(t *testing.T) {
	od := parseDay(t, "2024-01-01")
	cd := parseDay(t, "2024-06-01")
	td := parseDay(t, "2024-07-01")
	a := amt(10, "USD")
	na := amt(-10, "USD")

	open1 := &ast.Open{Date: od, Account: "Assets:Cash"}
	open2 := &ast.Open{Date: od, Account: "Income:Salary"}
	cls := &ast.Close{Date: cd, Account: "Assets:Cash"}
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Income:Salary", Amount: &na},
		},
	}
	errs := Check(ledgerOf(open1, open2, cls, txn))
	wantCodes(t, errs, CodeAccountClosed)
}

func TestCloseBeforeOpen(t *testing.T) {
	d := parseDay(t, "2024-01-01")
	cls := &ast.Close{Date: d, Account: "Assets:Cash"}
	errs := Check(ledgerOf(cls))
	wantCodes(t, errs, CodeAccountNotOpen)
}

func TestCurrencyNotAllowed(t *testing.T) {
	od := parseDay(t, "2024-01-01")
	td := parseDay(t, "2024-02-01")
	eur := amt(10, "EUR")
	neur := amt(-10, "EUR")

	open1 := &ast.Open{Date: od, Account: "Assets:Cash", Currencies: []string{"USD"}}
	open2 := &ast.Open{Date: od, Account: "Income:Salary"}
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &eur},
			{Account: "Income:Salary", Amount: &neur},
		},
	}
	errs := Check(ledgerOf(open1, open2, txn))
	wantCodes(t, errs, CodeCurrencyNotAllowed)
}

func TestValidLifecycle(t *testing.T) {
	od := parseDay(t, "2024-01-01")
	td := parseDay(t, "2024-02-01")
	cd := parseDay(t, "2024-12-31")
	a := amt(10, "USD")
	na := amt(-10, "USD")

	open1 := &ast.Open{Date: od, Account: "Assets:Cash", Currencies: []string{"USD"}}
	open2 := &ast.Open{Date: od, Account: "Income:Salary", Currencies: []string{"USD"}}
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Income:Salary", Amount: &na},
		},
	}
	cls1 := &ast.Close{Date: cd, Account: "Assets:Cash"}
	cls2 := &ast.Close{Date: cd, Account: "Income:Salary"}
	errs := Check(ledgerOf(open1, open2, txn, cls1, cls2))
	if len(errs) != 0 {
		t.Fatalf("valid lifecycle: got %v, want no errors", errs)
	}
}

func TestSameDayOpenAndTransaction(t *testing.T) {
	d := parseDay(t, "2024-01-01")
	a := amt(10, "USD")
	na := amt(-10, "USD")
	open1 := &ast.Open{Date: d, Account: "Assets:Cash"}
	open2 := &ast.Open{Date: d, Account: "Income:Salary"}
	txn := &ast.Transaction{
		Date: d,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Income:Salary", Amount: &na},
		},
	}
	// Source order: txn first, then opens. Directive ordering should fix this.
	errs := Check(ledgerOf(txn, open1, open2))
	if len(errs) != 0 {
		t.Fatalf("same-day open+txn: got %v, want no errors", errs)
	}
}

func TestBalanceOnClosedAccount(t *testing.T) {
	od := parseDay(t, "2024-01-01")
	cd := parseDay(t, "2024-06-01")
	bd := parseDay(t, "2024-07-01")

	open1 := &ast.Open{Date: od, Account: "Assets:Cash"}
	cls := &ast.Close{Date: cd, Account: "Assets:Cash"}
	bal := &ast.Balance{Date: bd, Account: "Assets:Cash", Amount: amt(0, "USD")}
	errs := Check(ledgerOf(open1, cls, bal))
	wantCodes(t, errs, CodeAccountClosed)
}

func TestValidBalancePadNoteDocument(t *testing.T) {
	od := parseDay(t, "2024-01-01")
	d := parseDay(t, "2024-02-01")

	open1 := &ast.Open{Date: od, Account: "Assets:Cash", Currencies: []string{"USD"}}
	open2 := &ast.Open{Date: od, Account: "Equity:Opening"}
	// Balance asserts zero because the pad directive is not yet resolved
	// in this step; pad resolution will land in a later phase.
	bal := &ast.Balance{Date: d, Account: "Assets:Cash", Amount: amt(0, "USD")}
	pad := &ast.Pad{Date: d, Account: "Assets:Cash", PadAccount: "Equity:Opening"}
	note := &ast.Note{Date: d, Account: "Assets:Cash", Comment: "hello"}
	doc := &ast.Document{Date: d, Account: "Assets:Cash", Path: "/tmp/x.pdf"}

	errs := Check(ledgerOf(open1, open2, bal, pad, note, doc))
	if len(errs) != 0 {
		t.Fatalf("valid refs: got %v, want no errors", errs)
	}
}
