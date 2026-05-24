package leafonly

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// diagCmpOpts compares ast.Diagnostic values structurally while leaving
// the human-readable Message field to per-test substring assertions.
var diagCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(ast.Diagnostic{}, "Message"),
}

// testPluginDir is a non-zero *ast.Plugin used as the api.Input.Directive
// fallback span in tests.
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 1}}}

func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

func amt(n int64, cur string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: cur}
}

// TestTransactionPostingOnNonLeafFlagged: a transaction posting against a
// non-leaf account emits one diagnostic with code "non-leaf-account".
func TestTransactionPostingOnNonLeafFlagged(t *testing.T) {
	openParent := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	openChild := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash:USD",
	}
	openIncome := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Income:Salary",
	}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 10}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openIncome, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []ast.Diagnostic{{Code: "non-leaf-account", Span: tx.Span, Severity: ast.Error}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply diagnostics mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "non-leaf account 'Assets:Cash' has transactions or pad directives on it"
	if got := res.Diagnostics[0].Message; got != wantMsg {
		t.Errorf("res.Diagnostics[0].Message = %q, want %q", got, wantMsg)
	}
}

// TestPadDirectiveOnNonLeafFlagged: a pad directive whose Account is non-leaf
// emits one diagnostic. This is the key behavioral addition over std/leafonly.
func TestPadDirectiveOnNonLeafFlagged(t *testing.T) {
	openParent := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Bank",
	}
	openChild := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Bank:Checking",
	}
	openEquity := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Equity:Opening",
	}
	padSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 20}}
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Bank",
		PadAccount: "Equity:Opening",
		Span:       padSpan,
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openEquity, pad}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []ast.Diagnostic{{Code: "non-leaf-account", Span: padSpan, Severity: ast.Error}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply diagnostics mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "non-leaf account 'Assets:Bank' has transactions or pad directives on it"
	if got := res.Diagnostics[0].Message; got != wantMsg {
		t.Errorf("res.Diagnostics[0].Message = %q, want %q", got, wantMsg)
	}
}

// TestPadOnLeafAccountNoError: a pad directive against a leaf account is fine.
func TestPadOnLeafAccountNoError(t *testing.T) {
	openLeaf := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Bank:Checking",
	}
	openEquity := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Equity:Opening",
	}
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Bank:Checking",
		PadAccount: "Equity:Opening",
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openLeaf, openEquity, pad}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (leaf pad is fine); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestNonTriggerDirectivesDoNotTrigger: Open, Close, Note, Document, and
// Balance directives on a non-leaf account do NOT emit diagnostics. This is
// a regression guard for the narrower trigger filter that distinguishes this
// plugin from std/leafonly.
func TestNonTriggerDirectivesDoNotTrigger(t *testing.T) {
	openParent := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	openChild := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash:USD",
	}
	closeParent := &ast.Close{
		Date:    time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	note := &ast.Note{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Comment: "annotating non-leaf",
	}
	doc := &ast.Document{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Path:    "/tmp/r.pdf",
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}

	cases := []struct {
		name string
		dir  ast.Directive
	}{
		{name: "Close", dir: closeParent},
		{name: "Note", dir: note},
		{name: "Document", dir: doc},
		{name: "Balance", dir: bal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := api.Input{
				Directive:  testPluginDir,
				Directives: seqOf([]ast.Directive{openParent, openChild, tc.dir}),
			}
			res, err := apply(context.Background(), in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(res.Diagnostics) != 0 {
				t.Errorf("len(res.Diagnostics) = %d, want 0 (%s on non-leaf does not trigger); errors = %v", len(res.Diagnostics), tc.name, res.Diagnostics)
			}
		})
	}
}

// TestTransactionAndPadBothFlagged: when both a transaction and a pad touch
// the same non-leaf account, both produce diagnostics.
func TestTransactionAndPadBothFlagged(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Bank"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Bank:Checking"}
	openEquity := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:Opening"}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: &pos},
			{Account: "Equity:Opening", Amount: &neg},
		},
	}
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Bank",
		PadAccount: "Equity:Opening",
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openEquity, tx, pad}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 2 {
		t.Errorf("len(res.Diagnostics) = %d, want 2 (one for tx, one for pad); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestLeafOnlyLedgerNoErrors: a ledger where every used account is a leaf
// in the referenced-account tree produces no diagnostics.
func TestLeafOnlyLedgerNoErrors(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash:USD", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestPostingSpanPreferred: when the offending posting itself carries a
// non-zero span, the diagnostic is anchored there in preference to the
// enclosing transaction's span.
func TestPostingSpanPreferred(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	postingSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 42}}
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 41}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos, Span: postingSpan},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openIncome, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != postingSpan {
		t.Errorf("res.Diagnostics[0].Span = %#v, want postingSpan %#v", res.Diagnostics[0].Span, postingSpan)
	}
}

// TestPadSpanFallbackToPlugin: when the pad directive has no span, the
// diagnostic falls back to the triggering plugin directive's span.
func TestPadSpanFallbackToPlugin(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Bank"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Bank:Checking"}
	openEquity := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:Opening"}
	// No Span set on the pad: zero value.
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Bank",
		PadAccount: "Equity:Opening",
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openEquity, pad}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != testPluginDir.Span {
		t.Errorf("res.Diagnostics[0].Span = %#v, want testPluginDir.Span %#v (fallback)", res.Diagnostics[0].Span, testPluginDir.Span)
	}
}

// TestPostingAlonePromotesToNonLeaf: even without an Open directive, a posting
// that mentions a deeper account makes a referenced parent non-leaf.
func TestPostingAlonePromotesToNonLeaf(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 10}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash:USD", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx1, tx2}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1 (one offending posting); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != tx1.Span {
		t.Errorf("res.Diagnostics[0].Span = %#v, want tx1.Span = %#v", res.Diagnostics[0].Span, tx1.Span)
	}
}

// TestEmptyInput: an empty directive sequence yields a zero-valued Result.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil for empty input", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 for empty input", len(res.Diagnostics))
	}
}

// TestNilDirectivesIterator: a nil Directives iterator is treated as empty
// input — the plugin returns a zero-valued Result, not an error.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{Directive: testPluginDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
	}
}

// TestCanceledContext: the plugin respects a canceled context.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
}

// TestNoDirectiveMutation: the plugin is diagnostic-only and must not mutate
// input directives or write back through Input.Directive.
func TestNoDirectiveMutation(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	origPostings := len(tx.Postings)
	origAcct := tx.Postings[0].Account
	origPluginSpan := testPluginDir.Span

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openIncome, tx}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
	if len(tx.Postings) != origPostings {
		t.Errorf("apply mutated transaction: len(tx.Postings) %d -> %d", origPostings, len(tx.Postings))
	}
	if tx.Postings[0].Account != origAcct {
		t.Errorf("apply mutated posting account: %q -> %q", origAcct, tx.Postings[0].Account)
	}
	if testPluginDir.Span != origPluginSpan {
		t.Errorf("apply mutated input plugin: testPluginDir.Span = %#v, want %#v", testPluginDir.Span, origPluginSpan)
	}
}

// TestSimilarPrefixIsNotAncestor: Assets:Cash is NOT an ancestor of
// Assets:CashFlow even though one's string is a prefix of the other.
func TestSimilarPrefixIsNotAncestor(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openB := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:CashFlow"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	pos := amt(1, "USD")
	neg := amt(-1, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openA, openB, openIncome, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (Assets:CashFlow is not a child of Assets:Cash); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}
