package csvbase_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// stdTxKeys names the resolved keys for the common single-primary, optional
// counter transaction shape (the shape csvimp and csvsexp assemble).
type stdTxKeys struct {
	date      csvbase.Key[time.Time]
	amount    csvbase.Key[*csvkit.Amount]
	currency  csvbase.Key[string]
	account   csvbase.Key[string]
	counter   csvbase.Key[string]
	payee     csvbase.Key[string]
	narration csvbase.Key[string]
	cost      csvbase.Key[*ast.CostSpec]
}

// stdEmit wires k into the standard primary+counter transaction using the
// construction primitives: a required-amount primary posting, a balancing
// counter via DoubleEntry, and EmitTx as the terminal.
func stdEmit(b *csvbase.Builder, k stdTxKeys) csvbase.EmitFunc {
	primary := csvbase.Posting(b, csvbase.PostingSpec{
		Account: k.account,
		Amount:  csvbase.Amount(b, csvbase.RequireAmount(b, k.amount, ""), k.currency),
		Cost:    k.cost,
	})
	return csvbase.EmitTx(csvbase.Transaction(b, csvbase.TxnSpec{
		Date:      k.date,
		Payee:     k.payee,
		Narration: k.narration,
		Postings:  csvbase.DoubleEntry(b, primary, k.counter),
	}))
}

// ---------------------------------------------------------------------------
// (a) Full happy path: counter, payee, template narration, RowHash stamping
// ---------------------------------------------------------------------------

func TestExample_HappyPathWithRowHash(t *testing.T) {
	const csv = `Date,Amount,Desc,Cat
2024-03-01,100.00,Amazon,food
2024-03-02,50.00,Netflix,entertainment
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})
	accKey := csvbase.Require(b,
		csvbase.Coalesce(b,
			csvbase.Hint(b, "account"),
			csvbase.MapValue(b,
				csvbase.JoinKeys(b, ":", csvbase.Columns(b, "Cat")...),
				map[string]string{"food": "Expenses:Food", "entertainment": "Expenses:Entertainment"},
				csvkit.Strict, csvbase.DiagUnmappedAccount),
		),
		csvbase.DiagMissingAccount)
	// Empty map: every Cat misses (warning), Coalesce falls through to the Const default.
	ctrKey := csvbase.Coalesce(b,
		csvbase.DiagAsWarning(b,
			csvbase.MapValue(b,
				csvbase.JoinKeys(b, "", csvbase.Columns(b, "Cat")...),
				map[string]string{}, csvkit.Strict, ""),
			csvbase.DiagUnmappedCounterAccount),
		csvbase.Const(b, "Assets:Bank"))
	payKey := csvbase.JoinKeys(b, " ", csvbase.Columns(b, "Desc")...)
	tmpl, _ := csvkit.CompileTemplate("{{.Desc}}")
	narrKey := csvbase.Template(b, tmpl, csvbase.Row(b))

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:      dateKey,
		amount:    amtKey,
		currency:  csvbase.Const(b, "USD"),
		account:   accKey,
		counter:   ctrKey,
		payee:     payKey,
		narration: narrKey,
	}))

	d, err := csvbase.New("happy", csvbase.Config{
		Mapper:  pipeline,
		RowHash: &csvbase.RowHash{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if tx.Payee != "Amazon" {
		t.Errorf("payee = %q, want %q", tx.Payee, "Amazon")
	}
	if tx.Narration != "Amazon" {
		t.Errorf("narration = %q, want %q", tx.Narration, "Amazon")
	}
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if string(tx.Postings[0].Account) != "Expenses:Food" {
		t.Errorf("primary account = %q", tx.Postings[0].Account)
	}
	// RowHash stamp
	if tx.Meta.Props == nil || tx.Meta.Props[csvbase.DefaultRowHashKey].String == "" {
		t.Errorf("RowHash metadata not stamped; meta = %v", tx.Meta)
	}
}

// ---------------------------------------------------------------------------
// (b) Split groups feeding payee/narration
// ---------------------------------------------------------------------------

func TestExample_SplitGroups(t *testing.T) {
	const csv = `Date,Amount,Desc
2024-03-01,10.00,Amazon / Books
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})

	groups := csvbase.SplitColumns(b, csvbase.Column(b, "Desc"),
		regexp.MustCompile(`(?P<payee>[^/]+) / (?P<memo>.+)`))
	payKey := csvbase.JoinKeys(b, "", groups["payee"])
	narrKey := csvbase.JoinKeys(b, " ", groups["memo"])

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:      dateKey,
		amount:    amtKey,
		currency:  csvbase.Const(b, "USD"),
		account:   csvbase.Const(b, "Expenses:Misc"),
		payee:     payKey,
		narration: narrKey,
	}))
	d, err := csvbase.New("split", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	tx := out.Directives[0].(*ast.Transaction)
	if strings.TrimSpace(tx.Payee) != "Amazon" {
		t.Errorf("payee = %q, want %q", tx.Payee, "Amazon")
	}
	if strings.TrimSpace(tx.Narration) != "Books" {
		t.Errorf("narration = %q, want %q", tx.Narration, "Books")
	}
}

// ---------------------------------------------------------------------------
// (c) header_match banner (lazy Identify)
// ---------------------------------------------------------------------------

func TestExample_HeaderMatchBanner(t *testing.T) {
	// File has a variable banner before the header row.
	const csv = "Bank Statement\nAccount: 12345\nDate,Amount,Desc\n2024-03-01,10.00,Coffee\n"

	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Expenses:Misc"),
	}))

	d, err := csvbase.New("banner", csvbase.Config{
		Reader: csvkit.Reader{
			HeaderMatch: func(row []string) bool {
				for _, c := range row {
					if strings.TrimSpace(c) == "Date" {
						return true
					}
				}
				return false
			},
		},
		Mapper: pipeline,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Identify must return true when the header is found past the banner.
	if !d.Identify(context.Background(), inputStr("/bank.csv", csv)) {
		t.Error("Identify returned false, want true")
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
}

// ---------------------------------------------------------------------------
// (d) Headerless input
// ---------------------------------------------------------------------------

func TestExample_Headerless(t *testing.T) {
	// No header row; columns are addressed by position.
	const csv = "2024-03-01,25.00,Coffee\n"

	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Expenses:Misc"),
	}))

	d, err := csvbase.New("headerless", csvbase.Config{
		Reader: csvkit.Reader{
			Columns: map[string]int{"Date": 0, "Amount": 1, "Desc": 2},
		},
		Mapper: pipeline,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if tx.Date != time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("date = %v", tx.Date)
	}
}

// ---------------------------------------------------------------------------
// (e) Exclude filter dropping a totals row
// ---------------------------------------------------------------------------

func TestExample_ExcludeFilter(t *testing.T) {
	const csv = `Date,Amount,Type
2024-03-01,100.00,Debit
2024-03-31,100.00,Total
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Expenses:Misc"),
	}))

	d, err := csvbase.New("exclude", csvbase.Config{
		Mapper:  pipeline,
		Filters: []csvkit.RowFilter{csvkit.ExcludeMatching("Type", regexp.MustCompile(`^Total$`))},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Errorf("got %d directives, want 1 (Total row excluded)", len(out.Directives))
	}
}

// ---------------------------------------------------------------------------
// (f) Cost with counter elision — AddStep extension pattern
// ---------------------------------------------------------------------------

func TestExample_CostElision(t *testing.T) {
	const csv = `Date,Units,CostNum,CostCur
2024-03-01,10,150.00,USD
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Units"), csvbase.ParseAmountConfig{})
	costNumCol := csvbase.Column(b, "CostNum")
	costCurCol := csvbase.Column(b, "CostCur")

	// Cost assembly is tightly coupled to ast.CostSpec shape; use AddStep.
	costKey := csvbase.AddStep(b, func(c *csvbase.MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		rawNum, _ := csvbase.Value(c, costNumCol)
		rawNum = strings.TrimSpace(rawNum)
		if rawNum == "" {
			return nil, nil, nil
		}
		num, _, err := csvkit.ParseNumber(rawNum, csvkit.NumberFormat{})
		if err != nil {
			info := c.Info()
			diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line,
				"cannot parse cost number")
			return nil, &diag, nil
		}
		cur := ""
		if v, _ := csvbase.Value(c, costCurCol); strings.TrimSpace(v) != "" {
			cur = strings.TrimSpace(v)
		}
		if cur == "" {
			info := c.Info()
			diag := csvbase.ErrorDiag(csvbase.DiagBadCost, info.Path, info.Line, "cost currency missing")
			return nil, &diag, nil
		}
		return &ast.CostSpec{PerUnit: &num, Currency: cur}, nil, nil
	})

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "STOCK"),
		account:  csvbase.Const(b, "Assets:Brokerage"),
		counter:  csvbase.Const(b, "Assets:Cash"),
		cost:     costKey,
	}))

	d, err := csvbase.New("cost", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/brokerage.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	// counter posting must have no amount (elided cash leg)
	if tx.Postings[1].Amount != nil {
		t.Errorf("counter posting amount = %v, want nil (cost-elided)", tx.Postings[1].Amount)
	}
}

// ---------------------------------------------------------------------------
// (g) Account map strict: unmapped row dropped with DiagUnmappedAccount
// ---------------------------------------------------------------------------

func TestExample_AccountMapStrict_Unmapped(t *testing.T) {
	const csv = `Date,Amount,Cat
2024-03-01,100.00,food
2024-03-02,50.00,unknown_category
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})
	// No Default: strict miss on Cat → DiagUnmappedAccount → Posting drops the row.
	accKey := csvbase.Require(b,
		csvbase.MapValue(b,
			csvbase.JoinKeys(b, ":", csvbase.Columns(b, "Cat")...),
			map[string]string{"food": "Expenses:Food"},
			csvkit.Strict, csvbase.DiagUnmappedAccount),
		csvbase.DiagMissingAccount)

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  accKey,
	}))

	d, err := csvbase.New("acct-strict", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// second row should be dropped
	if len(out.Directives) != 1 {
		t.Errorf("got %d directives, want 1 (unmapped row dropped)", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != csvbase.DiagUnmappedAccount {
		t.Errorf("diagnostics = %v, want one DiagUnmappedAccount", out.Diagnostics)
	}
}

// ---------------------------------------------------------------------------
// (h) Counter map strict: unmapped row kept with DiagUnmappedCounterAccount
// ---------------------------------------------------------------------------

func TestExample_CounterMapStrict_Unmapped(t *testing.T) {
	const csv = `Date,Amount,Type
2024-03-01,100.00,income
2024-03-02,50.00,mystery
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})
	// strict: "mystery" not in map → Warning soft-fail → DoubleEntry keeps row with single posting.
	ctrKey := csvbase.DiagAsWarning(b,
		csvbase.MapValue(b,
			csvbase.JoinKeys(b, "", csvbase.Columns(b, "Type")...),
			map[string]string{"income": "Income:Salary"},
			csvkit.Strict, ""),
		csvbase.DiagUnmappedCounterAccount)

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
		counter:  ctrKey,
	}))

	d, err := csvbase.New("ctr-strict", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// both rows kept
	if len(out.Directives) != 2 {
		t.Errorf("got %d directives, want 2 (both rows kept)", len(out.Directives))
	}
	// one warning diagnostic
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != csvbase.DiagUnmappedCounterAccount {
		t.Errorf("diagnostics = %v, want one DiagUnmappedCounterAccount warning", out.Diagnostics)
	}
	if out.Diagnostics[0].Severity != ast.Warning {
		t.Errorf("severity = %v, want Warning", out.Diagnostics[0].Severity)
	}
	// second transaction has single posting
	tx2 := out.Directives[1].(*ast.Transaction)
	if len(tx2.Postings) != 1 {
		t.Errorf("mystery tx postings = %d, want 1", len(tx2.Postings))
	}
}

// ---------------------------------------------------------------------------
// (i) Currency from amount suffix
// ---------------------------------------------------------------------------

func TestExample_CurrencyFromAmountSuffix(t *testing.T) {
	// CSV has no Cur column (blank), so Coalesce falls through to CurrencyHint ("JPY" from suffix).
	const csv = `Date,Amount,Cur
2024-03-01,1000 JPY,
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{SplitCurrency: true})
	// Explicit Cur column wins when non-blank; hint from amount suffix is the fallback; USD is the last resort.
	curKey := csvbase.Require(b,
		csvbase.Coalesce(b,
			csvbase.MapValue(b, csvbase.Column(b, "Cur"), nil, csvkit.Verbatim, ""),
			csvbase.CurrencyHint(b, amtKey),
			csvbase.Const(b, "USD"),
		),
		csvbase.DiagMissingCurrency)

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: curKey,
		account:  csvbase.Const(b, "Expenses:Misc"),
	}))

	d, err := csvbase.New("cur-suffix", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if tx.Postings[0].Amount.Currency != "JPY" {
		t.Errorf("currency = %q, want JPY", tx.Postings[0].Amount.Currency)
	}
}

// ---------------------------------------------------------------------------
// (j) Finalize hook
// ---------------------------------------------------------------------------

func TestExample_FinalizeHook(t *testing.T) {
	const csv = `Date,Amount
2024-03-01,50.00
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Expenses:Misc"),
	}))

	var finalizeCalled bool
	d, err := csvbase.New("finalize", csvbase.Config{
		Mapper: pipeline,
		Finalize: func(ctx context.Context, dirs []ast.Directive, diags []ast.Diagnostic) ([]ast.Directive, []ast.Diagnostic, error) {
			finalizeCalled = true
			// add a note directive in finalize
			return append(dirs, &ast.Note{Comment: "finalized"}), diags, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !finalizeCalled {
		t.Error("Finalize was not called")
	}
	if len(out.Directives) != 2 {
		t.Errorf("got %d directives, want 2 (1 tx + 1 note from finalize)", len(out.Directives))
	}
}

// ---------------------------------------------------------------------------
// (a continued) Verify RowHash produces stable, distinct hashes across rows
// ---------------------------------------------------------------------------

func TestExample_RowHashStability(t *testing.T) {
	const csv = `Date,Amount,Desc
2024-03-01,100.00,Coffee
2024-03-01,100.00,Coffee
2024-03-01,200.00,Lunch
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Expenses:Misc"),
	}))

	d, err := csvbase.New("hash-stable", csvbase.Config{
		Mapper:  pipeline,
		RowHash: &csvbase.RowHash{KeyFunc: csvbase.StaticRowHashKey("import-hash")},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 3 {
		t.Fatalf("got %d directives, want 3", len(out.Directives))
	}

	hash := func(i int) string {
		return out.Directives[i].(*ast.Transaction).Meta.Props["import-hash"].String
	}
	// Rows 0 and 1 are identical => same hash.
	if hash(0) != hash(1) {
		t.Errorf("identical rows have different hashes: %q vs %q", hash(0), hash(1))
	}
	// Row 2 has different amount => different hash.
	if hash(0) == hash(2) {
		t.Errorf("different rows have same hash: %q", hash(0))
	}
	// All hashes non-empty.
	for i := range 3 {
		if hash(i) == "" {
			t.Errorf("row %d hash is empty", i)
		}
	}
}

func inputStrHints(path, body string, hints map[string]string) importer.Input {
	in := inputStr(path, body)
	in.Hints = hints
	return in
}

// ---------------------------------------------------------------------------
// Bonus: account hint via Hints map (Identify + Extract with hint override)
// ---------------------------------------------------------------------------

func TestExample_AccountHintOverride(t *testing.T) {
	const csv = `Date,Amount,Cat
2024-03-01,50.00,food
`
	b := csvbase.NewBuilder()
	dateKey := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})
	accKey := csvbase.Require(b,
		csvbase.Coalesce(b,
			csvbase.Hint(b, "account"),
			csvbase.MapValue(b,
				csvbase.JoinKeys(b, ":", csvbase.Columns(b, "Cat")...),
				map[string]string{"food": "Expenses:Food"},
				csvkit.Strict, csvbase.DiagUnmappedAccount),
		),
		csvbase.DiagMissingAccount)

	pipeline := b.Emit(stdEmit(b, stdTxKeys{
		date:     dateKey,
		amount:   amtKey,
		currency: csvbase.Const(b, "USD"),
		account:  accKey,
	}))

	d, err := csvbase.New("hint", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(),
		inputStrHints("/bank.csv", csv, map[string]string{"account": "Assets:Override"}))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", out.Diagnostics)
	}
	tx := out.Directives[0].(*ast.Transaction)
	if string(tx.Postings[0].Account) != "Assets:Override" {
		t.Errorf("account = %q, want Assets:Override", tx.Postings[0].Account)
	}
}

// printDirectives renders an Extract result deterministically for the runnable
// examples below: one line per transaction (date, flag, payee, narration)
// followed by its postings, then any diagnostic codes.
func printDirectives(out importer.Output) {
	for _, d := range out.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			fmt.Printf("%T\n", d)
			continue
		}
		fmt.Printf("%s %c %q %q\n", tx.Date.Format("2006-01-02"), tx.Flag, tx.Payee, tx.Narration)
		for _, p := range tx.Postings {
			if p.Amount != nil {
				fmt.Printf("  %s  %s %s\n", p.Account, p.Amount.Number.String(), p.Amount.Currency)
			} else {
				fmt.Printf("  %s\n", p.Account)
			}
		}
	}
	for _, dg := range out.Diagnostics {
		fmt.Printf("! %s\n", dg.Code)
	}
}

// Example shows the simplest wiring: parse a date, parse one amount column,
// and post it against a fixed account. Each field is a Key produced by a step
// constructor; [Posting] builds the leg, [Transaction] assembles the directive,
// and [EmitTx] is the terminal the Driver invokes per row.
func Example() {
	const csv = `Date,Description,Amount
2024-01-05,Coffee,4.50
`
	b := csvbase.NewBuilder()
	date := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amount := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})
	narration := csvbase.JoinKeys(b, " ", csvbase.Columns(b, "Description")...)
	primary := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Cash"),
		Amount:  csvbase.Amount(b, amount, csvbase.Const(b, "USD")),
	})
	pipeline := b.Emit(csvbase.EmitTx(csvbase.Transaction(b, csvbase.TxnSpec{
		Date:      date,
		Narration: narration,
		Postings:  csvbase.Postings(b, primary),
	})))

	d, _ := csvbase.New("simple", csvbase.Config{Mapper: pipeline})
	out, _ := d.Extract(context.Background(), inputStr("/coffee.csv", csv))
	printDirectives(out)

	// Output:
	// 2024-01-05 * "" "Coffee"
	//   Assets:Cash  4.50 USD
}

// Example_debitCredit shows a typical bank statement: a debit and a credit
// column net into a single signed amount, the bank account is fixed, and the
// counter (category) account comes from a lookup map. DoubleEntry adds the
// balancing posting with the negated amount.
func Example_debitCredit() {
	const csv = `Date,Payee,Debit,Credit,Category
2024-02-10,Acme Cafe,12.00,,Coffee
2024-02-11,Paycheck,,3000.00,Salary
2024-02-12,Rent Co,1500.00,,Rent
`
	b := csvbase.NewBuilder()
	date := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amount := csvbase.AddAmounts(b,
		csvbase.ParseAmount(b, csvbase.Column(b, "Credit"), csvbase.ParseAmountConfig{}),
		csvbase.NegateAmount(b, csvbase.ParseAmount(b, csvbase.Column(b, "Debit"), csvbase.ParseAmountConfig{})),
		"")
	payee := csvbase.JoinKeys(b, " ", csvbase.Columns(b, "Payee")...)
	counterMap := map[string]string{
		"Coffee": "Expenses:Food",
		"Salary": "Income:Salary",
		"Rent":   "Expenses:Rent",
	}
	// Const("") here means "no counter posting" when the map misses (DoubleEntry skips empty counter).
	counter := csvbase.Coalesce(b,
		csvbase.DiagAsWarning(b,
			csvbase.MapValue(b,
				csvbase.JoinKeys(b, "", csvbase.Columns(b, "Category")...),
				counterMap, csvkit.Strict, ""),
			csvbase.DiagUnmappedCounterAccount),
		csvbase.Const(b, ""))
	primary := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Checking"),
		Amount:  csvbase.Amount(b, amount, csvbase.Const(b, "USD")),
	})
	pipeline := b.Emit(csvbase.EmitTx(csvbase.Transaction(b, csvbase.TxnSpec{
		Date:     date,
		Payee:    payee,
		Postings: csvbase.DoubleEntry(b, primary, counter),
	})))

	d, _ := csvbase.New("bank", csvbase.Config{Mapper: pipeline})
	out, _ := d.Extract(context.Background(), inputStr("/bank.csv", csv))
	printDirectives(out)

	// Output:
	// 2024-02-10 * "Acme Cafe" ""
	//   Assets:Checking  -12.00 USD
	//   Expenses:Food  12.00 USD
	// 2024-02-11 * "Paycheck" ""
	//   Assets:Checking  3000.00 USD
	//   Income:Salary  -3000.00 USD
	// 2024-02-12 * "Rent Co" ""
	//   Assets:Checking  -1500.00 USD
	//   Expenses:Rent  1500.00 USD
}

// Example_splitAndFilter shows several advanced features together: a banner
// line skipped before the header, a Detail column split by regular expression
// into payee and memo groups, the currency taken from a suffix on the amount
// cell, and a totals row dropped by an exclude filter.
func Example_splitAndFilter() {
	const csv = `# Bank export
Date,Detail,Amount
2024-04-01,Amazon|Books order,1500 JPY
2024-04-02,Starbucks|Latte,600 JPY
Total,,2100 JPY
`
	b := csvbase.NewBuilder()
	date := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	groups := csvbase.SplitColumns(b, csvbase.Column(b, "Detail"),
		regexp.MustCompile(`^(?P<payee>[^|]+)\|(?P<memo>.+)$`))
	amount := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{SplitCurrency: true})
	currency := csvbase.Require(b,
		csvbase.Coalesce(b, csvbase.CurrencyHint(b, amount)),
		csvbase.DiagMissingCurrency)
	primary := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Cash"),
		Amount:  csvbase.Amount(b, amount, currency),
	})
	pipeline := b.Emit(csvbase.EmitTx(csvbase.Transaction(b, csvbase.TxnSpec{
		Date:      date,
		Payee:     csvbase.JoinKeys(b, "", groups["payee"]),
		Narration: csvbase.JoinKeys(b, " ", groups["memo"]),
		Postings:  csvbase.Postings(b, primary),
	})))

	d, _ := csvbase.New("statement", csvbase.Config{
		Reader:  csvkit.Reader{SkipLines: 1},
		Mapper:  pipeline,
		Filters: []csvkit.RowFilter{csvkit.ExcludeMatching("Date", regexp.MustCompile(`^Total`))},
	})
	out, _ := d.Extract(context.Background(), inputStr("/statement.csv", csv))
	printDirectives(out)

	// Output:
	// 2024-04-01 * "Amazon" "Books order"
	//   Assets:Cash  1500 JPY
	// 2024-04-02 * "Starbucks" "Latte"
	//   Assets:Cash  600 JPY
}

// Example_templateWithSplit shows Template over Merge(Row, overlay): a split
// group's value is made available to the template under a custom name, so the
// template can reference it without that name being a raw header column.
func Example_templateWithSplit() {
	const csv = `Date,Amount,Detail
2024-05-01,42.00,Amazon|Order #999
`
	b := csvbase.NewBuilder()
	date := csvbase.ParseDate(b, csvbase.Column(b, "Date"), "2006-01-02", "")
	amount := csvbase.ParseAmount(b, csvbase.Column(b, "Amount"), csvbase.ParseAmountConfig{})
	groups := csvbase.SplitColumns(b, csvbase.Column(b, "Detail"),
		regexp.MustCompile(`^(?P<vendor>[^|]+)\|(?P<ref>.+)$`))

	tmpl, _ := csvkit.CompileTemplate("{{.vendor}} — {{.ref}}")
	narration := csvbase.Template(b, tmpl,
		csvbase.Merge(b, csvbase.Row(b), map[string]csvbase.Key[string]{
			"vendor": groups["vendor"],
			"ref":    groups["ref"],
		}))

	primary := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Cash"),
		Amount:  csvbase.Amount(b, amount, csvbase.Const(b, "USD")),
	})
	pipeline := b.Emit(csvbase.EmitTx(csvbase.Transaction(b, csvbase.TxnSpec{
		Date:      date,
		Narration: narration,
		Postings:  csvbase.Postings(b, primary),
	})))

	d, _ := csvbase.New("tmpl-split", csvbase.Config{Mapper: pipeline})
	out, _ := d.Extract(context.Background(), inputStr("/orders.csv", csv))
	printDirectives(out)

	// Output:
	// 2024-05-01 * "" "Amazon — Order #999"
	//   Assets:Cash  42.00 USD
}

// Example_lowLevelMapper shows that the framework layers are optional: the
// Driver can run a plain MapperFunc with no pipeline at all, extracting cells by
// hand and reusing csvkit building blocks (here ParseNumber) directly.
func Example_lowLevelMapper() {
	const csv = `Date,Memo,Amount
2024-05-01,Lunch,9.00
`
	mapper := csvbase.MapperFunc(
		[]string{"Date", "Memo", "Amount"},
		func(_ context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			get := func(col string) string { return rec.Fields[rec.Index[col]] }
			date, err := time.Parse("2006-01-02", get("Date"))
			if err != nil {
				return nil, []ast.Diagnostic{csvbase.ErrorDiag(csvbase.DiagBadDate, rec.Path, rec.Line, "bad date")}, nil
			}
			num, _, _ := csvkit.ParseNumber(get("Amount"), csvkit.NumberFormat{})
			tx := &ast.Transaction{
				Date:      date,
				Flag:      '*',
				Narration: get("Memo"),
				Postings: []ast.Posting{{
					Account: "Assets:Cash",
					Amount:  &ast.Amount{Number: num, Currency: "USD"},
				}},
			}
			return []ast.Directive{tx}, nil, nil
		})

	d, _ := csvbase.New("manual", csvbase.Config{Mapper: mapper})
	out, _ := d.Extract(context.Background(), inputStr("/manual.csv", csv))
	printDirectives(out)

	// Output:
	// 2024-05-01 * "" "Lunch"
	//   Assets:Cash  9.00 USD
}
