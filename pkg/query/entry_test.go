package query_test

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func entryLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{
			Date:    date(2018, 1, 1),
			Account: "Assets:Cash",
			Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"rate": {Kind: ast.MetaString, String: "0.5"}}},
		},
		&ast.Close{Date: date(2023, 1, 1), Account: "Assets:Cash"},
		&ast.Open{Date: date(2019, 1, 1), Account: "Income:Salary"},
		&ast.Transaction{
			Date: date(2020, 5, 1), Flag: '*', Payee: "Boss", Narration: "pay",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "100"), Currency: "USD"}},
				{Account: "Income:Salary", Amount: &ast.Amount{Number: dec(t, "-100"), Currency: "USD"}},
			},
		},
	})
	return l
}

func TestAccountsTableQuery(t *testing.T) {
	res := mustQueryOn(t, "SELECT account, open, close FROM accounts", entryLedger(t))
	if want := []string{"account", "open", "close"}; !equalStrings(colNames(res), want) {
		t.Fatalf("columns = %v, want %v", colNames(res), want)
	}
	if res.Columns[1].Type != types.Entry || res.Columns[2].Type != types.Entry {
		t.Fatalf("open/close not Entry: %+v", res.Columns)
	}
	// Two accounts have an open or close: Assets:Cash, Income:Salary (ascending).
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	acct0, _ := types.AsString(cell(res, 0, 0))
	if acct0 != "Assets:Cash" {
		t.Errorf("row0 account = %q, want Assets:Cash", acct0)
	}
	if cell(res, 0, 1).IsNull() {
		t.Error("Assets:Cash open is NULL, want entry")
	}
	// Income:Salary has an open but no close.
	if !cell(res, 1, 2).IsNull() {
		t.Error("Income:Salary close is not NULL, want NULL")
	}
}

func TestEntryAttributeAccessQuery(t *testing.T) {
	res := mustQueryOn(t, "SELECT account, open.date AS od FROM accounts", entryLedger(t))
	if res.Columns[1].Type != types.Date {
		t.Fatalf("open.date type = %v, want date", res.Columns[1].Type)
	}
	od, _ := types.AsDate(cell(res, 0, 1))
	if od.Year() != 2018 {
		t.Errorf("Assets:Cash open.date year = %d, want 2018", od.Year())
	}
}

func TestEntrySubscriptQuery(t *testing.T) {
	res := mustQueryOn(t, "SELECT account, open.meta['rate'] AS r FROM accounts", entryLedger(t))
	got, _ := types.AsString(cell(res, 0, 1))
	if got != "0.5" {
		t.Errorf("Assets:Cash open.meta['rate'] = %q, want 0.5", got)
	}
}

func TestEntryColumnFromPostings(t *testing.T) {
	res := mustQueryOn(t, "SELECT entry.narration AS n FROM postings", entryLedger(t))
	if len(res.Rows) != 2 { // two postings of the one transaction
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	n, _ := types.AsString(cell(res, 0, 0))
	if n != "pay" {
		t.Errorf("entry.narration = %q, want pay", n)
	}
}

func TestEntryGroupByAndOrderBy(t *testing.T) {
	// Both postings share one transaction, so GROUP BY entry yields one group.
	res := mustQueryOn(t, "SELECT count(account) FROM postings GROUP BY entry", entryLedger(t))
	if len(res.Rows) != 1 {
		t.Fatalf("GROUP BY entry rows = %d, want 1", len(res.Rows))
	}
	// ORDER BY entry must compile and run without error.
	if _, err := query.Query(context.Background(), "SELECT account FROM postings ORDER BY entry", entryLedger(t)); err != nil {
		t.Fatalf("ORDER BY entry: %v", err)
	}
}

func TestEntryFieldAccessErrors(t *testing.T) {
	l := entryLedger(t)
	for _, q := range []string{
		"SELECT narration.foo FROM postings", // attribute on a non-entry
		"SELECT account['x'] FROM postings",  // subscript on a non-dict
		"SELECT entry.bogus FROM postings",   // unknown attribute
	} {
		if _, err := query.Compile(q, l); err == nil {
			t.Errorf("Compile(%q) succeeded, want a compile error", q)
		}
	}
}
