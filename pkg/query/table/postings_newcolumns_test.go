package table_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func setElems(t *testing.T, v types.Value) []string {
	t.Helper()
	s, ok := types.AsSet(v)
	if !ok {
		t.Fatalf("value %v is NULL or not a Set", v.Format())
	}
	return s.Elements()
}

func TestPostingsNewColumns(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)
	rows := collectRows(tb)
	first, auto := rows[0], rows[1]

	assertString(t, valueOf(t, tb, first, "location"), "main.beancount:13:")
	assertString(t, valueOf(t, tb, first, "description"), "ACME | buy stock")

	// other_accounts excludes the current posting by index.
	if got := setElems(t, valueOf(t, tb, first, "other_accounts")); len(got) != 1 || got[0] != "Assets:Cash" {
		t.Errorf("other_accounts(first) = %v, want [Assets:Cash]", got)
	}
	// accounts includes the current posting.
	if got := setElems(t, valueOf(t, tb, first, "accounts")); len(got) != 2 || got[0] != "Assets:Broker" || got[1] != "Assets:Cash" {
		t.Errorf("accounts(first) = %v, want [Assets:Broker Assets:Cash]", got)
	}

	// posting_flag: the posting's own flag, NOT the txn fallback.
	assertString(t, valueOf(t, tb, first, "posting_flag"), "!")
	pf := valueOf(t, tb, auto, "posting_flag")
	if !pf.IsNull() {
		t.Errorf("posting_flag(auto) = %v, want NULL (no own flag, no txn fallback)", pf.Format())
	}
	if pf.Type() != types.String {
		t.Errorf("posting_flag NULL carries type %v, want String", pf.Type())
	}
}

func TestPostingsOtherAccountsKeepsDuplicateSibling(t *testing.T) {
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
			{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "-1"), Currency: "USD"}},
			{Account: "Expenses:Fee", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
		},
	}
	l := &ast.Ledger{}
	l.Insert(txn)
	tb := table.Postings(l)
	rows := collectRows(tb)

	// Excluding posting 0 by index still leaves the sibling sharing its account.
	got := setElems(t, valueOf(t, tb, rows[0], "other_accounts"))
	if len(got) != 2 || got[0] != "Assets:Cash" || got[1] != "Expenses:Fee" {
		t.Errorf("other_accounts(0) = %v, want [Assets:Cash Expenses:Fee] (dedup of two siblings)", got)
	}
}

func TestPostingsDescriptionVariants(t *testing.T) {
	cases := []struct {
		payee, narration, want string
		wantNull               bool
	}{
		{"ACME", "buy", "ACME | buy", false},
		{"ACME", "", "ACME", false},
		{"", "buy", "buy", false},
		{"", "", "", true},
	}
	for _, c := range cases {
		txn := &ast.Transaction{
			Date:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Flag:      '*',
			Payee:     c.payee,
			Narration: c.narration,
			Postings:  []ast.Posting{{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}}},
		}
		l := &ast.Ledger{}
		l.Insert(txn)
		tb := table.Postings(l)
		v := valueOf(t, tb, collectRows(tb)[0], "description")
		if c.wantNull {
			if !v.IsNull() {
				t.Errorf("description(%q,%q) = %v, want NULL", c.payee, c.narration, v.Format())
			}
			continue
		}
		assertString(t, v, c.want)
	}
}

func TestPostingsLocationNullOnZeroSpan(t *testing.T) {
	txn := &ast.Transaction{
		Date:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag:     '*',
		Postings: []ast.Posting{{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}}},
	}
	l := &ast.Ledger{}
	l.Insert(txn)
	tb := table.Postings(l)
	v := valueOf(t, tb, collectRows(tb)[0], "location")
	if !v.IsNull() || v.Type() != types.String {
		t.Errorf("location on zero span = %v (type %v), want typed-NULL String", v.Format(), v.Type())
	}
}
