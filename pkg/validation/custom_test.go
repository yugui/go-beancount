package validation

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// cashTxn returns a transaction that moves `amount` USD from Expenses:Food to
// Assets:Cash on the given date. It is a common fixture used to seed the
// running balance before a custom assertion.
func cashTxn(t *testing.T, date string, amount int64) *ast.Transaction {
	t.Helper()
	pos := amt(amount, "USD")
	neg := amt(-amount, "USD")
	return &ast.Transaction{
		Date: parseDay(t, date),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
}

func TestCustomUnknownTypeIsIgnored(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	cd := &ast.Custom{
		Date:     parseDay(t, "2024-02-01"),
		TypeName: "budget",
		Values: []ast.MetaValue{
			{Kind: ast.MetaString, String: "ignored"},
		},
	}
	errs := Check(ledgerOf(append(dirs, cd)...))
	if len(errs) != 0 {
		t.Fatalf("unknown custom: got %v, want no errors", errs)
	}
}

func TestCustomAssertMatching(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	txn := cashTxn(t, "2024-02-01", 100)
	cd := &ast.Custom{
		Date:     parseDay(t, "2024-03-01"),
		TypeName: "assert",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Cash"},
			{Kind: ast.MetaAmount, Amount: amt(100, "USD")},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn, cd)...))
	if len(errs) != 0 {
		t.Fatalf("matching assert: got %v, want no errors", errs)
	}
}

func TestCustomAssertMismatch(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	txn := cashTxn(t, "2024-02-01", 100)
	cd := &ast.Custom{
		Date:     parseDay(t, "2024-03-01"),
		TypeName: "assert",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Cash"},
			{Kind: ast.MetaAmount, Amount: amt(42, "USD")},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn, cd)...))
	wantCodes(t, errs, CodeCustomAssertionFailed)
}

func TestCustomAssertWrongArity(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	cd := &ast.Custom{
		Date:     parseDay(t, "2024-02-01"),
		TypeName: "assert",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Cash"},
		},
	}
	errs := Check(ledgerOf(append(dirs, cd)...))
	wantCodes(t, errs, CodeCustomAssertionFailed)
}

func TestCustomAssertWrongTypes(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	cd := &ast.Custom{
		Date:     parseDay(t, "2024-02-01"),
		TypeName: "assert",
		Values: []ast.MetaValue{
			{Kind: ast.MetaString, String: "Assets:Cash"},
			{Kind: ast.MetaAmount, Amount: amt(100, "USD")},
		},
	}
	errs := Check(ledgerOf(append(dirs, cd)...))
	wantCodes(t, errs, CodeCustomAssertionFailed)
}

func TestCustomAssertUnknownAccount(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	cd := &ast.Custom{
		Date:     parseDay(t, "2024-02-01"),
		TypeName: "assert",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Missing"},
			{Kind: ast.MetaAmount, Amount: amt(0, "USD")},
		},
	}
	errs := Check(ledgerOf(append(dirs, cd)...))
	wantCodes(t, errs, CodeCustomAssertionFailed)
}

func TestRegisterCustomAssertionDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on duplicate registration")
		}
	}()
	RegisterCustomAssertion(assertHandler{})
}
