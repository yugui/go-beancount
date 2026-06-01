package table_test

import (
	"iter"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func assertString(t *testing.T, v types.Value, want string) {
	t.Helper()
	got, ok := types.AsString(v)
	if !ok {
		t.Fatalf("value %v is NULL or not a String, want %q", v.Format(), want)
	}
	if got != want {
		t.Errorf("string = %q, want %q", got, want)
	}
}

func assertInt(t *testing.T, v types.Value, want int64) {
	t.Helper()
	got, ok := types.AsInt(v)
	if !ok {
		t.Fatalf("value %v is NULL or not an Int, want %d", v.Format(), want)
	}
	if got != want {
		t.Errorf("int = %d, want %d", got, want)
	}
}

func assertDate(t *testing.T, v types.Value, want time.Time) {
	t.Helper()
	got, ok := types.AsDate(v)
	if !ok {
		t.Fatalf("value %v is NULL or not a Date, want %v", v.Format(), want)
	}
	if !got.Equal(want) {
		t.Errorf("date = %v, want %v", got, want)
	}
}

func assertDecimal(t *testing.T, v types.Value, want string) {
	t.Helper()
	got, ok := types.AsDecimal(v)
	if !ok {
		t.Fatalf("value %v is NULL or not a Decimal, want %s", v.Format(), want)
	}
	if got.Text('f') != want {
		t.Errorf("decimal = %s, want %s", got.Text('f'), want)
	}
}

func setElems(t *testing.T, v types.Value) []string {
	t.Helper()
	s, ok := types.AsSet(v)
	if !ok {
		t.Fatalf("value %v is NULL or not a Set", v.Format())
	}
	return s.Elements()
}

// entriesLedger builds a ledger holding one of each of a transaction, open,
// balance, price, and note directive, with distinct dates so the canonical
// iteration order is predictable.
func entriesLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{
			Span:    ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 1}},
			Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Broker",
		},
		&ast.Transaction{
			Span:      ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 5}},
			Date:      time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			Flag:      '*',
			Payee:     "ACME",
			Narration: "buy",
			Tags:      []string{"trip"},
			Links:     []string{"inv-1"},
			Postings: []ast.Posting{
				{Account: "Assets:Broker", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
			},
		},
		&ast.Balance{
			Span:    ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 9}},
			Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Broker",
			Amount:  ast.Amount{Number: dec(t, "1"), Currency: "USD"},
		},
		&ast.Note{
			Span:    ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 11}},
			Date:    time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
			Account: "Assets:Broker",
			Comment: "a note",
			Tags:    []string{"memo"},
		},
		&ast.Price{
			Span:      ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 13}},
			Date:      time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
			Commodity: "STOCK",
			Amount:    ast.Amount{Number: dec(t, "5"), Currency: "USD"},
		},
	})
	return l
}

func TestEntriesColumnSchema(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	if tb.Name != "entries" {
		t.Errorf("table name = %q, want entries", tb.Name)
	}
	want := []struct {
		name string
		typ  types.Type
	}{
		{"type", types.String},
		{"date", types.Date},
		{"year", types.Int},
		{"month", types.Int},
		{"day", types.Int},
		{"filename", types.String},
		{"lineno", types.Int},
		{"flag", types.String},
		{"payee", types.String},
		{"narration", types.String},
		{"tags", types.SetType},
		{"links", types.SetType},
		{"meta", types.DictType},
		{"entry_meta", types.DictType},
		{"any_meta", types.DictType},
		{"id", types.String},
		{"entry", types.Entry},
		{"description", types.String},
		{"accounts", types.SetType},
	}
	if len(tb.Columns) != len(want) {
		t.Fatalf("got %d columns, want %d", len(tb.Columns), len(want))
	}
	for i, w := range want {
		got := tb.Columns[i]
		if got.Name != w.name || got.Type != w.typ {
			t.Errorf("column[%d] = (%q,%v), want (%q,%v)", i, got.Name, got.Type, w.name, w.typ)
		}
	}
}

func TestEntriesRowAccessors(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	rows := collectRows(tb)
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	// Canonical order: open(Jan), transaction(Feb), balance(Mar),
	// note(Apr), price(May).
	open, txn, bal, note := rows[0], rows[1], rows[2], rows[3]

	assertString(t, valueOf(t, tb, open, "type"), "open")
	assertString(t, valueOf(t, tb, txn, "type"), "transaction")
	assertString(t, valueOf(t, tb, bal, "type"), "balance")
	assertString(t, valueOf(t, tb, note, "type"), "note")
	assertString(t, valueOf(t, tb, rows[4], "type"), "price")

	// transaction carries flag/payee/narration/tags/links.
	assertString(t, valueOf(t, tb, txn, "flag"), "*")
	assertString(t, valueOf(t, tb, txn, "payee"), "ACME")
	assertString(t, valueOf(t, tb, txn, "narration"), "buy")
	assertDate(t, valueOf(t, tb, txn, "date"), time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC))
	assertInt(t, valueOf(t, tb, txn, "year"), 2024)
	assertInt(t, valueOf(t, tb, txn, "month"), 2)
	assertInt(t, valueOf(t, tb, txn, "day"), 1)
	assertString(t, valueOf(t, tb, txn, "filename"), "main.beancount")
	assertInt(t, valueOf(t, tb, txn, "lineno"), 5)

	txnTags, ok := types.AsSet(valueOf(t, tb, txn, "tags"))
	if !ok {
		t.Fatal("transaction tags is not a set")
	}
	if got := txnTags.Elements(); len(got) != 1 || got[0] != "trip" {
		t.Errorf("transaction tags = %v, want [trip]", got)
	}

	// note carries tags but no payee/flag concept.
	noteTags, ok := types.AsSet(valueOf(t, tb, note, "tags"))
	if !ok {
		t.Fatal("note tags is not a set")
	}
	if got := noteTags.Elements(); len(got) != 1 || got[0] != "memo" {
		t.Errorf("note tags = %v, want [memo]", got)
	}
}

func TestEntriesNullForNonTransactionFields(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	rows := collectRows(tb)
	open := rows[0]

	// flag/payee/narration are transaction-only → NULL elsewhere.
	for _, col := range []string{"flag", "payee", "narration"} {
		if v := valueOf(t, tb, open, col); !v.IsNull() {
			t.Errorf("open %s = %v, want NULL", col, v.Format())
		}
		if v := valueOf(t, tb, open, col); v.Type() != types.String {
			t.Errorf("open %s NULL carries type %v, want String", col, v.Type())
		}
	}

	// tags/links are NULL on directive types with no tags concept (open).
	for _, col := range []string{"tags", "links"} {
		v := valueOf(t, tb, open, col)
		if !v.IsNull() {
			t.Errorf("open %s = %v, want NULL", col, v.Format())
		}
		if v.Type() != types.SetType {
			t.Errorf("open %s NULL carries type %v, want SetType", col, v.Type())
		}
	}
}

func TestEntriesHeaderDirectiveNullDate(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(&ast.Option{
		Span:  ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 1}},
		Key:   "title",
		Value: "My Ledger",
	})
	tb := table.Entries(l)
	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	opt := rows[0]

	assertString(t, valueOf(t, tb, opt, "type"), "option")
	// header directives have the zero date → date/year/month/day NULL.
	for _, col := range []string{"date", "year", "month", "day"} {
		if v := valueOf(t, tb, opt, col); !v.IsNull() {
			t.Errorf("option %s = %v, want NULL", col, v.Format())
		}
	}
	// filename/lineno still resolve from the span.
	assertString(t, valueOf(t, tb, opt, "filename"), "main.beancount")
	assertInt(t, valueOf(t, tb, opt, "lineno"), 1)
}

func TestEntriesMetaCoercion(t *testing.T) {
	l := &ast.Ledger{}
	metaDate := time.Date(2023, 7, 8, 0, 0, 0, 0, time.UTC)
	l.Insert(&ast.Note{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Broker",
		Comment: "n",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"s": {Kind: ast.MetaString, String: "v"},
			"d": {Kind: ast.MetaDate, Date: metaDate},
			"b": {Kind: ast.MetaBool, Bool: false},
		}},
	})
	tb := table.Entries(l)
	rows := collectRows(tb)

	v := valueOf(t, tb, rows[0], "meta")
	d, ok := types.AsDict(v)
	if !ok {
		t.Fatalf("meta is not a Dict: %v", v)
	}
	if d.Len() != 3 {
		t.Fatalf("meta has %d keys, want 3", d.Len())
	}
	sv, _ := d.Get("s")
	assertString(t, sv, "v")
	dv, _ := d.Get("d")
	assertDate(t, dv, metaDate)
	bv, _ := d.Get("b")
	if bv.Type() != types.Bool {
		t.Errorf("meta[b] type = %v, want Bool", bv.Type())
	}
}

func TestEntriesLazyEarlyExit(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	count := 0
	for range tb.Rows() {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("early-exit visited %d rows, want 1", count)
	}
	if a, b := len(collectRows(tb)), len(collectRows(tb)); a != b || a != 5 {
		t.Fatalf("re-run lengths = %d, %d; want 5, 5", a, b)
	}
}

// TestEntriesOverSyntheticDirective verifies EntriesOver with a hand-built
// iterator factory yielding a zero-Span synthetic transaction: filename and
// lineno return typed NULL, and all other accessors return non-NULL values.
func TestEntriesOverSyntheticDirective(t *testing.T) {
	d := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	synth := &ast.Transaction{
		// zero Span
		Date:      d,
		Flag:      '*',
		Narration: "Opening balance for 'Assets:Cash'",
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "100"), Currency: "USD"}},
		},
	}

	var calls int
	factory := func() iter.Seq2[int, ast.Directive] {
		calls++
		return func(yield func(int, ast.Directive) bool) {
			yield(0, synth)
		}
	}

	tb := table.EntriesOver("scoped-entries", factory)
	if tb.Name != "scoped-entries" {
		t.Errorf("table name = %q, want scoped-entries", tb.Name)
	}

	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]

	// zero Span → filename and lineno are typed NULL.
	fn := valueOf(t, tb, r, "filename")
	if fn.Type() != types.String || !fn.IsNull() {
		t.Errorf("filename = %v (null=%v), want typed-NULL String", fn.Format(), fn.IsNull())
	}
	ln := valueOf(t, tb, r, "lineno")
	if ln.Type() != types.Int || !ln.IsNull() {
		t.Errorf("lineno = %v (null=%v), want typed-NULL Int", ln.Format(), ln.IsNull())
	}

	assertString(t, valueOf(t, tb, r, "type"), "transaction")
	assertDate(t, valueOf(t, tb, r, "date"), d)
	assertInt(t, valueOf(t, tb, r, "year"), 2024)
	assertInt(t, valueOf(t, tb, r, "month"), 6)
	assertInt(t, valueOf(t, tb, r, "day"), 1)
	assertString(t, valueOf(t, tb, r, "flag"), "*")
	assertString(t, valueOf(t, tb, r, "narration"), "Opening balance for 'Assets:Cash'")

	// Re-runnable: factory is re-invoked on each Rows() call.
	collectRows(tb)
	if calls != 2 {
		t.Errorf("factory called %d times after two Rows() calls, want 2", calls)
	}
}

func TestEntriesIDNeverNull(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	for i, r := range collectRows(tb) {
		v := valueOf(t, tb, r, "id")
		s, ok := types.AsString(v)
		if !ok || len(s) != 32 {
			t.Errorf("id[%d] = %v, want a 32-hex String (never NULL)", i, v.Format())
		}
	}
}

func TestEntriesDescriptionJoinAndNull(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	rows := collectRows(tb)
	open, txn := rows[0], rows[1]

	assertString(t, valueOf(t, tb, txn, "description"), "ACME | buy")
	if v := valueOf(t, tb, open, "description"); !v.IsNull() || v.Type() != types.String {
		t.Errorf("description(open) = %v (type %v), want typed-NULL String", v.Format(), v.Type())
	}
}

func TestEntriesAccountsPerKind(t *testing.T) {
	tb := table.Entries(entriesLedger(t))
	rows := collectRows(tb)
	open, txn, bal, note := rows[0], rows[1], rows[2], rows[3]

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

// TestEntriesAccountsPadAndAccountlessKinds pins that a Pad reports both its
// target and source account, while kinds with no account concept yield a typed
// NULL Set rather than an empty Set.
func TestEntriesAccountsPadAndAccountlessKinds(t *testing.T) {
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

	if got := setElems(t, valueOf(t, tb, pad, "accounts")); len(got) != 2 || got[0] != "Assets:Cash" || got[1] != "Equity:Opening" {
		t.Errorf("accounts(pad) = %v, want [Assets:Cash Equity:Opening]", got)
	}
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
