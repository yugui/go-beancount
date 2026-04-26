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

// errorCmpOpts compares api.Error values structurally while leaving
// the human-readable Message field to per-test substring assertions.
var errorCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(api.Error{}, "Message"),
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

// TestLeafOnlyLedgerNoErrors: a ledger where every used account is a
// leaf in the referenced-account tree produces no diagnostics.
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
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestNonLeafPostingFlagged: when both Assets:Cash and Assets:Cash:USD
// are opened and a transaction posts to Assets:Cash, a single error is
// emitted.
func TestNonLeafPostingFlagged(t *testing.T) {
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

	want := []api.Error{{Code: "non-leaf-account", Span: tx.Span}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	// The exact wording is part of the documented behavior — it mirrors
	// upstream's "Non-leaf account '{}' has postings on it" template.
	wantMsg := "Non-leaf account 'Assets:Cash' has postings on it"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
}

// TestNestedHierarchyParentOfParent: when accounts span three levels
// (e.g. Assets, Assets:Cash, Assets:Cash:USD), a posting against any
// non-leaf account is flagged. Postings against the deepest leaf are
// not.
func TestNestedHierarchyParentOfParent(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	// Three accounts opened at the same path; the deepest is the only
	// leaf.
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets"}
	openB := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openC := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}

	// Three transactions: one posting against each of the three Assets
	// levels. Only the leaf (Assets:Cash:USD) should pass.
	mkTx := func(acct ast.Account, line int) *ast.Transaction {
		p := pos
		n := neg
		return &ast.Transaction{
			Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Flag: '*',
			Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: line}},
			Postings: []ast.Posting{
				{Account: acct, Amount: &p},
				{Account: "Income:Salary", Amount: &n},
			},
		}
	}
	txRoot := mkTx("Assets", 10)
	txMid := mkTx("Assets:Cash", 20)
	txLeaf := mkTx("Assets:Cash:USD", 30)

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openA, openB, openC, openIncome, txRoot, txMid, txLeaf}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Errors) != 2 {
		t.Fatalf("len(res.Errors) = %d, want 2 (Assets and Assets:Cash); errors = %v", len(res.Errors), res.Errors)
	}
	// Build a (message -> span) map and assert each diagnostic is
	// anchored at the originating transaction's span. Keying on the full
	// message avoids the order-sensitivity of substring matching when
	// one account name is a prefix of another.
	msgFor := func(acct ast.Account) string {
		return "Non-leaf account '" + string(acct) + "' has postings on it"
	}
	hits := map[string]ast.Span{}
	for _, e := range res.Errors {
		hits[e.Message] = e.Span
	}
	if got, ok := hits[msgFor("Assets")]; !ok || got != txRoot.Span {
		t.Errorf("error for 'Assets' not anchored at txRoot.Span; hits = %#v", hits)
	}
	if got, ok := hits[msgFor("Assets:Cash")]; !ok || got != txMid.Span {
		t.Errorf("error for 'Assets:Cash' not anchored at txMid.Span; hits = %#v", hits)
	}
}

// TestParentOpenedNoPostingNoError: a non-leaf parent that is merely
// opened (no transaction posts to it) emits no diagnostic. The plugin
// only flags actual postings.
func TestParentOpenedNoPostingNoError(t *testing.T) {
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
		// Posts only to leaves; no error expected.
		Postings: []ast.Posting{
			{Account: "Assets:Cash:USD", Amount: &pos},
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
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (parent opened but never posted to); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestParentWithoutChildIsLeaf: when only a parent account is opened
// (no descendants), it remains a leaf — postings against it are fine.
func TestParentWithoutChildIsLeaf(t *testing.T) {
	openCash := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
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
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openCash, openIncome, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (no child opened); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestPostingAlonePromotesToNonLeaf: even without an Open directive,
// a posting that mentions a deeper account makes a referenced parent
// non-leaf. This matches upstream's `realization.realize` behavior of
// building the tree from references rather than from Open directives.
func TestPostingAlonePromotesToNonLeaf(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	// Two transactions: tx1 posts against the parent, tx2 posts against
	// a deeper child. Neither account is opened, but tx2's reference is
	// enough to mark Assets:Cash non-leaf and flag tx1's posting.
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
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1 (one offending posting); errors = %v", len(res.Errors), res.Errors)
	}
	if res.Errors[0].Span != tx1.Span {
		t.Errorf("res.Errors[0].Span = %#v, want tx1.Span = %#v", res.Errors[0].Span, tx1.Span)
	}
}

// TestMultipleBadPostingsEmitMultipleErrors: each offending posting
// against a non-leaf account contributes its own diagnostic. This
// differs from upstream (which emits one error per offending account);
// the deviation is documented in the package godoc.
func TestMultipleBadPostingsEmitMultipleErrors(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	pos := amt(50, "USD")
	neg := amt(-50, "USD")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openParent, openChild, openIncome, tx1, tx2}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 2 {
		t.Errorf("len(res.Errors) = %d, want 2 (one per offending posting); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestPostingSpanPreferred: when the offending posting itself carries
// a non-zero span, the diagnostic is anchored there in preference to
// the enclosing transaction's span.
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
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if res.Errors[0].Span != postingSpan {
		t.Errorf("res.Errors[0].Span = %#v, want postingSpan %#v", res.Errors[0].Span, postingSpan)
	}
}

// TestPluginSpanFallback: when neither the posting nor the transaction
// carries a span, the diagnostic falls back to the triggering plugin
// directive's span.
func TestPluginSpanFallback(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	// No Span set on the transaction or posting: zero values.
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
		Directives: seqOf([]ast.Directive{openParent, openChild, openIncome, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if res.Errors[0].Span != testPluginDir.Span {
		t.Errorf("res.Errors[0].Span = %#v, want testPluginDir.Span %#v (fallback)", res.Errors[0].Span, testPluginDir.Span)
	}
}

// TestNonTransactionDirectiveDoesNotTrigger: a non-Transaction directive
// referencing a non-leaf account does not produce a diagnostic. Per the
// package godoc this is an intentional deviation from upstream — the
// references still contribute to the non-leaf set but only Transaction
// postings trigger errors.
func TestNonTransactionDirectiveDoesNotTrigger(t *testing.T) {
	openParent := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openChild := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash:USD"}
	note := &ast.Note{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Comment: "non-leaf, but harmless",
	}
	doc := &ast.Document{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Path:    "/tmp/r.pdf",
	}
	// Pad references the non-leaf parent in both Account and PadAccount.
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Cash",
		PadAccount: "Assets:Cash",
	}
	// Custom carries a MetaAccount value referencing the non-leaf parent.
	custom := &ast.Custom{
		Date:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		TypeName: "budget",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Cash"},
		},
	}

	cases := []struct {
		name string
		dir  ast.Directive
	}{
		{name: "Note", dir: note},
		{name: "Document", dir: doc},
		{name: "Pad", dir: pad},
		{name: "Custom", dir: custom},
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
			if len(res.Errors) != 0 {
				t.Errorf("len(res.Errors) = %d, want 0 (%s reference does not trigger); errors = %v", len(res.Errors), tc.name, res.Errors)
			}
		})
	}
}

// TestEmptyInput: an empty directive sequence yields a zero-valued
// Result.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil for empty input", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 for empty input", len(res.Errors))
	}
}

// TestNilDirectivesIterator: a nil Directives iterator is treated as
// empty input — the plugin returns a zero-valued Result, not an error.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{Directive: testPluginDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestNoDirectiveMutation: the plugin is diagnostic-only and must not
// mutate input directives or write back through Input.Directive.
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

// TestCanceledContext: the plugin respects a canceled context.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
}

// TestDistinctRootsAreLeaves: two completely unrelated accounts under
// different roots (Assets:Cash, Income:Salary) — neither is a prefix
// of the other, so neither is non-leaf.
func TestDistinctRootsAreLeaves(t *testing.T) {
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
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestSimilarPrefixIsNotAncestor: Assets:Cash is NOT an ancestor of
// Assets:CashFlow even though one's string is a prefix of the other.
// The plugin must compare on component boundaries, not raw string
// prefixes.
func TestSimilarPrefixIsNotAncestor(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash"}
	openB := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:CashFlow"}
	openIncome := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary"}
	pos := amt(1, "USD")
	neg := amt(-1, "USD")
	// Both Assets:Cash and Assets:CashFlow are leaves of the tree —
	// they share Assets as ancestor but neither is the other's parent.
	// Posting against either is fine.
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
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (Assets:CashFlow is not a child of Assets:Cash); errors = %v", len(res.Errors), res.Errors)
	}
}
