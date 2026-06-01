package table_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// assertSchema checks a table's name and ordered (name,type) column list.
func assertSchema(t *testing.T, tb *table.Table, name string, want [][2]any) {
	t.Helper()
	if tb.Name != name {
		t.Errorf("table name = %q, want %q", tb.Name, name)
	}
	if len(tb.Columns) != len(want) {
		t.Fatalf("got %d columns, want %d", len(tb.Columns), len(want))
	}
	for i, w := range want {
		got := tb.Columns[i]
		if got.Name != w[0] || got.Type != w[1] {
			t.Errorf("column[%d] = (%q,%v), want (%q,%v)", i, got.Name, got.Type, w[0], w[1])
		}
	}
}

// mixedLedger holds one of each directive the new tables filter on, plus an
// extra unrelated directive, so each table's filter is exercised.
func mixedLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"},
		&ast.Commodity{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Currency: "USD"},
		&ast.Transaction{
			Date:      time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag:      '*',
			Payee:     "ACME",
			Narration: "buy",
			Tags:      []string{"trip"},
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "-5"), Currency: "USD"}},
				{Account: "Expenses:Food", Amount: &ast.Amount{Number: dec(t, "5"), Currency: "USD"}},
			},
		},
		&ast.Note{Date: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Comment: "hi", Tags: []string{"memo"}},
		&ast.Event{Date: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), Name: "location", Value: "Paris"},
		&ast.Document{Date: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Path: "/receipts/a.pdf"},
		&ast.Price{Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Commodity: "STOCK", Amount: ast.Amount{Number: dec(t, "7"), Currency: "USD"}},
	})
	return l
}

func TestPricesTable(t *testing.T) {
	tb := table.PricesOver("prices", mixedLedger(t).All)
	assertSchema(t, tb, "prices", [][2]any{
		{"date", types.Date}, {"currency", types.String},
		{"amount", types.Amount}, {"meta", types.DictType},
	})
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Price", len(rows))
	}
	assertDate(t, valueOf(t, tb, rows[0], "date"), time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	assertString(t, valueOf(t, tb, rows[0], "currency"), "STOCK")
	a, ok := types.AsAmount(valueOf(t, tb, rows[0], "amount"))
	if !ok || a.Currency != "USD" || a.Number.Text('f') != "7" {
		t.Errorf("amount = %v, want 7 USD", valueOf(t, tb, rows[0], "amount").Format())
	}
	// meta is always a Dict (empty here), never NULL.
	if d, ok := types.AsDict(valueOf(t, tb, rows[0], "meta")); !ok || d.Len() != 0 {
		t.Errorf("meta = %v, want empty Dict", valueOf(t, tb, rows[0], "meta").Format())
	}
}

func TestCommoditiesTable(t *testing.T) {
	tb := table.CommoditiesOver("commodities", mixedLedger(t).All)
	assertSchema(t, tb, "commodities", [][2]any{
		{"currency", types.String}, {"date", types.Date}, {"meta", types.DictType},
	})
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Commodity", len(rows))
	}
	assertString(t, valueOf(t, tb, rows[0], "currency"), "USD")
	assertDate(t, valueOf(t, tb, rows[0], "date"), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
}

func TestTransactionsTable(t *testing.T) {
	tb := table.TransactionsOver("transactions", mixedLedger(t).All)
	assertSchema(t, tb, "transactions", [][2]any{
		{"date", types.Date}, {"flag", types.String}, {"payee", types.String},
		{"narration", types.String}, {"description", types.String},
		{"tags", types.SetType}, {"links", types.SetType},
		{"accounts", types.SetType}, {"meta", types.DictType},
	})
	// no `postings` column, matching upstream.
	if _, ok := tb.Column("postings"); ok {
		t.Error("transactions table has a postings column; upstream deletes it")
	}
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Transaction", len(rows))
	}
	assertString(t, valueOf(t, tb, rows[0], "flag"), "*")
	assertString(t, valueOf(t, tb, rows[0], "description"), "ACME | buy")
	// accounts is deduped + sorted across postings.
	got := setElems(t, valueOf(t, tb, rows[0], "accounts"))
	if len(got) != 2 || got[0] != "Assets:Cash" || got[1] != "Expenses:Food" {
		t.Errorf("accounts = %v, want [Assets:Cash Expenses:Food]", got)
	}
}

func TestNotesTable(t *testing.T) {
	tb := table.NotesOver("notes", mixedLedger(t).All)
	assertSchema(t, tb, "notes", [][2]any{
		{"date", types.Date}, {"account", types.String}, {"comment", types.String},
		{"tags", types.SetType}, {"links", types.SetType}, {"meta", types.DictType},
	})
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Note", len(rows))
	}
	assertString(t, valueOf(t, tb, rows[0], "account"), "Assets:Cash")
	assertString(t, valueOf(t, tb, rows[0], "comment"), "hi")
	if got := setElems(t, valueOf(t, tb, rows[0], "tags")); len(got) != 1 || got[0] != "memo" {
		t.Errorf("tags = %v, want [memo]", got)
	}
}

func TestEventsTable(t *testing.T) {
	tb := table.EventsOver("events", mixedLedger(t).All)
	assertSchema(t, tb, "events", [][2]any{
		{"date", types.Date}, {"type", types.String},
		{"description", types.String}, {"meta", types.DictType},
	})
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Event", len(rows))
	}
	// `type`/`description` are the upstream names for Name/Value.
	assertString(t, valueOf(t, tb, rows[0], "type"), "location")
	assertString(t, valueOf(t, tb, rows[0], "description"), "Paris")
}

func TestDocumentsTable(t *testing.T) {
	tb := table.DocumentsOver("documents", mixedLedger(t).All)
	assertSchema(t, tb, "documents", [][2]any{
		{"date", types.Date}, {"account", types.String}, {"filename", types.String},
		{"tags", types.SetType}, {"links", types.SetType}, {"meta", types.DictType},
	})
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Document", len(rows))
	}
	assertString(t, valueOf(t, tb, rows[0], "account"), "Assets:Cash")
	// `filename` is the upstream name for the document Path.
	assertString(t, valueOf(t, tb, rows[0], "filename"), "/receipts/a.pdf")
}

func TestBalancesTable(t *testing.T) {
	tol := dec(t, "0.005")
	diff := ast.Amount{Number: dec(t, "0.01"), Currency: "USD"}
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Balance{
			Date:       time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
			Account:    "Assets:Cash",
			Amount:     ast.Amount{Number: dec(t, "100"), Currency: "USD"},
			Tolerance:  &tol,
			DiffAmount: &diff,
		},
		&ast.Open{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"},
	})
	tb := table.BalancesOver("balances", l.All)

	assertSchema(t, tb, "balances", [][2]any{
		{"date", types.Date},
		{"account", types.String},
		{"amount", types.Amount},
		{"tolerance", types.Decimal},
		{"discrepancy", types.Amount},
		{"meta", types.DictType},
	})

	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 Balance", len(rows))
	}
	r := rows[0]

	assertDate(t, valueOf(t, tb, r, "date"), time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC))
	assertString(t, valueOf(t, tb, r, "account"), "Assets:Cash")

	a, ok := types.AsAmount(valueOf(t, tb, r, "amount"))
	if !ok || a.Currency != "USD" || a.Number.Text('f') != "100" {
		t.Errorf("amount = %v, want 100 USD", valueOf(t, tb, r, "amount").Format())
	}

	assertDecimal(t, valueOf(t, tb, r, "tolerance"), "0.005")

	da, ok := types.AsAmount(valueOf(t, tb, r, "discrepancy"))
	if !ok || da.Currency != "USD" || da.Number.Text('f') != "0.01" {
		t.Errorf("discrepancy = %v, want 0.01 USD", valueOf(t, tb, r, "discrepancy").Format())
	}

	if d, ok := types.AsDict(valueOf(t, tb, r, "meta")); !ok || d.Len() != 0 {
		t.Errorf("meta = %v, want empty Dict", valueOf(t, tb, r, "meta").Format())
	}

	// tolerance and discrepancy are typed NULL when fields are nil.
	l2 := &ast.Ledger{}
	l2.Insert(&ast.Balance{
		Date:    time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  ast.Amount{Number: dec(t, "50"), Currency: "USD"},
	})
	tb2 := table.BalancesOver("balances", l2.All)
	rows2 := collectRows(tb2)
	if len(rows2) != 1 {
		t.Fatalf("got %d rows, want 1 Balance", len(rows2))
	}
	r2 := rows2[0]

	tolVal := valueOf(t, tb2, r2, "tolerance")
	if !tolVal.IsNull() || tolVal.Type() != types.Decimal {
		t.Errorf("tolerance = %v, want typed NULL Decimal", tolVal.Format())
	}
	discVal := valueOf(t, tb2, r2, "discrepancy")
	if !discVal.IsNull() || discVal.Type() != types.Amount {
		t.Errorf("discrepancy = %v, want typed NULL Amount", discVal.Format())
	}
}

// TestDirectiveTablesLazyRerunnable confirms the shared directiveRows spine is
// re-runnable and supports early exit, like the postings/entries tables.
func TestDirectiveTablesLazyRerunnable(t *testing.T) {
	tb := table.NotesOver("notes", mixedLedger(t).All)
	count := 0
	for range tb.Rows() {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("early-exit visited %d rows, want 1", count)
	}
	if a, b := len(collectRows(tb)), len(collectRows(tb)); a != b || a != 1 {
		t.Fatalf("re-run lengths = %d, %d; want 1, 1", a, b)
	}
}
