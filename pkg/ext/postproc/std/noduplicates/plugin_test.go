package noduplicates

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

// astCmpOpts is the standard option set for deep-comparing AST values.
// apd.Decimal has unexported fields and time.Time carries monotonic
// state, so each gets a custom comparer that defers to the type's own
// equality semantics.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

// testPluginDir is a non-zero *ast.Plugin used as the api.Input.Directive
// fallback span in tests.
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "n.beancount", Line: 1}}}

// txnSpan1/txnSpan2/txnSpan3 are distinct non-zero spans so tests can
// assert each diagnostic anchors at the duplicate Transaction's own
// span and not at the plugin fallback.
var (
	txnSpan1 = ast.Span{Start: ast.Position{Filename: "n.beancount", Line: 100}}
	txnSpan2 = ast.Span{Start: ast.Position{Filename: "n.beancount", Line: 200}}
	txnSpan3 = ast.Span{Start: ast.Position{Filename: "n.beancount", Line: 300}}
)

func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

// dec parses a decimal literal, failing the test on a parse error.
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("apd.SetString(%q): %v", s, err)
	}
	return d
}

// posting builds a Posting with a non-nil Amount.
func posting(t *testing.T, account, number, currency string) ast.Posting {
	t.Helper()
	return ast.Posting{
		Account: ast.Account(account),
		Amount:  &ast.Amount{Number: dec(t, number), Currency: currency},
	}
}

// txn builds a Transaction directive with the given date, narration,
// span, and postings.
func txn(t *testing.T, year, month, day int, narration string, span ast.Span, postings ...ast.Posting) *ast.Transaction {
	t.Helper()
	return &ast.Transaction{
		Date:      time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Narration: narration,
		Postings:  postings,
		Span:      span,
	}
}

// TestNoDuplicates: a single Transaction directive yields no
// diagnostic.
func TestNoDuplicates(t *testing.T) {
	tx := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
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

// TestExactDuplicate: two transactions with identical postings on the
// same date produce one diagnostic on the second. The exact message is
// asserted to lock the human-readable contract.
func TestExactDuplicate(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy coffee", txnSpan2,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []ast.Diagnostic{{Code: codeDuplicateTransaction, Span: txnSpan2}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "Duplicate transaction on 2024-01-01: same postings as earlier entry"
	if got := res.Diagnostics[0].Message; got != wantMsg {
		t.Errorf("res.Diagnostics[0].Message = %q, want %q", got, wantMsg)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestDifferentDates: two otherwise-identical transactions on
// different dates do not collide under the same-date rule documented
// in the package godoc.
func TestDifferentDates(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 2, "buy coffee", txnSpan2,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (different dates); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestDifferentAmounts: same accounts on the same date but with
// different amounts do not collide.
func TestDifferentAmounts(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy lunch", txnSpan2,
		posting(t, "Assets:Cash", "-9.00", "USD"),
		posting(t, "Expenses:Food", "9.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (different amounts); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestDifferentAccounts: same date and amounts but different account
// names do not collide.
func TestDifferentAccounts(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy stamps", txnSpan2,
		posting(t, "Assets:Checking", "-3.50", "USD"),
		posting(t, "Expenses:Postage", "3.50", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (different accounts); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestThreeIdentical: three identical transactions yield two
// diagnostics — one for each duplicate after the first survivor, in
// source-encounter order.
func TestThreeIdentical(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy coffee", txnSpan2,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t3 := txn(t, 2024, 1, 1, "buy coffee", txnSpan3,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2, t3})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{
		{Code: codeDuplicateTransaction, Span: txnSpan2},
		{Code: codeDuplicateTransaction, Span: txnSpan3},
	}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestPostingOrderDoesNotMatter: two transactions whose postings are
// in reversed order are still flagged because the similarity rule
// keys on the multiset of posting tuples, not the slice order.
func TestPostingOrderDoesNotMatter(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy coffee", txnSpan2,
		posting(t, "Expenses:Food", "3.50", "USD"),
		posting(t, "Assets:Cash", "-3.50", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{Code: codeDuplicateTransaction, Span: txnSpan2}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestNarrationDifferenceIgnored: two transactions agreeing on date
// and posting multiset but with different narration are flagged as
// duplicates under this port's rule. Documented as a deliberate
// deviation from upstream (which keys narration into its hash); see
// doc.go.
func TestNarrationDifferenceIgnored(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "ATM withdrawal", txnSpan1,
		posting(t, "Assets:Cash", "100.00", "USD"),
		posting(t, "Assets:Checking", "-100.00", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "ATM WITHDRAWAL", txnSpan2,
		posting(t, "Assets:Cash", "100.00", "USD"),
		posting(t, "Assets:Checking", "-100.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{Code: codeDuplicateTransaction, Span: txnSpan2}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestSpanAnchoring: when the duplicate has a non-zero Span the
// diagnostic anchors at it; when it has a zero Span the diagnostic
// falls back to the triggering plugin directive's Span.
func TestSpanAnchoring(t *testing.T) {
	// Non-zero span path.
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy coffee", txnSpan2,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{t1, t2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if got := res.Diagnostics[0].Span; got != txnSpan2 {
		t.Errorf("res.Diagnostics[0].Span = %#v, want txnSpan2 %#v", got, txnSpan2)
	}

	// Zero-span path: the duplicate has a zero Span, so the diagnostic
	// falls back to the plugin directive's span.
	q1 := txn(t, 2024, 2, 1, "buy lunch", txnSpan3,
		posting(t, "Assets:Cash", "-9.00", "USD"),
		posting(t, "Expenses:Food", "9.00", "USD"),
	)
	q2 := txn(t, 2024, 2, 1, "buy lunch", ast.Span{},
		posting(t, "Assets:Cash", "-9.00", "USD"),
		posting(t, "Expenses:Food", "9.00", "USD"),
	)
	in2 := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{q1, q2})}

	res2, err := apply(context.Background(), in2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res2.Diagnostics) != 1 {
		t.Fatalf("len(res2.Diagnostics) = %d, want 1; errors = %v", len(res2.Diagnostics), res2.Diagnostics)
	}
	if got := res2.Diagnostics[0].Span; got != testPluginDir.Span {
		t.Errorf("res2.Diagnostics[0].Span = %#v, want testPluginDir.Span %#v (fallback)", got, testPluginDir.Span)
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
	if res.Diagnostics != nil {
		t.Errorf("res.Diagnostics = %v, want nil for empty input", res.Diagnostics)
	}
}

// TestNilDirectivesIterator: an Input whose Directives field is nil
// is treated as an empty ledger, returning a zero Result.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{Directive: testPluginDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var zero api.Result
	if !cmp.Equal(res, zero) {
		t.Errorf("apply result = %#v, want zero api.Result", res)
	}
}

// TestCanceledContext: the plugin respects a canceled context and
// returns the context's error along with a zero Result.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := apply(ctx, api.Input{})
	if err != ctx.Err() {
		t.Fatalf("apply error = %v, want ctx.Err() = %v", err, ctx.Err())
	}
	var zero api.Result
	if !cmp.Equal(res, zero) {
		t.Errorf("apply result = %#v, want zero api.Result", res)
	}
}

// TestNoDirectiveMutation: the plugin is diagnostic-only; it must
// not mutate the directives it observes. Asserted by snapshotting
// pre-apply state against post-apply state for a duplicate-pair
// input that exercises the diagnostic emission path.
func TestNoDirectiveMutation(t *testing.T) {
	t1 := txn(t, 2024, 1, 1, "buy coffee", txnSpan1,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(t, 2024, 1, 1, "buy coffee", txnSpan2,
		posting(t, "Assets:Cash", "-3.50", "USD"),
		posting(t, "Expenses:Food", "3.50", "USD"),
	)
	dirs := []ast.Directive{t1, t2}

	snap1 := *t1
	snap2 := *t2

	in := api.Input{Directive: testPluginDir, Directives: seqOf(dirs)}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(snap1, *t1, astCmpOpts); diff != "" {
		t.Errorf("t1 mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(snap2, *t2, astCmpOpts); diff != "" {
		t.Errorf("t2 mutated (-want +got):\n%s", diff)
	}
}
