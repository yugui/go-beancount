package nounused

import (
	"context"
	"iter"
	"reflect"
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
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "n.beancount", Line: 1}}}

// openSpan is a non-zero span attached to Open directives so tests can
// distinguish "anchored at Open" from "fell back to plugin span".
var openSpan = ast.Span{Start: ast.Position{Filename: "n.beancount", Line: 100}}

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

// TestSingleOpenedAndUsed: an account that is opened and then referenced
// by a transaction posting produces no diagnostic.
func TestSingleOpenedAndUsed(t *testing.T) {
	openA := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    openSpan,
	}
	openI := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Income:Salary",
		Span:    openSpan,
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
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openI, tx})}

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

// TestSingleOpenedNotUsed: a single Open with no other directives yields
// exactly one diagnostic anchored at the Open's span and carrying the
// upstream-matching message verbatim.
func TestSingleOpenedNotUsed(t *testing.T) {
	openA := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    openSpan,
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []ast.Diagnostic{{Code: "unused-account", Span: openSpan}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	// Exact wording matches upstream's "Unused account '{}'" template.
	wantMsg := "Unused account 'Assets:Cash'"
	if got := res.Diagnostics[0].Message; got != wantMsg {
		t.Errorf("res.Diagnostics[0].Message = %q, want %q", got, wantMsg)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestMultipleUnused: three Opens, none referenced — three diagnostics in
// alphabetical account-name order.
func TestMultipleUnused(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	openB := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:Opening", Span: openSpan}
	openC := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary", Span: openSpan}
	// Input order intentionally non-alphabetical so the test exercises
	// the sort step, not just iteration order.
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openC, openA, openB})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Diagnostics) != 3 {
		t.Fatalf("len(res.Diagnostics) = %d, want 3; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	wantOrder := []string{
		"Unused account 'Assets:Cash'",
		"Unused account 'Equity:Opening'",
		"Unused account 'Income:Salary'",
	}
	for i, e := range res.Diagnostics {
		if e.Code != "unused-account" {
			t.Errorf("res.Diagnostics[%d].Code = %q, want %q", i, e.Code, "unused-account")
		}
		if e.Message != wantOrder[i] {
			t.Errorf("res.Diagnostics[%d].Message = %q, want %q (alphabetical order)", i, e.Message, wantOrder[i])
		}
	}
}

// TestPartiallyUsed: of five opened accounts only two are unused; the
// plugin emits exactly two diagnostics for the two unused.
func TestPartiallyUsed(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	openB := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Stale", Span: openSpan}
	openC := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:Forgotten", Span: openSpan}
	openD := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary", Span: openSpan}
	openE := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Expenses:Rent", Span: openSpan}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	pos2 := amt(50, "USD")
	neg2 := amt(-50, "USD")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Expenses:Rent", Amount: &pos2},
			{Account: "Assets:Cash", Amount: &neg2},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openB, openC, openD, openE, tx1, tx2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Diagnostics) != 2 {
		t.Fatalf("len(res.Diagnostics) = %d, want 2; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	want := []string{"Unused account 'Assets:Stale'", "Unused account 'Equity:Forgotten'"}
	for i, e := range res.Diagnostics {
		if e.Message != want[i] {
			t.Errorf("res.Diagnostics[%d].Message = %q, want %q", i, e.Message, want[i])
		}
	}
}

// TestUsedByBalanceCountsAsUsed: an Open followed by a Balance assertion
// on the same account counts as used; no diagnostic.
func TestUsedByBalanceCountsAsUsed(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan}
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  amt(0, "USD"),
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, bal})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (Balance counts as use); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestUsedByPadCountsAsUsed: a Pad directive contributes both Account
// and PadAccount to the used set. Two opens — the pad target and the pad
// source — are both reached by the single Pad directive.
func TestUsedByPadCountsAsUsed(t *testing.T) {
	openTarget := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Bank", Span: openSpan}
	openSource := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Equity:Opening", Span: openSpan}
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Bank",
		PadAccount: "Equity:Opening",
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openTarget, openSource, pad})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (Pad covers both Account and PadAccount); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestUsedByNoteCountsAsUsed: an Open followed by a Note on the same
// account counts as used; no diagnostic.
func TestUsedByNoteCountsAsUsed(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Liabilities:Loan", Span: openSpan}
	note := &ast.Note{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Liabilities:Loan",
		Comment: "first interest accrual",
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, note})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (Note counts as use); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestUsedByDocumentCountsAsUsed: an Open followed by a Document on the
// same account counts as used; no diagnostic.
func TestUsedByDocumentCountsAsUsed(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Vault", Span: openSpan}
	doc := &ast.Document{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Vault",
		Path:    "/tmp/receipt.pdf",
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, doc})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (Document counts as use); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestClosedButNeverUsed: an Open immediately followed by a Close on the
// same account, with no other intervening reference, counts as USED per
// upstream's documented semantics ("an account that is open and then
// closed is considered used"). The plugin emits no diagnostic; the
// behavior choice is documented in the package godoc.
func TestClosedButNeverUsed(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Old", Span: openSpan}
	closeA := &ast.Close{Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Old"}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, closeA})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (Close counts as use, matching upstream); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestCustomDoesNotCountAsUse: upstream classifies Custom under the "no
// accounts" handler; even when a Custom directive's Values list contains
// a MetaAccount entry the account is still considered unused. This
// matches upstream and parallels the autoaccounts port.
func TestCustomDoesNotCountAsUse(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Hidden", Span: openSpan}
	cust := &ast.Custom{
		Date:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		TypeName: "budget",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Hidden"},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, cust})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1 (Custom does not count as use); errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if got, want := res.Diagnostics[0].Message, "Unused account 'Assets:Hidden'"; got != want {
		t.Errorf("res.Diagnostics[0].Message = %q, want %q", got, want)
	}
}

// TestEmptyInput: an empty directive sequence yields a zero-valued
// Result — both Errors and Directives are nil.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Diagnostics != nil {
		t.Errorf("res.Diagnostics = %v, want nil for empty input", res.Diagnostics)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil for empty input", res.Directives)
	}
}

// TestNilDirectivesIterator: an Input with a nil Directives iterator is
// treated as empty input and yields the zero Result.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{Directives: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var zero api.Result
	if !cmp.Equal(res, zero) {
		t.Errorf("apply result = %#v, want zero api.Result", res)
	}
}

// TestCanceledContext: a pre-canceled context is observed at function
// entry; the plugin returns ctx.Err() and a zero Result without
// iterating.
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

// TestUnusedOpenWithZeroSpanFallsBackToTrigger: when the offending Open
// has a zero Span, the diagnostic anchors at the triggering Plugin
// directive's span instead. This exercises the fallback branch of
// diagSpan that the other tests bypass by setting a non-zero openSpan.
func TestUnusedOpenWithZeroSpanFallsBackToTrigger(t *testing.T) {
	openA := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		// Span deliberately left zero to trigger the fallback.
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if got, want := res.Diagnostics[0].Span, testPluginDir.Span; got != want {
		t.Errorf("res.Diagnostics[0].Span = %#v, want %#v (fallback to trigger span)", got, want)
	}
}

// TestNoDirectiveMutation: snapshot the input directives, run apply,
// and assert the directives are deep-equal to the snapshot afterward.
// The plugin is diagnostic-only and must never mutate input.
func TestNoDirectiveMutation(t *testing.T) {
	openA := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Cash", Span: openSpan, Currencies: []string{"USD"}}
	openB := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Income:Salary", Span: openSpan}
	openUnused := &ast.Open{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Stale", Span: openSpan}
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
	dirs := []ast.Directive{openA, openB, openUnused, tx}

	// Deep snapshot via reflect-deep clone-by-value of the directive
	// pointees. We retain the original pointers in `dirs` (so identity is
	// preserved for the apply call) and compare the value pointed to
	// before and after.
	snapshot := []ast.Directive{
		cloneOpen(openA), cloneOpen(openB), cloneOpen(openUnused), cloneTx(tx),
	}
	origPluginSpan := testPluginDir.Span

	in := api.Input{Directive: testPluginDir, Directives: seqOf(dirs)}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}

	for i, want := range snapshot {
		if !reflect.DeepEqual(want, dirs[i]) {
			t.Errorf("dirs[%d] mutated:\n  before: %#v\n  after:  %#v", i, want, dirs[i])
		}
	}
	if testPluginDir.Span != origPluginSpan {
		t.Errorf("apply mutated input plugin: testPluginDir.Span = %#v, want %#v", testPluginDir.Span, origPluginSpan)
	}
}

func cloneOpen(o *ast.Open) *ast.Open {
	cp := *o
	if o.Currencies != nil {
		cp.Currencies = append([]string(nil), o.Currencies...)
	}
	return &cp
}

func cloneTx(tx *ast.Transaction) *ast.Transaction {
	cp := *tx
	cp.Postings = append([]ast.Posting(nil), tx.Postings...)
	return &cp
}
