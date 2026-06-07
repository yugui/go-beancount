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

type txKeys struct {
	date     csvbase.Key[time.Time]
	amount   csvbase.Key[csvkit.Amount]
	currency csvbase.Key[string]
	account  csvbase.Key[string]
	counter  csvbase.Key[string]
	narr     csvbase.Key[string]
	payee    csvbase.Key[string]
	cost     csvbase.Key[*ast.CostSpec]
	tag      csvbase.Key[string]
	link     csvbase.Key[string]
	meta     csvbase.Key[string]
}

// minimalTxConfig returns a TxConfig with only the required keys wired, plus
// whatever optional keys are non-zero in k.
func minimalTxConfig(k txKeys) csvbase.TxConfig {
	return csvbase.TxConfig{
		Date:      k.date,
		Amount:    k.amount,
		Currency:  k.currency,
		Account:   k.account,
		Counter:   k.counter,
		Narration: k.narr,
		Payee:     k.payee,
		Cost:      k.cost,
		Tags:      nonZeroStringKeys(k.tag),
		Links:     nonZeroStringKeys(k.link),
		Meta: func() []csvbase.MetaField {
			if isZeroStringKey(k.meta) {
				return nil
			}
			return []csvbase.MetaField{{Name: "ref", Value: k.meta}}
		}(),
	}
}

func nonZeroStringKeys(ks ...csvbase.Key[string]) []csvbase.Key[string] {
	var out []csvbase.Key[string]
	for _, k := range ks {
		if !isZeroStringKey(k) {
			out = append(out, k)
		}
	}
	return out
}

func isZeroStringKey(k csvbase.Key[string]) bool {
	var zero csvbase.Key[string]
	return k == zero
}

// ---------------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------------

func TestEmitTransaction_HappyPath(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
		counter:  csvbase.Const(b, "Expenses:Food"),
		narr:     csvbase.Const(b, "Groceries"),
		payee:    csvbase.Const(b, "ACME"),
		tag:      csvbase.Const(b, "trip"),
		link:     csvbase.Const(b, "inv-1"),
		meta:     csvbase.Const(b, "REF123"),
	}
	p := b.Emit(csvbase.EmitTransaction(minimalTxConfig(keys)))

	dirs, diags, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d directives, want 1", len(dirs))
	}
	tx := dirs[0].(*ast.Transaction)
	if tx.Flag != '*' {
		t.Errorf("EmitTransaction() flag = %q, want %q", tx.Flag, byte('*'))
	}
	if tx.Payee != "ACME" {
		t.Errorf("payee = %q, want %q", tx.Payee, "ACME")
	}
	if tx.Narration != "Groceries" {
		t.Errorf("narration = %q, want %q", tx.Narration, "Groceries")
	}
	if len(tx.Tags) != 1 || tx.Tags[0] != "trip" {
		t.Errorf("tags = %v, want [trip]", tx.Tags)
	}
	if len(tx.Links) != 1 || tx.Links[0] != "inv-1" {
		t.Errorf("links = %v, want [inv-1]", tx.Links)
	}
	if tx.Meta.Props["ref"].String != "REF123" {
		t.Errorf("meta ref = %q, want %q", tx.Meta.Props["ref"].String, "REF123")
	}
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	// primary posting
	p0 := tx.Postings[0]
	if string(p0.Account) != "Assets:Bank" {
		t.Errorf("primary account = %q, want %q", p0.Account, "Assets:Bank")
	}
	if p0.Amount.Currency != "USD" {
		t.Errorf("primary currency = %q, want %q", p0.Amount.Currency, "USD")
	}
	wantNum, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "100")
	if p0.Amount.Number.Cmp(wantNum) != 0 {
		t.Errorf("primary amount = %v, want 100", p0.Amount.Number)
	}
	// counter posting: negated amount
	p1 := tx.Postings[1]
	if string(p1.Account) != "Expenses:Food" {
		t.Errorf("counter account = %q, want %q", p1.Account, "Expenses:Food")
	}
	wantNeg, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "-100")
	if p1.Amount.Number.Cmp(wantNeg) != 0 {
		t.Errorf("counter amount = %v, want -100", p1.Amount.Number)
	}
}

// ---------------------------------------------------------------------------
// Custom flag
// ---------------------------------------------------------------------------

func TestEmitTransaction_CustomFlag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "10"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
	}
	cfg := minimalTxConfig(keys)
	cfg.Flag = '!'
	p := b.Emit(csvbase.EmitTransaction(cfg))
	dirs, _, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	tx := dirs[0].(*ast.Transaction)
	if tx.Flag != '!' {
		t.Errorf("EmitTransaction() flag = %q, want %q", tx.Flag, '!')
	}
}

// ---------------------------------------------------------------------------
// Drop on each required field's soft-fail diagnostic
// ---------------------------------------------------------------------------

func TestEmitTransaction_DropOnDateDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     failingDateKey(b),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
	}
	assertDropWithDiag(t, b, keys, "date-fail")
}

func TestEmitTransaction_DropOnAmountDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   failingAmountKey(b),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
	}
	assertDropWithDiag(t, b, keys, "amount-fail")
}

func TestEmitTransaction_DropOnCurrencyDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: failingStringKey(b, "currency-fail"),
		account:  csvbase.Const(b, "Assets:Bank"),
	}
	assertDropWithDiag(t, b, keys, "currency-fail")
}

func TestEmitTransaction_DropOnAccountDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, "USD"),
		account:  failingStringKey(b, "account-fail"),
	}
	assertDropWithDiag(t, b, keys, "account-fail")
}

func TestEmitTransaction_DropOnNarrationDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
		narr:     failingStringKey(b, "narration-fail"),
	}
	assertDropWithDiag(t, b, keys, "narration-fail")
}

func TestEmitTransaction_DropOnCostDiag(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
		cost:     failingCostKey(b, "cost-fail"),
	}
	assertDropWithDiag(t, b, keys, "cost-fail")
}

// ---------------------------------------------------------------------------
// Missing currency/account (empty value, no diag) => drop with missing code
// ---------------------------------------------------------------------------

func TestEmitTransaction_EmptyCurrencyDrops(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, ""),
		account:  csvbase.Const(b, "Assets:Bank"),
	}
	p := b.Emit(csvbase.EmitTransaction(minimalTxConfig(keys)))
	dirs, diags, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingCurrency {
		t.Errorf("diags = %v, want DiagMissingCurrency", diags)
	}
}

func TestEmitTransaction_EmptyAccountDrops(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "100"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, ""),
	}
	p := b.Emit(csvbase.EmitTransaction(minimalTxConfig(keys)))
	dirs, diags, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("got %d dirs, want 0", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != csvbase.DiagMissingAccount {
		t.Errorf("diags = %v, want DiagMissingAccount", diags)
	}
}

// ---------------------------------------------------------------------------
// Counter warning => row kept, single posting, warning surfaced
// ---------------------------------------------------------------------------

func TestEmitTransaction_CounterWarning(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "50"),
		currency: csvbase.Const(b, "USD"),
		account:  csvbase.Const(b, "Assets:Bank"),
		counter:  warnStringKey(b, "counter-warn"),
	}
	p := b.Emit(csvbase.EmitTransaction(minimalTxConfig(keys)))
	dirs, diags, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	// row is kept
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1 (row kept)", len(dirs))
	}
	tx := dirs[0].(*ast.Transaction)
	// single posting only
	if len(tx.Postings) != 1 {
		t.Errorf("got %d postings, want 1 (no counter posting on warning)", len(tx.Postings))
	}
	// warning surfaced
	if len(diags) != 1 || diags[0].Code != "counter-warn" {
		t.Errorf("diags = %v, want one counter-warn warning", diags)
	}
	if diags[0].Severity != ast.Warning {
		t.Errorf("diag severity = %v, want Warning", diags[0].Severity)
	}
}

// ---------------------------------------------------------------------------
// Cost present + counter => counter posting has no amount (elision)
// ---------------------------------------------------------------------------

func TestEmitTransaction_CostElision(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := txKeys{
		date:     fixedDateKey(b, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		amount:   fixedAmountKey(b, "10"),
		currency: csvbase.Const(b, "STOCK"),
		account:  csvbase.Const(b, "Assets:Brokerage"),
		counter:  csvbase.Const(b, "Assets:Cash"),
		cost:     fixedCostKey(b),
	}
	p := b.Emit(csvbase.EmitTransaction(minimalTxConfig(keys)))
	dirs, diags, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1", len(dirs))
	}
	tx := dirs[0].(*ast.Transaction)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	// counter posting must have no amount (elided)
	if tx.Postings[1].Amount != nil {
		t.Errorf("counter posting amount = %v, want nil (cost-elided)", tx.Postings[1].Amount)
	}
}

// ---------------------------------------------------------------------------
// Panic on zero Date/Amount key
// ---------------------------------------------------------------------------

func TestEmitTransaction_PanicOnZeroDateKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero Date key, got none")
		}
	}()
	b := csvbase.NewBuilder()
	amt := fixedAmountKey(b, "10")
	csvbase.EmitTransaction(csvbase.TxConfig{Amount: amt}) // zero Date
}

func TestEmitTransaction_PanicOnZeroAmountKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero Amount key, got none")
		}
	}()
	b := csvbase.NewBuilder()
	date := fixedDateKey(b, time.Now())
	csvbase.EmitTransaction(csvbase.TxConfig{Date: date}) // zero Amount
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func emptyRec() csvbase.RowContext {
	return csvbase.RowContext{Fields: []string{}, Index: map[string]int{}, Path: "/f.csv", Line: 1}
}

func fixedDateKey(b *csvbase.Builder, t time.Time) csvbase.Key[time.Time] {
	return csvbase.AddStep(b, func(*csvbase.Cells) (time.Time, *ast.Diagnostic, error) {
		return t, nil, nil
	})
}

func failingDateKey(b *csvbase.Builder) csvbase.Key[time.Time] {
	return csvbase.AddStep(b, func(*csvbase.Cells) (time.Time, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag("date-fail", "/f.csv", 1, "bad date")
		return time.Time{}, &d, nil
	})
}

func fixedAmountKey(b *csvbase.Builder, num string) csvbase.Key[csvkit.Amount] {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), num)
	return csvbase.AddStep(b, func(*csvbase.Cells) (csvkit.Amount, *ast.Diagnostic, error) {
		return csvkit.Amount{Number: *n}, nil, nil
	})
}

func failingAmountKey(b *csvbase.Builder) csvbase.Key[csvkit.Amount] {
	return csvbase.AddStep(b, func(*csvbase.Cells) (csvkit.Amount, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag("amount-fail", "/f.csv", 1, "bad amount")
		return csvkit.Amount{}, &d, nil
	})
}

func failingStringKey(b *csvbase.Builder, code string) csvbase.Key[string] {
	return csvbase.AddStep(b, func(*csvbase.Cells) (string, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag(code, "/f.csv", 1, "fail")
		return "", &d, nil
	})
}

// warnStringKey produces a warning-severity soft-fail.
func warnStringKey(b *csvbase.Builder, code string) csvbase.Key[string] {
	return csvbase.AddStep(b, func(*csvbase.Cells) (string, *ast.Diagnostic, error) {
		d := csvbase.WarnDiag(code, "/f.csv", 1, "warn")
		return "", &d, nil
	})
}

func fixedCostKey(b *csvbase.Builder) csvbase.Key[*ast.CostSpec] {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "100")
	return csvbase.AddStep(b, func(*csvbase.Cells) (*ast.CostSpec, *ast.Diagnostic, error) {
		return &ast.CostSpec{PerUnit: n, Currency: "USD"}, nil, nil
	})
}

func failingCostKey(b *csvbase.Builder, code string) csvbase.Key[*ast.CostSpec] {
	return csvbase.AddStep(b, func(*csvbase.Cells) (*ast.CostSpec, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag(code, "/f.csv", 1, "fail")
		return nil, &d, nil
	})
}

func assertDropWithDiag(t *testing.T, b *csvbase.Builder, keys txKeys, wantCode string) {
	t.Helper()
	p := b.Emit(csvbase.EmitTransaction(minimalTxConfig(keys)))
	dirs, diags, err := p.Map(context.Background(), emptyRec())
	if err != nil {
		t.Fatalf("EmitTransaction() Map error = %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("EmitTransaction() directive count = %d, want 0 (row dropped)", len(dirs))
	}
	if len(diags) != 1 || diags[0].Code != wantCode {
		t.Errorf("EmitTransaction() diags = %v, want one %q diagnostic", diags, wantCode)
	}
}
