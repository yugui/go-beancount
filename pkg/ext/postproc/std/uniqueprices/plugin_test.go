package uniqueprices

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

// errorCmpOpts compares api.Error values structurally while leaving the
// human-readable Message field to per-test substring assertions.
var errorCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(api.Error{}, "Message"),
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
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "u.beancount", Line: 1}}}

// priceSpan1/priceSpan2/priceSpan3 are distinct non-zero spans attached
// to Price directives so tests can assert each diagnostic anchors at
// the offending Price's own span and not at the plugin fallback.
var (
	priceSpan1 = ast.Span{Start: ast.Position{Filename: "u.beancount", Line: 100}}
	priceSpan2 = ast.Span{Start: ast.Position{Filename: "u.beancount", Line: 200}}
	priceSpan3 = ast.Span{Start: ast.Position{Filename: "u.beancount", Line: 300}}
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
// Used so tests can express prices like "1.5" or "1.50" naturally,
// including representations whose internal exponents differ but whose
// numeric values are equal (the upstream-defined "duplicate, not
// conflict" case).
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("apd.SetString(%q): %v", s, err)
	}
	return d
}

// price builds a Price directive from a date, base/quote currencies, a
// numeric value (parsed via dec), and a span. Centralising construction
// keeps tests focused on the conflict scenarios under test.
func price(t *testing.T, year, month, day int, base, quote, value string, span ast.Span) *ast.Price {
	t.Helper()
	return &ast.Price{
		Date:      time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC),
		Commodity: base,
		Amount:    ast.Amount{Number: dec(t, value), Currency: quote},
		Span:      span,
	}
}

// TestNoConflicts: a single Price directive on a triple produces no
// diagnostic.
func TestNoConflicts(t *testing.T) {
	p := price(t, 2024, 1, 1, "HOOL", "USD", "100", priceSpan1)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p})}

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

// TestDuplicateSameValue: two Price directives on the same triple with
// numerically equal values (including representations like "1.5" vs
// "1.50") are treated as duplicates and produce no diagnostic, matching
// upstream's explicit "same number is not an error" carve-out.
func TestDuplicateSameValue(t *testing.T) {
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "1.5", priceSpan1)
	p2 := price(t, 2024, 1, 1, "HOOL", "USD", "1.50", priceSpan2)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p1, p2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (numerically-equal duplicates); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestConflictingValues: two Price directives on the same triple with
// different values produce one diagnostic anchored at the second
// (offending) Price. The exact message is asserted to lock the
// human-readable contract.
func TestConflictingValues(t *testing.T) {
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	p2 := price(t, 2024, 1, 1, "HOOL", "USD", "100", priceSpan2)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p1, p2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []api.Error{{Code: codeDuplicatePrice, Span: priceSpan2}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "Disagreeing price for HOOL/USD on 2024-01-01: 99 vs 100"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestMultipleConflictsAcrossPairs: two distinct triples, each with its
// own conflict, produce two independent diagnostics in source-encounter
// order.
func TestMultipleConflictsAcrossPairs(t *testing.T) {
	span4 := ast.Span{Start: ast.Position{Filename: "u.beancount", Line: 400}}
	a1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	b1 := price(t, 2024, 1, 1, "AAPL", "USD", "150", priceSpan2)
	a2 := price(t, 2024, 1, 1, "HOOL", "USD", "100", priceSpan3)
	b2 := price(t, 2024, 1, 1, "AAPL", "USD", "151", span4)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{a1, b1, a2, b2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Source-encounter order: a2 (line 300) then b2 (line 400).
	want := []api.Error{
		{Code: codeDuplicatePrice, Span: priceSpan3},
		{Code: codeDuplicatePrice, Span: span4},
	}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestThreeWayConflict: three Price directives on the same triple with
// three different values produce two diagnostics — one for each Price
// after the first whose value differs from the first (the documented
// per-directive policy).
func TestThreeWayConflict(t *testing.T) {
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	p2 := price(t, 2024, 1, 1, "HOOL", "USD", "100", priceSpan2)
	p3 := price(t, 2024, 1, 1, "HOOL", "USD", "101", priceSpan3)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p1, p2, p3})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []api.Error{
		{Code: codeDuplicatePrice, Span: priceSpan2},
		{Code: codeDuplicatePrice, Span: priceSpan3},
	}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestSameTripleDifferentDayNoConflict: the same base/quote pair on
// different days does not collide; each day has its own group.
func TestSameTripleDifferentDayNoConflict(t *testing.T) {
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	p2 := price(t, 2024, 1, 2, "HOOL", "USD", "100", priceSpan2)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p1, p2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (different days); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestDifferentBaseSameDayNoConflict: distinct base/quote permutations
// on the same day do not collide. (HOOL/USD vs AAPL/USD are different
// triples; HOOL/USD vs USD/HOOL are different triples too.)
func TestDifferentBaseSameDayNoConflict(t *testing.T) {
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	p2 := price(t, 2024, 1, 1, "AAPL", "USD", "100", priceSpan2)
	p3 := price(t, 2024, 1, 1, "USD", "HOOL", "0.01", priceSpan3)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p1, p2, p3})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (distinct triples); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestSpanAnchoring: when the offending Price has a non-zero Span the
// diagnostic anchors at it; when it has a zero Span the diagnostic
// falls back to the triggering plugin directive's Span.
func TestSpanAnchoring(t *testing.T) {
	// Non-zero span path.
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	p2 := price(t, 2024, 1, 1, "HOOL", "USD", "100", priceSpan2)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{p1, p2})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if got := res.Errors[0].Span; got != priceSpan2 {
		t.Errorf("res.Errors[0].Span = %#v, want priceSpan2 %#v", got, priceSpan2)
	}

	// Zero-span path: the offending Price has a zero Span, so the
	// diagnostic falls back to the plugin directive's span.
	q1 := price(t, 2024, 2, 1, "HOOL", "USD", "99", priceSpan1)
	q2 := price(t, 2024, 2, 1, "HOOL", "USD", "100", ast.Span{}) // zero
	in2 := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{q1, q2})}

	res2, err := apply(context.Background(), in2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res2.Errors) != 1 {
		t.Fatalf("len(res2.Errors) = %d, want 1; errors = %v", len(res2.Errors), res2.Errors)
	}
	if got := res2.Errors[0].Span; got != testPluginDir.Span {
		t.Errorf("res2.Errors[0].Span = %#v, want testPluginDir.Span %#v (fallback)", got, testPluginDir.Span)
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
	if res.Errors != nil {
		t.Errorf("res.Errors = %v, want nil for empty input", res.Errors)
	}
}

// TestNilDirectivesIterator: an Input whose Directives field is nil is
// treated as an empty ledger, returning a zero Result.
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

// TestNoDirectiveMutation: the plugin is diagnostic-only; it must not
// mutate the directives it observes. Asserted by snapshotting the
// pre-apply state against the post-apply state.
func TestNoDirectiveMutation(t *testing.T) {
	p1 := price(t, 2024, 1, 1, "HOOL", "USD", "99", priceSpan1)
	p2 := price(t, 2024, 1, 1, "HOOL", "USD", "100", priceSpan2)
	dirs := []ast.Directive{p1, p2}

	// Each *ast.Price's value snapshot is captured before apply. The
	// plugin must leave the underlying structs unchanged.
	snap1 := *p1
	snap2 := *p2

	in := api.Input{Directive: testPluginDir, Directives: seqOf(dirs)}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(snap1, *p1, astCmpOpts); diff != "" {
		t.Errorf("p1 mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(snap2, *p2, astCmpOpts); diff != "" {
		t.Errorf("p2 mutated (-want +got):\n%s", diff)
	}
}
