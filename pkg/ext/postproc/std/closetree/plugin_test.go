package closetree

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
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

// filterCloses returns only the *ast.Close directives in ds, preserving
// their order. Used by tests that focus on synthesized closes and want
// to ignore the surrounding directives.
func filterCloses(ds []ast.Directive) []*ast.Close {
	var out []*ast.Close
	for _, d := range ds {
		if c, ok := d.(*ast.Close); ok {
			out = append(out, c)
		}
	}
	return out
}

// closeAccounts returns the Account field of each Close in cs, in
// order. Useful for asserting on which accounts were closed without
// caring about Spans, Dates, or Metadata.
func closeAccounts(cs []*ast.Close) []ast.Account {
	out := make([]ast.Account, len(cs))
	for i, c := range cs {
		out[i] = c.Account
	}
	return out
}

// TestSimpleSubtreeClose: a parent Close with two opened child
// accounts synthesizes two Close directives, one per child.
func TestSimpleSubtreeClose(t *testing.T) {
	open1 := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	open2 := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:ORNG",
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{open1, open2, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatalf("res.Directives = nil, want non-nil (synthesized closes expected)")
	}
	wantCloses := []ast.Account{
		"Assets:Brokerage",      // original
		"Assets:Brokerage:AAPL", // synthesized (alphabetical)
		"Assets:Brokerage:ORNG", // synthesized
	}
	if diff := cmp.Diff(wantCloses, closeAccounts(filterCloses(res.Directives))); diff != "" {
		t.Errorf("Close accounts mismatch (-want +got):\n%s", diff)
	}
	// Synthesized closes must inherit the parent's Date.
	for _, c := range filterCloses(res.Directives) {
		if !c.Date.Equal(closeDir.Date) {
			t.Errorf("Close{Account:%q}.Date = %v, want %v", c.Account, c.Date, closeDir.Date)
		}
	}
}

// TestNoChildrenNoSynthesis: a Close on a leaf account with no
// descendants synthesizes nothing. Per the Result contract a no-op
// pass returns nil Directives so the runner does not replace the
// ledger.
func TestNoChildrenNoSynthesis(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{op, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for no-op pass", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
	}
}

// TestExplicitChildCloseRespected: when a child has been explicitly
// Close'd before the parent Close, the plugin does not double-close.
// Only siblings without an explicit Close are synthesized.
func TestExplicitChildCloseRespected(t *testing.T) {
	open1 := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	open2 := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:ORNG",
	}
	explicitClose := &ast.Close{
		Date:    time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	parentClose := &ast.Close{
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{open1, open2, explicitClose, parentClose})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotAccounts := closeAccounts(filterCloses(res.Directives))
	want := []ast.Account{
		"Assets:Brokerage:AAPL", // explicit (preserved)
		"Assets:Brokerage",      // parent (preserved)
		"Assets:Brokerage:ORNG", // synthesized (the only un-closed sibling)
	}
	if diff := cmp.Diff(want, gotAccounts); diff != "" {
		t.Errorf("Close accounts mismatch (-want +got):\n%s", diff)
	}
}

// TestNestedSubtree: closing an outer parent synthesizes Closes for
// every descendant at every depth, not just direct children.
func TestNestedSubtree(t *testing.T) {
	opens := []ast.Directive{
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Brokerage:US"},
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Brokerage:US:AAPL"},
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:Brokerage:US:AAPL:Lots"},
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	input := append(append([]ast.Directive{}, opens...), closeDir)
	in := api.Input{Directives: seqOf(input)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotAccounts := closeAccounts(filterCloses(res.Directives))
	// Original first, synthesized in alphabetical order.
	want := []ast.Account{
		"Assets:Brokerage",
		"Assets:Brokerage:US",
		"Assets:Brokerage:US:AAPL",
		"Assets:Brokerage:US:AAPL:Lots",
	}
	if diff := cmp.Diff(want, gotAccounts); diff != "" {
		t.Errorf("Close accounts mismatch (-want +got):\n%s", diff)
	}
}

// TestParentClosedBeforeChildOpened: a child Open'ed strictly after
// the parent Close is NOT synthesized — emitting a Close that predates
// its own Open would produce an invalid ledger. This is a documented
// deviation from upstream.
func TestParentClosedBeforeChildOpened(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:Existing",
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	// This Open appears after the parent Close in source order AND
	// has a date that is strictly after the parent Close's date.
	// Both guards must reject it.
	lateOpen := &ast.Open{
		Date:    time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:Late",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{op, closeDir, lateOpen})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotAccounts := closeAccounts(filterCloses(res.Directives))
	want := []ast.Account{
		"Assets:Brokerage",          // original
		"Assets:Brokerage:Existing", // synthesized (existed at close time)
		// Assets:Brokerage:Late is intentionally absent.
	}
	if diff := cmp.Diff(want, gotAccounts); diff != "" {
		t.Errorf("Close accounts mismatch (-want +got):\n%s", diff)
	}
}

// TestSimilarPrefixIsNotDescendant: `Assets:CashFlow` has the string
// prefix `Assets:Cash` but is NOT a component-child of `Assets:Cash`.
// Closing `Assets:Cash` must not synthesize a Close for
// `Assets:CashFlow`. This is a documented deviation from upstream's
// raw-prefix behavior.
func TestSimilarPrefixIsNotDescendant(t *testing.T) {
	openCash := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	openCashFlow := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:CashFlow",
	}
	closeCash := &ast.Close{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openCash, openCashFlow, closeCash})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		// A no-op pass: Cash has no real descendants, CashFlow is a
		// sibling of Cash, not a child.
		gotAccounts := closeAccounts(filterCloses(res.Directives))
		for _, a := range gotAccounts {
			if a == "Assets:CashFlow" {
				t.Errorf("synthesized Close for Assets:CashFlow; it is a sibling of Assets:Cash, not a descendant")
			}
		}
	}
}

// TestEmptyInput: an Input with no directives yields a Result with
// nil Directives (the no-change signal). Mirrors implicitprices.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directives: seqOf(nil)}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for empty input", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
	}
}

// TestNilDirectivesIterator: an Input with nil Directives is also a
// no-op and must not panic.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
	}
}

// TestCanceledContext: the plugin honors context cancellation at
// entry. The exact error returned mirrors ctx.Err().
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
	if err != ctx.Err() {
		t.Errorf("apply error = %v, want ctx.Err() = %v", err, ctx.Err())
	}
}

// TestNoDirectiveMutation: input directives must not be mutated.
func TestNoDirectiveMutation(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	parentClose := &ast.Close{
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	origOpenAcct := op.Account
	origOpenDate := op.Date
	origCloseAcct := parentClose.Account
	origCloseDate := parentClose.Date

	in := api.Input{Directives: seqOf([]ast.Directive{op, parentClose})}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if op.Account != origOpenAcct {
		t.Errorf("apply mutated Open.Account: %q -> %q", origOpenAcct, op.Account)
	}
	if !op.Date.Equal(origOpenDate) {
		t.Errorf("apply mutated Open.Date: %v -> %v", origOpenDate, op.Date)
	}
	if parentClose.Account != origCloseAcct {
		t.Errorf("apply mutated Close.Account: %q -> %q", origCloseAcct, parentClose.Account)
	}
	if !parentClose.Date.Equal(origCloseDate) {
		t.Errorf("apply mutated Close.Date: %v -> %v", origCloseDate, parentClose.Date)
	}
}

// TestExactDateOnSynthesizedClose: a synthesized Close must have the
// same Date as the parent Close that triggered it — this is the
// upstream contract that lets ledger consumers treat the parent Close
// as the canonical authoritative date.
func TestExactDateOnSynthesizedClose(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	parentClose := &ast.Close{
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{op, parentClose})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	closes := filterCloses(res.Directives)
	if len(closes) != 2 {
		t.Fatalf("len(closes) = %d, want 2 (original + synthesized); closes = %#v", len(closes), closes)
	}
	// Find the synthesized one (Account != parentClose.Account).
	var synth *ast.Close
	for _, c := range closes {
		if c.Account != parentClose.Account {
			synth = c
			break
		}
	}
	if synth == nil {
		t.Fatalf("no synthesized Close found; closes = %#v", closes)
	}
	if !synth.Date.Equal(parentClose.Date) {
		t.Errorf("synth.Date = %v, want %v (parent's date)", synth.Date, parentClose.Date)
	}
	if !synth.Date.Equal(time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("synth.Date = %v, want 2024-11-10 (literal)", synth.Date)
	}
}

// TestExactSpanOnSynthesizedClose: each synthesized Close inherits the
// parent Close's Span so balance-mismatch and unused-account
// diagnostics on the synthesized account anchor at the user-authored
// close line — the most actionable location.
func TestExactSpanOnSynthesizedClose(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	parentSpan := ast.Span{
		Start: ast.Position{Filename: "ledger.beancount", Line: 42, Column: 1},
		End:   ast.Position{Filename: "ledger.beancount", Line: 42, Column: 30},
	}
	parentClose := &ast.Close{
		Span:    parentSpan,
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{op, parentClose})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, c := range filterCloses(res.Directives) {
		if c.Account == parentClose.Account {
			continue // original
		}
		if c.Span != parentSpan {
			t.Errorf("synthesized Close{Account:%q}.Span = %+v, want %+v (parent's span)", c.Account, c.Span, parentSpan)
		}
	}
}

// TestPreservesAllOriginalDirectives: every input directive — Open,
// Close, and any other kind — appears in the output slice in addition
// to the synthesized Closes, by pointer identity.
func TestPreservesAllOriginalDirectives(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage:AAPL",
	}
	note := &ast.Note{
		Date:    time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
		Comment: "winding down",
	}
	parentClose := &ast.Close{
		Date:    time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Brokerage",
	}
	input := []ast.Directive{op, note, parentClose}
	in := api.Input{Directives: seqOf(input)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(len(filterCloses(res.Directives)), 2); diff != "" {
		t.Errorf("synthesized close count diff: %s", diff)
	}
	for _, want := range input {
		found := false
		for _, got := range res.Directives {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("input directive %T %p not preserved in output", want, want)
		}
	}
}

// TestTwoParentsBothClosed: two sibling parents each spawn their own
// subtree closures independently. The set-tracking logic must prevent
// a descendant of one parent from leaking into the other parent's
// synthesis.
func TestTwoParentsBothClosed(t *testing.T) {
	opens := []ast.Directive{
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:A:Child1"},
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:A:Child2"},
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:B:Child1"},
		&ast.Open{Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Account: "Assets:B:Child2"},
	}
	closeA := &ast.Close{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:A",
	}
	closeB := &ast.Close{
		Date:    time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:B",
	}
	input := append(append([]ast.Directive{}, opens...), closeA, closeB)
	in := api.Input{Directives: seqOf(input)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotAccounts := closeAccounts(filterCloses(res.Directives))
	want := []ast.Account{
		"Assets:A",
		"Assets:A:Child1",
		"Assets:A:Child2",
		"Assets:B",
		"Assets:B:Child1",
		"Assets:B:Child2",
	}
	if diff := cmp.Diff(want, gotAccounts); diff != "" {
		t.Errorf("Close accounts mismatch (-want +got):\n%s", diff)
	}
}
