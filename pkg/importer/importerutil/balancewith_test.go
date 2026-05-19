package importerutil_test

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
)

var cmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

func mustDecimal(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return *d
}

func mustDecimalPtr(s string) *apd.Decimal {
	d := mustDecimal(s)
	return &d
}

func singlePostingTxn() *ast.Transaction {
	return &ast.Transaction{
		Span:      ast.Span{Start: ast.Position{Filename: "test.bean", Line: 1}},
		Date:      time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Narration: "test",
		Postings: []ast.Posting{
			{
				Span:    ast.Span{Start: ast.Position{Filename: "test.bean", Line: 2}},
				Account: ast.Assets.MustSub("Cash"),
				Amount:  &ast.Amount{Number: mustDecimal("100.00"), Currency: "USD"},
			},
		},
	}
}

func TestBalanceWith_SinglePosting(t *testing.T) {
	txn := singlePostingTxn()
	got := importerutil.BalanceWith(txn, "Expenses:Food", "")

	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("BalanceWith returned %T, want *ast.Transaction", got)
	}
	if len(result.Postings) != 2 {
		t.Fatalf("len(Postings) = %d, want 2", len(result.Postings))
	}

	wantPosting := ast.Posting{
		Span:    txn.Postings[0].Span,
		Account: ast.Account("Expenses:Food"),
		Amount:  &ast.Amount{Number: mustDecimal("-100.00"), Currency: "USD"},
	}
	if diff := cmp.Diff(wantPosting, result.Postings[1], cmpOpts); diff != "" {
		t.Errorf("counterpart posting mismatch (-want +got):\n%s", diff)
	}
}

func TestBalanceWith_SpanCopied(t *testing.T) {
	txn := singlePostingTxn()
	got := importerutil.BalanceWith(txn, "Expenses:Food", "").(*ast.Transaction)
	wantSpan := txn.Postings[0].Span
	if got.Postings[1].Span != wantSpan {
		t.Errorf("counterpart Span = %v, want %v", got.Postings[1].Span, wantSpan)
	}
}

func TestBalanceWith_CurrencyOverride(t *testing.T) {
	txn := singlePostingTxn()
	got := importerutil.BalanceWith(txn, "Expenses:Food", "JPY").(*ast.Transaction)
	if got.Postings[1].Amount.Currency != "JPY" {
		t.Errorf("counterpart Currency = %q, want %q", got.Postings[1].Amount.Currency, "JPY")
	}
}

func TestBalanceWith_CurrencyInferred(t *testing.T) {
	txn := singlePostingTxn()
	got := importerutil.BalanceWith(txn, "Expenses:Food", "").(*ast.Transaction)
	if got.Postings[1].Amount.Currency != "USD" {
		t.Errorf("counterpart Currency = %q, want %q", got.Postings[1].Amount.Currency, "USD")
	}
}

func TestBalanceWith_InputUnchanged(t *testing.T) {
	orig := singlePostingTxn()
	orig.Meta = ast.Metadata{Props: map[string]ast.MetaValue{
		"existing": {Kind: ast.MetaString, String: "keep"},
	}}
	origCopy := orig.Clone()

	result := importerutil.BalanceWith(orig, "Expenses:Food", "").(*ast.Transaction)

	// (a) mutate the returned Amount via Decimal context op (not direct .Set)
	ctx := apd.BaseContext.WithPrecision(20)
	_, _ = ctx.Add(&result.Postings[0].Amount.Number, &result.Postings[0].Amount.Number, mustDecimalPtr("999"))

	// (b) append a new posting to the returned slice
	result.Postings = append(result.Postings, ast.Posting{Account: "Imaginary"})

	// (c) write a new key into the returned directive's Meta.Props
	if result.Meta.Props == nil {
		result.Meta.Props = map[string]ast.MetaValue{}
	}
	result.Meta.Props["injected"] = ast.MetaValue{Kind: ast.MetaString, String: "from-result"}

	if diff := cmp.Diff(origCopy, orig, cmpOpts); diff != "" {
		t.Errorf("BalanceWith mutated input (-want +got):\n%s", diff)
	}
}

func TestBalanceWith_NoopZeroPostings(t *testing.T) {
	txn := &ast.Transaction{Postings: nil}
	got := importerutil.BalanceWith(txn, "Expenses:Food", "")
	if got != ast.Directive(txn) {
		t.Errorf("BalanceWith(zero-posting txn, %q, %q): got %p, want same pointer as input %p", "Expenses:Food", "", got, txn)
	}
}

func TestBalanceWith_NoopTwoPostings(t *testing.T) {
	txn := singlePostingTxn()
	txn.Postings = append(txn.Postings, txn.Postings[0])
	got := importerutil.BalanceWith(txn, "Expenses:Food", "")
	if got != ast.Directive(txn) {
		t.Errorf("BalanceWith(two-posting txn, %q, %q): got %p, want same pointer as input %p", "Expenses:Food", "", got, txn)
	}
}

func TestBalanceWith_NoopNilAmount(t *testing.T) {
	txn := singlePostingTxn()
	txn.Postings[0].Amount = nil
	got := importerutil.BalanceWith(txn, "Expenses:Food", "")
	if got != ast.Directive(txn) {
		t.Errorf("BalanceWith(nil-Amount txn, %q, %q): got %p, want same pointer as input %p", "Expenses:Food", "", got, txn)
	}
}

func TestBalanceWith_NoopNonTransaction(t *testing.T) {
	directives := []ast.Directive{
		&ast.Open{Date: time.Now(), Account: ast.Assets.MustSub("Cash")},
		&ast.Close{Date: time.Now(), Account: ast.Assets.MustSub("Cash")},
		&ast.Balance{Date: time.Now(), Account: ast.Assets.MustSub("Cash"), Amount: ast.Amount{Number: mustDecimal("0"), Currency: "USD"}},
		&ast.Pad{Date: time.Now(), Account: ast.Assets.MustSub("Cash"), PadAccount: ast.Equity.MustSub("Opening")},
		&ast.Note{Date: time.Now(), Account: ast.Assets.MustSub("Cash"), Comment: "hi"},
		&ast.Price{Date: time.Now(), Commodity: "USD", Amount: ast.Amount{Number: mustDecimal("1"), Currency: "EUR"}},
		&ast.Commodity{Date: time.Now(), Currency: "USD"},
		&ast.Event{Date: time.Now(), Name: "location", Value: "NYC"},
		&ast.Query{Date: time.Now(), Name: "q", BQL: "SELECT *"},
		&ast.Custom{Date: time.Now(), TypeName: "t"},
		&ast.Option{Key: "k", Value: "v"},
		&ast.Plugin{Name: "p"},
		&ast.Include{Path: "/f"},
	}
	for _, d := range directives {
		got := importerutil.BalanceWith(d, "Expenses:Food", "")
		if got != d {
			t.Errorf("BalanceWith(%T): got %p, want same pointer as input (%p)", d, got, d)
		}
	}
}

func TestBalanceWith_NoopNil(t *testing.T) {
	got := importerutil.BalanceWith(nil, "Expenses:Food", "")
	if got != nil {
		t.Errorf("BalanceWith(nil) = %v, want nil", got)
	}
}

func BenchmarkBalanceWith(b *testing.B) {
	txn := singlePostingTxn()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = importerutil.BalanceWith(txn, "Expenses:Food", "")
	}
}
