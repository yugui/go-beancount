package table_test

import (
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

// stockTxn builds a transaction whose first posting buys 10 STOCK at a booked
// cost of 3 USD with an @ price of 4 USD, and whose second posting is an
// auto-posting (nil Amount). The posting metadata on the first posting
// exercises every ast.MetaValueKind. The transaction carries tags and links.
func stockTxn(t *testing.T) *ast.Transaction {
	t.Helper()
	d := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	metaDate := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)
	num := dec(t, "1.5")
	return &ast.Transaction{
		Span:      ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 12}},
		Date:      d,
		Flag:      '*',
		Payee:     "ACME",
		Narration: "buy stock",
		Tags:      []string{"trip", "q1"},
		Links:     []string{"inv-1"},
		Postings: []ast.Posting{
			{
				Span:    ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 13}},
				Flag:    '!',
				Account: "Assets:Broker",
				Amount:  &ast.Amount{Number: dec(t, "10"), Currency: "STOCK"},
				Cost: &ast.Cost{
					Number:   dec(t, "3"),
					Currency: "USD",
					Date:     time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
					Label:    "lot-a",
				},
				Price: &ast.PriceAnnotation{
					Amount:  ast.Amount{Number: dec(t, "4"), Currency: "USD"},
					IsTotal: false,
				},
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"k_string":   {Kind: ast.MetaString, String: "s"},
					"k_account":  {Kind: ast.MetaAccount, String: "Assets:Cash"},
					"k_currency": {Kind: ast.MetaCurrency, String: "USD"},
					"k_date":     {Kind: ast.MetaDate, Date: metaDate},
					"k_tag":      {Kind: ast.MetaTag, String: "tagval"},
					"k_link":     {Kind: ast.MetaLink, String: "linkval"},
					"k_number":   {Kind: ast.MetaNumber, Number: num},
					"k_amount":   {Kind: ast.MetaAmount, Amount: ast.Amount{Number: dec(t, "2"), Currency: "EUR"}},
					"k_bool":     {Kind: ast.MetaBool, Bool: true},
				}},
			},
			{
				Span:    ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 14}},
				Account: "Assets:Cash",
				Amount:  nil,
			},
		},
	}
}

func collectRows(tb *table.Table) []table.Row {
	var rows []table.Row
	for r := range tb.Rows() {
		rows = append(rows, r)
	}
	return rows
}

func valueOf(t *testing.T, tb *table.Table, row table.Row, col string) types.Value {
	t.Helper()
	c, ok := tb.Column(col)
	if !ok {
		t.Fatalf("column %q not found", col)
	}
	return c.Accessor(row)
}

func TestPostingsColumnSchema(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)

	if tb.Name != "postings" {
		t.Errorf("table name = %q, want postings", tb.Name)
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
		{"account", types.String},
		{"number", types.Decimal},
		{"currency", types.String},
		{"cost_number", types.Decimal},
		{"cost_currency", types.String},
		{"cost_date", types.Date},
		{"cost_label", types.String},
		{"position", types.Position},
		{"weight", types.Amount},
		{"price", types.Amount},
		{"meta", types.DictType},
		{"entry_meta", types.DictType},
		{"any_meta", types.DictType},
		{"id", types.String},
		{"location", types.String},
		{"description", types.String},
		{"other_accounts", types.SetType},
		{"accounts", types.SetType},
		{"posting_flag", types.String},
		{"balance", types.Inventory},
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

func TestPostingsFirstPostingAccessors(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)
	rows := collectRows(tb)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one per posting)", len(rows))
	}
	r := rows[0]

	assertString(t, valueOf(t, tb, r, "type"), "transaction")
	assertDate(t, valueOf(t, tb, r, "date"), time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC))
	assertInt(t, valueOf(t, tb, r, "year"), 2024)
	assertInt(t, valueOf(t, tb, r, "month"), 3)
	assertInt(t, valueOf(t, tb, r, "day"), 15)
	assertString(t, valueOf(t, tb, r, "filename"), "main.beancount")
	assertInt(t, valueOf(t, tb, r, "lineno"), 13)
	// posting flag '!' overrides the txn flag '*'.
	assertString(t, valueOf(t, tb, r, "flag"), "!")
	assertString(t, valueOf(t, tb, r, "payee"), "ACME")
	assertString(t, valueOf(t, tb, r, "narration"), "buy stock")
	assertString(t, valueOf(t, tb, r, "account"), "Assets:Broker")
	assertDecimal(t, valueOf(t, tb, r, "number"), "10")
	assertString(t, valueOf(t, tb, r, "currency"), "STOCK")
	assertDecimal(t, valueOf(t, tb, r, "cost_number"), "3")
	assertString(t, valueOf(t, tb, r, "cost_currency"), "USD")
	assertDate(t, valueOf(t, tb, r, "cost_date"), time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC))
	assertString(t, valueOf(t, tb, r, "cost_label"), "lot-a")

	tags, ok := types.AsSet(valueOf(t, tb, r, "tags"))
	if !ok {
		t.Fatal("tags is not a set")
	}
	if got := tags.Elements(); len(got) != 2 || got[0] != "q1" || got[1] != "trip" {
		t.Errorf("tags = %v, want [q1 trip]", got)
	}
	links, ok := types.AsSet(valueOf(t, tb, r, "links"))
	if !ok {
		t.Fatal("links is not a set")
	}
	if got := links.Elements(); len(got) != 1 || got[0] != "inv-1" {
		t.Errorf("links = %v, want [inv-1]", got)
	}

	pos, ok := types.AsPosition(valueOf(t, tb, r, "position"))
	if !ok {
		t.Fatal("position is NULL or not a Position")
	}
	if pos.Units.Currency != "STOCK" || pos.Units.Number.Text('f') != "10" {
		t.Errorf("position units = %s %s, want 10 STOCK", pos.Units.Number.Text('f'), pos.Units.Currency)
	}
	if pos.Cost == nil {
		t.Fatal("position lot is nil; booked cost should produce a lot")
	}
	if pos.Cost.Currency != "USD" || pos.Cost.Number.Text('f') != "3" {
		t.Errorf("position lot = %s %s, want 3 USD", pos.Cost.Number.Text('f'), pos.Cost.Currency)
	}

	// weight of a booked posting: units * cost number = 30 USD.
	w, ok := types.AsAmount(valueOf(t, tb, r, "weight"))
	if !ok {
		t.Fatal("weight is NULL or not an Amount")
	}
	if w.Currency != "USD" || w.Number.Text('f') != "30" {
		t.Errorf("weight = %s %s, want 30 USD", w.Number.Text('f'), w.Currency)
	}

	// @ price is per-unit, taken directly.
	pr, ok := types.AsAmount(valueOf(t, tb, r, "price"))
	if !ok {
		t.Fatal("price is NULL or not an Amount")
	}
	if pr.Currency != "USD" || pr.Number.Text('f') != "4" {
		t.Errorf("price = %s %s, want 4 USD", pr.Number.Text('f'), pr.Currency)
	}

	assertPostingMeta(t, valueOf(t, tb, r, "meta"))
}

func assertPostingMeta(t *testing.T, v types.Value) {
	t.Helper()
	d, ok := types.AsDict(v)
	if !ok {
		t.Fatalf("meta is not a Dict: %v", v)
	}
	cases := []struct {
		key string
		typ types.Type
	}{
		{"k_string", types.String},
		{"k_account", types.String},
		{"k_currency", types.String},
		{"k_date", types.Date},
		{"k_tag", types.String},
		{"k_link", types.String},
		{"k_number", types.Decimal},
		{"k_amount", types.Amount},
		{"k_bool", types.Bool},
	}
	if d.Len() != len(cases) {
		t.Fatalf("meta dict has %d keys, want %d", d.Len(), len(cases))
	}
	for _, c := range cases {
		got, ok := d.Get(c.key)
		if !ok {
			t.Errorf("meta missing key %q", c.key)
			continue
		}
		if got.Type() != c.typ {
			t.Errorf("meta[%q] type = %v, want %v", c.key, got.Type(), c.typ)
		}
	}
}

func TestPostingsAutoPostingNulls(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)
	rows := collectRows(tb)
	r := rows[1] // auto-posting (nil Amount)

	assertString(t, valueOf(t, tb, r, "account"), "Assets:Cash")
	// flag falls back to the txn flag '*' (posting flag is 0).
	assertString(t, valueOf(t, tb, r, "flag"), "*")

	for _, col := range []string{"number", "currency", "position", "weight"} {
		if v := valueOf(t, tb, r, col); !v.IsNull() {
			t.Errorf("auto-posting %s = %v, want NULL", col, v.Format())
		}
	}
	// cost_* are NULL on a posting with no cost.
	for _, col := range []string{"cost_number", "cost_currency", "cost_date", "cost_label"} {
		if v := valueOf(t, tb, r, col); !v.IsNull() {
			t.Errorf("auto-posting %s = %v, want NULL", col, v.Format())
		}
	}
	// meta is always a Dict, possibly empty (never NULL).
	mv := valueOf(t, tb, r, "meta")
	d, ok := types.AsDict(mv)
	if !ok {
		t.Fatalf("auto-posting meta is not a Dict: %v", mv)
	}
	if d.Len() != 0 {
		t.Errorf("auto-posting meta has %d keys, want 0", d.Len())
	}
}

func TestPostingsTotalPriceDivision(t *testing.T) {
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txn := &ast.Transaction{
		Date: d,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker",
				Amount:  &ast.Amount{Number: dec(t, "8"), Currency: "STOCK"},
				Price: &ast.PriceAnnotation{
					Amount:  ast.Amount{Number: dec(t, "40"), Currency: "USD"},
					IsTotal: true,
				},
			},
		},
	}
	l := &ast.Ledger{}
	l.Insert(txn)
	tb := table.Postings(l)
	rows := collectRows(tb)

	// @@ total price: 40 USD over 8 units = 5 USD per unit.
	pr, ok := types.AsAmount(valueOf(t, tb, rows[0], "price"))
	if !ok {
		t.Fatal("price is NULL or not an Amount")
	}
	if pr.Currency != "USD" || pr.Number.Text('f') != "5" {
		t.Errorf("@@ per-unit price = %s %s, want 5 USD", pr.Number.Text('f'), pr.Currency)
	}
}

// TestPostingsBalanceIsExecutorSupplied documents that the running balance is
// not a table-level value: the column accessor is a typed-NULL placeholder, and
// the cumulative inventory over the selected rows is computed by the executor
// (see pkg/query/exec and the query-level tests).
func TestPostingsBalanceIsExecutorSupplied(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)

	for i, r := range collectRows(tb) {
		v := valueOf(t, tb, r, table.RunningBalanceColumn)
		if v.Type() != types.Inventory || !v.IsNull() {
			t.Errorf("table-level balance: row %d = %v (type %v, null=%v), want a NULL Inventory placeholder", i, v, v.Type(), v.IsNull())
		}
	}
}

func TestPostingsLazyEarlyExit(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)

	count := 0
	for range tb.Rows() {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("early-exit visited %d rows, want 1", count)
	}

	// Rows() is re-runnable: a fresh iteration yields the full sequence again.
	first := collectRows(tb)
	second := collectRows(tb)
	if len(first) != len(second) || len(second) != 2 {
		t.Fatalf("re-run lengths = %d, %d; want 2, 2", len(first), len(second))
	}
}

func TestPostingsSkipsNonTransactions(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(&ast.Open{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Broker"})
	l.Insert(stockTxn(t))
	l.Insert(&ast.Price{Date: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Commodity: "STOCK", Amount: ast.Amount{Number: dec(t, "5"), Currency: "USD"}})
	tb := table.Postings(l)
	if rows := collectRows(tb); len(rows) != 2 {
		t.Fatalf("got %d posting rows, want 2 (only the transaction's postings)", len(rows))
	}
}

// TestConcurrentReadIsRaceFree asserts Decision 6 at the table layer: many
// goroutines iterating one table over one shared immutable ledger and reading
// every column observe identical accessor output with no locking. Run under
// -race to catch any shared mutable state.
func TestConcurrentReadIsRaceFree(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range tb.Rows() {
				for _, c := range tb.Columns {
					_ = c.Accessor(r).Format()
				}
			}
		}()
	}
	wg.Wait()
}

// TestPostingsOverSyntheticDirective verifies PostingsOver with a hand-built
// iterator factory yielding a zero-Span synthetic transaction: filename and
// lineno return typed NULL, and all other accessors return non-NULL values.
func TestPostingsOverSyntheticDirective(t *testing.T) {
	d := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	synth := &ast.Transaction{
		// zero Span
		Date:      d,
		Flag:      '*',
		Narration: "Opening balance for 'Assets:Cash'",
		Postings: []ast.Posting{
			{
				Account: "Assets:Cash",
				Amount:  &ast.Amount{Number: dec(t, "100"), Currency: "USD"},
			},
		},
	}

	var calls int
	factory := func() iter.Seq2[int, ast.Directive] {
		calls++
		return func(yield func(int, ast.Directive) bool) {
			yield(0, synth)
		}
	}

	tb := table.PostingsOver("scoped-postings", factory)
	if tb.Name != "scoped-postings" {
		t.Errorf("table name = %q, want scoped-postings", tb.Name)
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
	assertString(t, valueOf(t, tb, r, "account"), "Assets:Cash")
	assertDecimal(t, valueOf(t, tb, r, "number"), "100")
	assertString(t, valueOf(t, tb, r, "currency"), "USD")

	// Re-runnable: factory is re-invoked on each Rows() call.
	collectRows(tb)
	if calls != 2 {
		t.Errorf("factory called %d times after two Rows() calls, want 2", calls)
	}
}

// TestPostingsOverSkipsNonTransactions verifies that non-transaction directives
// yielded by the factory are filtered out, matching the behaviour of [Postings].
func TestPostingsOverSkipsNonTransactions(t *testing.T) {
	d := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	txn := &ast.Transaction{
		Date: d,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
		},
	}
	open := &ast.Open{Date: d, Account: "Assets:Cash"}

	tb := table.PostingsOver("scoped-postings", func() iter.Seq2[int, ast.Directive] {
		return func(yield func(int, ast.Directive) bool) {
			yield(0, open)
			yield(1, txn)
		}
	})

	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (Open directive must be skipped)", len(rows))
	}
	assertString(t, valueOf(t, tb, rows[0], "type"), "transaction")
}

// TestAnyMetaNonAliasing verifies that any_meta returns an independent copy:
// mutating the source transaction and posting Meta.Props maps after reading
// any_meta does not alter the previously returned Dict.
func TestAnyMetaNonAliasing(t *testing.T) {
	txn := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{"tkey": {Kind: ast.MetaString, String: "tval"}}},
		Postings: []ast.Posting{
			{
				Account: "Assets:Cash",
				Amount:  &ast.Amount{Number: dec(t, "1"), Currency: "USD"},
				Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"pkey": {Kind: ast.MetaString, String: "pval"}}},
			},
		},
	}
	l := &ast.Ledger{}
	l.Insert(txn)
	tb := table.Postings(l)

	rows := collectRows(tb)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	v := valueOf(t, tb, rows[0], "any_meta")
	d, ok := types.AsDict(v)
	if !ok {
		t.Fatalf("any_meta not a Dict: %v", v)
	}

	// Mutate source maps; the returned Dict must be unaffected.
	txn.Meta.Props["tkey"] = ast.MetaValue{Kind: ast.MetaString, String: "mutated"}
	txn.Postings[0].Meta.Props["pkey"] = ast.MetaValue{Kind: ast.MetaString, String: "mutated"}

	if got, _ := d.Get("tkey"); got.IsNull() {
		t.Fatalf("any_meta lost tkey after source mutation")
	} else if s, _ := types.AsString(got); s != "tval" {
		t.Errorf("any_meta[tkey] = %q after mutation, want original %q", s, "tval")
	}
	if got, _ := d.Get("pkey"); got.IsNull() {
		t.Fatalf("any_meta lost pkey after source mutation")
	} else if s, _ := types.AsString(got); s != "pval" {
		t.Errorf("any_meta[pkey] = %q after mutation, want original %q", s, "pval")
	}
}

func TestColumnLookupCaseInsensitive(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(stockTxn(t))
	tb := table.Postings(l)

	if _, ok := tb.Column("ACCOUNT"); !ok {
		t.Error(`Column("ACCOUNT") not found; lookup should be case-insensitive`)
	}
	if c, ok := tb.Column("Account"); !ok || c.Name != "account" {
		t.Errorf(`Column("Account") = (%q,%v), want ("account",true)`, c.Name, ok)
	}
	if _, ok := tb.Column("nonesuch"); ok {
		t.Error(`Column("nonesuch") found; want not found`)
	}
}
