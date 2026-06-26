package csvbase_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func buildRec() csvbase.RowContext {
	return csvbase.RowContext{Fields: []string{}, Index: map[string]int{}, Path: "/f.csv", Line: 1}
}

func dateKey(b *csvbase.Builder, t time.Time) csvbase.Key[time.Time] {
	return csvbase.AddStep(b, func(*csvbase.MappingState) (time.Time, *ast.Diagnostic, error) {
		return t, nil, nil
	})
}

func amtKey(b *csvbase.Builder, num string) csvbase.Key[*csvkit.Amount] {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), num)
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		return &csvkit.Amount{Number: *n}, nil, nil
	})
}

func amtHintKey(b *csvbase.Builder, num, hint string) csvbase.Key[*csvkit.Amount] {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), num)
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		return &csvkit.Amount{Number: *n, CurrencyHint: hint}, nil, nil
	})
}

func nilAmtKey(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		return nil, nil, nil
	})
}

func failStrKey(b *csvbase.Builder, code string) csvbase.Key[string] {
	return csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag(code, "/f.csv", 1, "fail")
		return "", &d, nil
	})
}

func warnStrKey(b *csvbase.Builder, code string) csvbase.Key[string] {
	return csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
		d := csvbase.WarnDiag(code, "/f.csv", 1, "warn")
		return "", &d, nil
	})
}

func costSpecKey(b *csvbase.Builder) csvbase.Key[*ast.CostSpec] {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "100")
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		return &ast.CostSpec{PerUnit: n, Currency: "USD"}, nil, nil
	})
}

func nilCostKey(b *csvbase.Builder) csvbase.Key[*ast.CostSpec] {
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		return nil, nil, nil
	})
}

func runEmit(t *testing.T, b *csvbase.Builder, tx csvbase.Key[*ast.Transaction]) ([]ast.Directive, []ast.Diagnostic) {
	t.Helper()
	p := b.Emit(csvbase.EmitTx(tx))
	dirs, diags, err := p.Map(context.Background(), buildRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	return dirs, diags
}

func decEq(d apd.Decimal, want string) bool {
	w, _, _ := apd.BaseContext.SetString(new(apd.Decimal), want)
	return d.Cmp(w) == 0
}

var someDate = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// Transaction with three postings, including an auto posting
// ---------------------------------------------------------------------------

func TestTransaction_ThreePostingsWithAuto(t *testing.T) {
	b := csvbase.NewBuilder()
	p1 := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "100"), csvbase.Const(b, "USD")),
	})
	p2 := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Expenses:Food"),
		Amount:  csvbase.Amount(b, amtKey(b, "-60"), csvbase.Const(b, "USD")),
	})
	p3 := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Expenses:Tax"), // auto: no Amount
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{
		Date:     dateKey(b, someDate),
		Postings: csvbase.Postings(b, p1, p2, p3),
	})

	dirs, diags := runEmit(t, b, tx)
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d directives, want 1", len(dirs))
	}
	got := dirs[0].(*ast.Transaction)
	if got.Flag != '*' {
		t.Errorf("flag = %q, want '*'", got.Flag)
	}
	if len(got.Postings) != 3 {
		t.Fatalf("got %d postings, want 3", len(got.Postings))
	}
	if got.Postings[2].Amount != nil {
		t.Errorf("third posting amount = %v, want nil (auto posting)", got.Postings[2].Amount)
	}
	if !decEq(got.Postings[0].Amount.Number, "100") || got.Postings[0].Amount.Currency != "USD" {
		t.Errorf("p0 = %v", got.Postings[0].Amount)
	}
	if !decEq(got.Postings[1].Amount.Number, "-60") {
		t.Errorf("p1 number = %v, want -60", got.Postings[1].Amount.Number)
	}
}

// ---------------------------------------------------------------------------
// Posting: nil cost must leave Cost as a true nil interface (not a typed nil)
// ---------------------------------------------------------------------------

func TestPosting_NilCostLeavesInterfaceNil(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "1"), csvbase.Const(b, "USD")),
		Cost:    nilCostKey(b),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	got := dirs[0].(*ast.Transaction)
	if got.Postings[0].Cost != nil {
		t.Errorf("Cost = %v, want a nil interface", got.Postings[0].Cost)
	}
}

func TestPosting_WithCost(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Brokerage"),
		Amount:  csvbase.Amount(b, amtKey(b, "10"), csvbase.Const(b, "STOCK")),
		Cost:    costSpecKey(b),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	got := dirs[0].(*ast.Transaction)
	if got.Postings[0].Cost == nil {
		t.Fatal("Cost = nil, want a CostSpec")
	}
}

// ---------------------------------------------------------------------------
// Posting metadata + flag
// ---------------------------------------------------------------------------

func TestPosting_MetaAndFlag(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "1"), csvbase.Const(b, "USD")),
		Flag:    '!',
		Meta: []csvbase.MetaField{
			{Name: "ref", Value: csvbase.Const(b, "R1")},
			{Name: "blank", Value: csvbase.Const(b, "")}, // skipped
			{Name: "bad", Value: failStrKey(b, "x")},     // skipped
		},
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	got := dirs[0].(*ast.Transaction).Postings[0]
	if got.Flag != '!' {
		t.Errorf("posting flag = %q, want '!'", got.Flag)
	}
	if got.Meta.Props["ref"].String != "R1" {
		t.Errorf("meta ref = %q, want R1", got.Meta.Props["ref"].String)
	}
	if _, ok := got.Meta.Props["blank"]; ok {
		t.Error("blank meta should be skipped")
	}
	if _, ok := got.Meta.Props["bad"]; ok {
		t.Error("soft-failed meta should be skipped")
	}
}

func TestPosting_BlankAccountDrops(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, ""),
		Amount:  csvbase.Amount(b, amtKey(b, "1"), csvbase.Const(b, "USD")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingAccount {
		t.Errorf("diags = %v, want DiagMissingAccount", diags)
	}
}

func TestPosting_PanicOnZeroAccount(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero Account key")
		}
	}()
	b := csvbase.NewBuilder()
	csvbase.Posting(b, csvbase.PostingSpec{})
}

// ---------------------------------------------------------------------------
// Amount: currency resolution
// ---------------------------------------------------------------------------

func TestAmount_ExplicitCurrencyWins(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtHintKey(b, "5", "JPY"), csvbase.Const(b, "USD")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	if got := dirs[0].(*ast.Transaction).Postings[0].Amount.Currency; got != "USD" {
		t.Errorf("currency = %q, want USD (explicit wins over hint)", got)
	}
}

func TestAmount_HintFallback(t *testing.T) {
	b := csvbase.NewBuilder()
	var zeroCur csvbase.Key[string]
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtHintKey(b, "5", "JPY"), zeroCur),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	if got := dirs[0].(*ast.Transaction).Postings[0].Amount.Currency; got != "JPY" {
		t.Errorf("currency = %q, want JPY (hint fallback)", got)
	}
}

func TestAmount_MissingCurrencyDrops(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "5"), csvbase.Const(b, "")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingCurrency {
		t.Errorf("diags = %v, want DiagMissingCurrency", diags)
	}
}

func TestAmount_NilYieldsAutoPosting(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, nilAmtKey(b), csvbase.Const(b, "USD")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	if got := dirs[0].(*ast.Transaction).Postings[0].Amount; got != nil {
		t.Errorf("amount = %v, want nil (auto posting)", got)
	}
}

// ---------------------------------------------------------------------------
// RequireAmount
// ---------------------------------------------------------------------------

func TestRequireAmount_NilDropsWithDefaultCode(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, csvbase.RequireAmount(b, nilAmtKey(b), ""), csvbase.Const(b, "USD")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagAllBlankAmount {
		t.Errorf("diags = %v, want DiagAllBlankAmount", diags)
	}
}

// ---------------------------------------------------------------------------
// Postings gathering
// ---------------------------------------------------------------------------

func TestPostings_MemberSoftFailDropsTxn(t *testing.T) {
	b := csvbase.NewBuilder()
	good := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "1"), csvbase.Const(b, "USD")),
	})
	bad := csvbase.Posting(b, csvbase.PostingSpec{Account: csvbase.Const(b, "")}) // DiagMissingAccount
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, good, bad)})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0 (member soft-fail drops txn)", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingAccount {
		t.Errorf("diags = %v, want DiagMissingAccount", diags)
	}
}

func TestPostings_ZeroKeySkipped(t *testing.T) {
	b := csvbase.NewBuilder()
	var zero csvbase.Key[ast.Posting]
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "1"), csvbase.Const(b, "USD")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, zero, p, zero)})
	dirs, _ := runEmit(t, b, tx)
	if n := len(dirs[0].(*ast.Transaction).Postings); n != 1 {
		t.Errorf("got %d postings, want 1 (zero keys skipped)", n)
	}
}

func TestTransaction_EmptyPostingsDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b)})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagNoPostings {
		t.Errorf("diags = %v, want DiagNoPostings", diags)
	}
}

// ---------------------------------------------------------------------------
// Transaction-level tags / links / meta
// ---------------------------------------------------------------------------

func TestTransaction_TagsLinksMeta(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Bank"),
		Amount:  csvbase.Amount(b, amtKey(b, "1"), csvbase.Const(b, "USD")),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{
		Date:     dateKey(b, someDate),
		Payee:    csvbase.Const(b, "ACME"),
		Tags:     csvbase.StringList(b, csvbase.Const(b, "trip"), csvbase.Const(b, ""), failStrKey(b, "x")),
		Links:    csvbase.StringList(b, csvbase.Const(b, "inv-1")),
		Meta:     csvbase.Meta(b, csvbase.MetaField{Name: "ref", Value: csvbase.Const(b, "R9")}),
		Postings: csvbase.Postings(b, p),
	})
	dirs, _ := runEmit(t, b, tx)
	got := dirs[0].(*ast.Transaction)
	if got.Payee != "ACME" {
		t.Errorf("payee = %q", got.Payee)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "trip" {
		t.Errorf("tags = %v, want [trip] (blank and soft-fail skipped)", got.Tags)
	}
	if len(got.Links) != 1 || got.Links[0] != "inv-1" {
		t.Errorf("links = %v, want [inv-1]", got.Links)
	}
	if got.Meta.Props["ref"].String != "R9" {
		t.Errorf("meta ref = %q, want R9", got.Meta.Props["ref"].String)
	}
}

// ---------------------------------------------------------------------------
// Transaction / EmitTx panics and skip
// ---------------------------------------------------------------------------

func TestTransaction_PanicOnZeroDate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero Date key")
		}
	}()
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{Account: csvbase.Const(b, "Assets:Bank")})
	csvbase.Transaction(b, csvbase.TxnSpec{Postings: csvbase.Postings(b, p)})
}

func TestTransaction_PanicOnZeroPostings(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero Postings key")
		}
	}()
	b := csvbase.NewBuilder()
	csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate)})
}

func TestEmitTx_NilSkips(t *testing.T) {
	b := csvbase.NewBuilder()
	nilTx := csvbase.AddStep(b, func(*csvbase.MappingState) (*ast.Transaction, *ast.Diagnostic, error) {
		return nil, nil, nil
	})
	dirs, diags := runEmit(t, b, nilTx)
	if len(dirs) != 0 || len(diags) != 0 {
		t.Errorf("got dirs=%v diags=%v, want both empty (nil tx skips)", dirs, diags)
	}
}

func TestEmitTx_PanicOnZeroKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero tx key")
		}
	}()
	var zero csvbase.Key[*ast.Transaction]
	csvbase.EmitTx(zero)
}

// ---------------------------------------------------------------------------
// DoubleEntry (counter convenience)
// ---------------------------------------------------------------------------

func primaryPosting(b *csvbase.Builder, account, num, cur string) csvbase.Key[ast.Posting] {
	return csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, account),
		Amount:  csvbase.Amount(b, amtKey(b, num), csvbase.Const(b, cur)),
	})
}

func TestDoubleEntry_NegatesCounter(t *testing.T) {
	b := csvbase.NewBuilder()
	postings := csvbase.DoubleEntry(b, primaryPosting(b, "Assets:Bank", "100", "USD"), csvbase.Const(b, "Expenses:Food"))
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: postings})
	dirs, diags := runEmit(t, b, tx)
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	got := dirs[0].(*ast.Transaction)
	if len(got.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(got.Postings))
	}
	if string(got.Postings[1].Account) != "Expenses:Food" {
		t.Errorf("counter account = %q", got.Postings[1].Account)
	}
	if !decEq(got.Postings[1].Amount.Number, "-100") || got.Postings[1].Amount.Currency != "USD" {
		t.Errorf("counter amount = %v, want -100 USD", got.Postings[1].Amount)
	}
}

func TestDoubleEntry_CostElidesCounterAmount(t *testing.T) {
	b := csvbase.NewBuilder()
	primary := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Brokerage"),
		Amount:  csvbase.Amount(b, amtKey(b, "10"), csvbase.Const(b, "STOCK")),
		Cost:    costSpecKey(b),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{
		Date:     dateKey(b, someDate),
		Postings: csvbase.DoubleEntry(b, primary, csvbase.Const(b, "Assets:Cash")),
	})
	dirs, _ := runEmit(t, b, tx)
	got := dirs[0].(*ast.Transaction)
	if len(got.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(got.Postings))
	}
	if got.Postings[1].Amount != nil {
		t.Errorf("counter amount = %v, want nil (cost-elided)", got.Postings[1].Amount)
	}
}

func TestDoubleEntry_CounterWarningKeepsSinglePosting(t *testing.T) {
	b := csvbase.NewBuilder()
	postings := csvbase.DoubleEntry(b, primaryPosting(b, "Assets:Bank", "50", "USD"), warnStrKey(b, "counter-warn"))
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: postings})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1 (row kept)", len(dirs))
	}
	if n := len(dirs[0].(*ast.Transaction).Postings); n != 1 {
		t.Errorf("got %d postings, want 1 (counter warned)", n)
	}
	if len(diags) != 1 || diags[0].Code != "counter-warn" {
		t.Fatalf("diags = %v, want one counter-warn", diags)
	}
	if diags[0].Severity != ast.Warning {
		t.Errorf("severity = %v, want Warning", diags[0].Severity)
	}
}

func TestDoubleEntry_EmptyCounterNoWarning(t *testing.T) {
	b := csvbase.NewBuilder()
	postings := csvbase.DoubleEntry(b, primaryPosting(b, "Assets:Bank", "50", "USD"), csvbase.Const(b, ""))
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: postings})
	dirs, diags := runEmit(t, b, tx)
	if len(diags) != 0 {
		t.Errorf("diags = %v, want none", diags)
	}
	if n := len(dirs[0].(*ast.Transaction).Postings); n != 1 {
		t.Errorf("got %d postings, want 1", n)
	}
}

func TestDoubleEntry_ZeroCounterNoWarning(t *testing.T) {
	b := csvbase.NewBuilder()
	var zeroCounter csvbase.Key[string]
	postings := csvbase.DoubleEntry(b, primaryPosting(b, "Assets:Bank", "50", "USD"), zeroCounter)
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: postings})
	dirs, diags := runEmit(t, b, tx)
	if len(diags) != 0 {
		t.Errorf("diags = %v, want none", diags)
	}
	if n := len(dirs[0].(*ast.Transaction).Postings); n != 1 {
		t.Errorf("got %d postings, want 1", n)
	}
}

func runDir(t *testing.T, b *csvbase.Builder, k csvbase.Key[ast.Directive]) ([]ast.Directive, []ast.Diagnostic) {
	t.Helper()
	p := b.Emit(csvbase.EmitDirective(k))
	dirs, diags, err := p.Map(context.Background(), buildRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	return dirs, diags
}

// ---------------------------------------------------------------------------
// Price annotations
// ---------------------------------------------------------------------------

func TestPosting_WithPrice(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Stock"),
		Amount:  csvbase.Amount(b, amtKey(b, "-2"), csvbase.Const(b, "ACME")),
		Price:   csvbase.Price(b, amtKey(b, "12090"), csvbase.Const(b, "JPY"), false),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	got := dirs[0].(*ast.Transaction).Postings[0]
	if got.Price == nil {
		t.Fatal("Price = nil, want annotation")
	}
	if got.Price.IsTotal {
		t.Error("IsTotal = true, want false (@ per-unit)")
	}
	if !decEq(got.Price.Amount.Number, "12090") || got.Price.Amount.Currency != "JPY" {
		t.Errorf("price amount = %v, want 12090 JPY", got.Price.Amount)
	}
}

func TestPrice_NilAmountYieldsNoAnnotation(t *testing.T) {
	b := csvbase.NewBuilder()
	p := csvbase.Posting(b, csvbase.PostingSpec{
		Account: csvbase.Const(b, "Assets:Stock"),
		Amount:  csvbase.Amount(b, amtKey(b, "-2"), csvbase.Const(b, "ACME")),
		Price:   csvbase.Price(b, nilAmtKey(b), csvbase.Const(b, "JPY"), false),
	})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{Date: dateKey(b, someDate), Postings: csvbase.Postings(b, p)})
	dirs, _ := runEmit(t, b, tx)
	if got := dirs[0].(*ast.Transaction).Postings[0].Price; got != nil {
		t.Errorf("Price = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// Balance assertions via the generic directive terminal
// ---------------------------------------------------------------------------

func balanceKey(t *testing.T, b *csvbase.Builder, account, num, cur string) csvbase.Key[*ast.Balance] {
	t.Helper()
	return csvbase.Balance(b, csvbase.BalanceSpec{
		Date:    dateKey(b, someDate),
		Account: csvbase.Const(b, account),
		Amount:  csvbase.Amount(b, amtKey(b, num), csvbase.Const(b, cur)),
	})
}

func TestBalance_EmittedViaDirective(t *testing.T) {
	b := csvbase.NewBuilder()
	dir := csvbase.AsDirective(b, balanceKey(t, b, "Assets:Cash", "1000", "JPY"))
	dirs, diags := runDir(t, b, dir)
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	got, ok := dirs[0].(*ast.Balance)
	if !ok {
		t.Fatalf("directive = %T, want *ast.Balance", dirs[0])
	}
	if string(got.Account) != "Assets:Cash" || !decEq(got.Amount.Number, "1000") || got.Amount.Currency != "JPY" {
		t.Errorf("balance = %+v", got)
	}
}

func TestBalance_MissingAmountDrops(t *testing.T) {
	b := csvbase.NewBuilder()
	bal := csvbase.Balance(b, csvbase.BalanceSpec{
		Date:    dateKey(b, someDate),
		Account: csvbase.Const(b, "Assets:Cash"),
		Amount:  csvbase.Amount(b, nilAmtKey(b), csvbase.Const(b, "JPY")),
	})
	dirs, diags := runDir(t, b, csvbase.AsDirective(b, bal))
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingAmount {
		t.Errorf("diags = %v, want DiagMissingAmount", diags)
	}
}

// TestAsDirective_NilLiftsToNil pins the interface-nil guard: a nil typed value
// must lift to a true nil directive, so EmitDirective skips rather than emitting
// a non-nil interface wrapping a nil pointer.
func TestAsDirective_NilLiftsToNil(t *testing.T) {
	b := csvbase.NewBuilder()
	nilTx := csvbase.AddStep(b, func(*csvbase.MappingState) (*ast.Transaction, *ast.Diagnostic, error) {
		return nil, nil, nil
	})
	dirs, diags := runDir(t, b, csvbase.AsDirective(b, nilTx))
	if len(dirs) != 0 || len(diags) != 0 {
		t.Errorf("got dirs=%v diags=%v, want empty (nil lifts to nil)", dirs, diags)
	}
}

func TestDoubleEntry_PrimarySoftFailDrops(t *testing.T) {
	b := csvbase.NewBuilder()
	bad := csvbase.Posting(b, csvbase.PostingSpec{Account: csvbase.Const(b, "")})
	tx := csvbase.Transaction(b, csvbase.TxnSpec{
		Date:     dateKey(b, someDate),
		Postings: csvbase.DoubleEntry(b, bad, csvbase.Const(b, "Expenses:Food")),
	})
	dirs, diags := runEmit(t, b, tx)
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingAccount {
		t.Errorf("diags = %v, want DiagMissingAccount", diags)
	}
}
