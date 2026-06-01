package table_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func accountsLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	d := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{
			Date:       d(2018),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta:       ast.Metadata{Props: map[string]ast.MetaValue{"rate": {Kind: ast.MetaString, String: "0.5"}}},
		},
		&ast.Close{Date: d(2023), Account: "Assets:Cash"},
		&ast.Open{Date: d(2019), Account: "Assets:Bank"},       // open, no close
		&ast.Close{Date: d(2020), Account: "Liabilities:Card"}, // close, no open
		&ast.Open{Date: d(2099), Account: "Assets:Cash"},       // duplicate open ignored
	})
	return l
}

func TestAccountsColumnSchema(t *testing.T) {
	tb := table.Accounts(accountsLedger(t))
	if tb.Name != "accounts" {
		t.Errorf("name = %q, want accounts", tb.Name)
	}
	want := []struct {
		name string
		typ  types.Type
	}{
		{"account", types.String},
		{"open", types.Entry},
		{"close", types.Entry},
	}
	if len(tb.Columns) != len(want) {
		t.Fatalf("got %d columns, want %d", len(tb.Columns), len(want))
	}
	for i, w := range want {
		if c := tb.Columns[i]; c.Name != w.name || c.Type != w.typ {
			t.Errorf("column[%d] = (%q,%v), want (%q,%v)", i, c.Name, c.Type, w.name, w.typ)
		}
	}
}

func TestAccountsRows(t *testing.T) {
	tb := table.Accounts(accountsLedger(t))
	rows := collectRows(tb)

	// One row per account with an open or close, ascending by account name.
	var names []string
	for _, r := range rows {
		s, _ := types.AsString(valueOf(t, tb, r, "account"))
		names = append(names, s)
	}
	wantNames := []string{"Assets:Bank", "Assets:Cash", "Liabilities:Card"}
	if len(names) != len(wantNames) {
		t.Fatalf("accounts = %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] {
			t.Fatalf("accounts = %v, want %v", names, wantNames)
		}
	}

	// Assets:Cash: open present (first one, 2018), close present.
	cash := rows[1]
	open := valueOf(t, tb, cash, "open")
	if open.IsNull() {
		t.Fatal("Assets:Cash open is NULL, want an entry")
	}
	d, ok := types.AsEntry(open)
	if !ok {
		t.Fatalf("open is not an Entry: %v", open)
	}
	if od := d.(*ast.Open); od.Date.Year() != 2018 {
		t.Errorf("open date = %d, want the first open (2018)", od.Date.Year())
	}
	if valueOf(t, tb, cash, "close").IsNull() {
		t.Error("Assets:Cash close is NULL, want an entry")
	}

	// Assets:Bank: open present, close NULL.
	bank := rows[0]
	if valueOf(t, tb, bank, "open").IsNull() {
		t.Error("Assets:Bank open is NULL, want an entry")
	}
	if !valueOf(t, tb, bank, "close").IsNull() {
		t.Error("Assets:Bank close is not NULL, want NULL")
	}

	// Liabilities:Card: open NULL, close present.
	card := rows[2]
	if !valueOf(t, tb, card, "open").IsNull() {
		t.Error("Liabilities:Card open is not NULL, want NULL")
	}
	if valueOf(t, tb, card, "close").IsNull() {
		t.Error("Liabilities:Card close is NULL, want an entry")
	}
}

func TestEntryAttributeRegistry(t *testing.T) {
	open := &ast.Open{
		Date:       time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Cash",
		Currencies: []string{"USD", "EUR"},
	}
	cases := []struct {
		attr string
		typ  types.Type
		want string // Format() of the value
	}{
		{"account", types.String, "Assets:Cash"},
		{"date", types.Date, "2020-01-01"},
		{"type", types.String, "open"},
		{"currencies", types.SetType, "{EUR, USD}"},
	}
	for _, tc := range cases {
		typ, get, ok := table.EntryAttribute(tc.attr)
		if !ok {
			t.Errorf("EntryAttribute(%q) not found", tc.attr)
			continue
		}
		if typ != tc.typ {
			t.Errorf("EntryAttribute(%q) type = %v, want %v", tc.attr, typ, tc.typ)
		}
		if got := get(open).Format(); got != tc.want {
			t.Errorf("%s(open) = %q, want %q", tc.attr, got, tc.want)
		}
	}

	// narration does not apply to an Open: typed NULL String.
	_, get, _ := table.EntryAttribute("narration")
	if v := get(open); !v.IsNull() || v.Type() != types.String {
		t.Errorf("narration(open) = %v (null=%v), want NULL String", v, v.IsNull())
	}

	if _, _, ok := table.EntryAttribute("nonesuch"); ok {
		t.Error("EntryAttribute(nonesuch) found, want absent")
	}
}
