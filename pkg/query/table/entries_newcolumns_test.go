package table_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestEntriesDescriptionAndAccounts(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	rows := collectRows(tb)
	open, txn, bal, note := rows[0], rows[1], rows[2], rows[3]

	// description: join for a transaction, NULL otherwise.
	assertString(t, valueOf(t, tb, txn, "description"), "ACME | buy")
	if v := valueOf(t, tb, open, "description"); !v.IsNull() || v.Type() != types.String {
		t.Errorf("description(open) = %v (type %v), want typed-NULL String", v.Format(), v.Type())
	}

	// accounts: per account-bearing kind.
	for _, tc := range []struct {
		name string
		row  table.Row
	}{{"txn", txn}, {"open", open}, {"balance", bal}, {"note", note}} {
		got := setElems(t, valueOf(t, tb, tc.row, "accounts"))
		if len(got) != 1 || got[0] != "Assets:Broker" {
			t.Errorf("accounts(%s) = %v, want [Assets:Broker]", tc.name, got)
		}
	}
}

func TestEntriesAccountsPadBothAndNullKinds(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Pad{
			Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Account:    "Assets:Cash",
			PadAccount: "Equity:Opening",
		},
		&ast.Custom{
			Date:     time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			TypeName: "budget",
		},
		&ast.Option{
			Span:  ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 1}},
			Key:   "title",
			Value: "L",
		},
	})
	tb := table.Entries(l)
	var pad, custom, opt table.Row
	for _, r := range collectRows(tb) {
		switch r.(type) {
		case *ast.Pad:
			pad = r
		case *ast.Custom:
			custom = r
		case *ast.Option:
			opt = r
		}
	}
	if pad == nil || custom == nil || opt == nil {
		t.Fatal("missing one of pad/custom/option rows")
	}

	// Pad references both its target and source account.
	if got := setElems(t, valueOf(t, tb, pad, "accounts")); len(got) != 2 || got[0] != "Assets:Cash" || got[1] != "Equity:Opening" {
		t.Errorf("accounts(pad) = %v, want [Assets:Cash Equity:Opening]", got)
	}
	// Custom and a header directive have no account concept → typed NULL.
	for _, tc := range []struct {
		name string
		row  table.Row
	}{{"custom", custom}, {"option", opt}} {
		v := valueOf(t, tb, tc.row, "accounts")
		if !v.IsNull() || v.Type() != types.SetType {
			t.Errorf("accounts(%s) = %v (type %v), want typed-NULL SetType", tc.name, v.Format(), v.Type())
		}
	}
}
