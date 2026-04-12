package validation

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestPadResolvedByMatchingBalance(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Equity:Opening")
	pad := &ast.Pad{
		Date:       parseDay(t, "2024-01-15"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Assets:Cash",
		Amount:  amt(1000, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, pad, bal)...))
	if len(errs) != 0 {
		t.Fatalf("pad+balance: got %v, want no errors", errs)
	}
}

func TestPadZeroAdjustmentNeeded(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Equity:Opening", "Income:Salary")
	pos := amt(1000, "USD")
	neg := amt(-1000, "USD")
	txn := &ast.Transaction{
		Date: parseDay(t, "2024-01-10"),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	pad := &ast.Pad{
		Date:       parseDay(t, "2024-01-15"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Assets:Cash",
		Amount:  amt(1000, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, txn, pad, bal)...))
	if len(errs) != 0 {
		t.Fatalf("pad with zero delta: got %v, want no errors", errs)
	}
}

func TestPadWithInterveningTransaction(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Equity:Opening", "Income:Salary")
	pad := &ast.Pad{
		Date:       parseDay(t, "2024-01-15"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	pos := amt(50, "USD")
	neg := amt(-50, "USD")
	txn := &ast.Transaction{
		Date: parseDay(t, "2024-01-20"),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Assets:Cash",
		Amount:  amt(150, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, pad, txn, bal)...))
	if len(errs) != 0 {
		t.Fatalf("pad+txn+balance: got %v, want no errors", errs)
	}
}

func TestPadUnresolvedAtEndOfLedger(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Equity:Opening")
	padSpan := ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 10, Column: 1}}
	pad := &ast.Pad{
		Span:       padSpan,
		Date:       parseDay(t, "2024-01-15"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	errs := Check(ledgerOf(append(dirs, pad)...))
	wantCodes(t, errs, CodePadUnresolved)
	if errs[0].Span != padSpan {
		t.Errorf("span = %+v, want %+v", errs[0].Span, padSpan)
	}
}

func TestTwoPadsSameAccountFirstDropped(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Equity:Opening", "Equity:OtherOpening")
	firstSpan := ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 5, Column: 1}}
	pad1 := &ast.Pad{
		Span:       firstSpan,
		Date:       parseDay(t, "2024-01-10"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	pad2 := &ast.Pad{
		Date:       parseDay(t, "2024-01-15"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:OtherOpening",
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Assets:Cash",
		Amount:  amt(500, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, pad1, pad2, bal)...))
	wantCodes(t, errs, CodePadUnresolved)
	if errs[0].Span != firstSpan {
		t.Errorf("span = %+v, want %+v (first pad)", errs[0].Span, firstSpan)
	}
}

func TestPadNotConsumedByDifferentAccountBalance(t *testing.T) {
	// A pending pad on Assets:Cash is not consumed by a balance directive on
	// a different account, and so it remains pending and is reported as
	// unresolved at end-of-ledger.
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Equity:Opening")
	padSpan := ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 7, Column: 1}}
	pad := &ast.Pad{
		Span:       padSpan,
		Date:       parseDay(t, "2024-01-10"),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	otherBal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Equity:Opening",
		Amount:  amt(0, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, pad, otherBal)...))
	wantCodes(t, errs, CodePadUnresolved)
	if errs[0].Span != padSpan {
		t.Errorf("span = %+v, want %+v", errs[0].Span, padSpan)
	}
}
