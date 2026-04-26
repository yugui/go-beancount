package excludetag

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// astCmpOpts is the standard option set for comparing AST values
// produced by the plugin. apd.Decimal carries an internal
// representation (BigInt with unexported fields) that cmp.Diff cannot
// inspect by default, and time.Time has unexported monotonic-clock
// state — both need a custom comparer that defers to the type's own
// equality semantics.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

// seqOf wraps a slice of directives in the iter.Seq2 shape that
// api.Input.Directives expects. The yield-closure form mirrors the
// helper used by closetree's tests so the contract under test is
// exercised exactly the way the runner exercises it.
func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

// txWithTags builds a minimal Transaction with the given tags and a
// fixed date — content beyond Date and Tags is irrelevant for the
// plugin, which only inspects Tags.
func txWithTags(day int, tags ...string) *ast.Transaction {
	return &ast.Transaction{
		Date: time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC),
		Tags: tags,
	}
}

// TestSingleTransactionWithTagDropped: a lone transaction carrying the
// default tag is removed, leaving an empty (non-nil) directive list.
// The non-nil empty slice is the runner-visible "clear the ledger"
// signal per the Result.Directives contract.
func TestSingleTransactionWithTagDropped(t *testing.T) {
	tx := txWithTags(1, "virtual")
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatalf("res.Directives = nil, want non-nil empty slice (filtering occurred)")
	}
	if len(res.Directives) != 0 {
		t.Errorf("len(res.Directives) = %d, want 0", len(res.Directives))
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestTransactionWithoutTagPreserved: a transaction without the
// configured tag is a no-op for the plugin, which signals "no change"
// by returning a nil Directives slice.
func TestTransactionWithoutTagPreserved(t *testing.T) {
	tx := txWithTags(1, "trip-2024")
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for no-op pass", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestMixedDropped: with five transactions of which two carry the
// configured tag, the result preserves the three that do not in their
// original source order.
func TestMixedDropped(t *testing.T) {
	keep1 := txWithTags(1, "trip")
	drop1 := txWithTags(2, "virtual")
	keep2 := txWithTags(3) // no tags at all
	drop2 := txWithTags(4, "other", "virtual")
	keep3 := txWithTags(5, "tax")
	in := api.Input{Directives: seqOf([]ast.Directive{keep1, drop1, keep2, drop2, keep3})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatalf("res.Directives = nil, want non-nil (filtering occurred)")
	}
	want := []ast.Directive{keep1, keep2, keep3}
	if diff := cmp.Diff(want, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestNonTransactionDirectivesPreserved: directive kinds other than
// Transaction are never inspected for tags and always pass through —
// even when filtering is active. The Open/Balance/Price chosen here is
// a representative cross-section of the non-transaction kinds.
func TestNonTransactionDirectivesPreserved(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Amount:  ast.Amount{Number: *apd.New(100, 0), Currency: "USD"},
	}
	pr := &ast.Price{
		Date:      time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Commodity: "AAPL",
		Amount:    ast.Amount{Number: *apd.New(150, 0), Currency: "USD"},
	}
	dropTx := txWithTags(1, "virtual")
	keepTx := txWithTags(2, "trip")
	in := api.Input{Directives: seqOf([]ast.Directive{op, dropTx, bal, keepTx, pr})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Directive{op, bal, keepTx, pr}
	if diff := cmp.Diff(want, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigOverridesDefault: a non-empty Config replaces the default
// "virtual" — a transaction carrying the configured tag is dropped,
// and a transaction carrying the previous default ("virtual") is now
// preserved because the default no longer applies.
func TestConfigOverridesDefault(t *testing.T) {
	dropTx := txWithTags(1, "draft")
	keepTx := txWithTags(2, "virtual") // would have been dropped under default
	in := api.Input{
		Directives: seqOf([]ast.Directive{dropTx, keepTx}),
		Config:     "draft",
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Directive{keepTx}
	if diff := cmp.Diff(want, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestCaseMismatch: tag matching is case-sensitive (matching upstream
// and the codebase-wide convention for tags). A Config of "Foo" does
// not drop a transaction tagged "foo".
func TestCaseMismatch(t *testing.T) {
	tx := txWithTags(1, "foo")
	in := api.Input{
		Directives: seqOf([]ast.Directive{tx}),
		Config:     "Foo",
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil (case-sensitive: Foo != foo)", res.Directives)
	}
}

// TestSubstringNotMatch: tag membership is full-string equality, not
// substring containment. A Config of "car" does not drop a transaction
// tagged "carpool".
func TestSubstringNotMatch(t *testing.T) {
	tx := txWithTags(1, "carpool")
	in := api.Input{
		Directives: seqOf([]ast.Directive{tx}),
		Config:     "car",
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil (membership, not substring)", res.Directives)
	}
}

// TestEmptyInput: an Input with an empty directive iterator is a
// no-op. Returning nil Directives is the no-change signal honoured by
// the runner; a non-nil empty slice would (incorrectly) clear the
// ledger.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directives: seqOf(nil)}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for empty input", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestNilDirectivesIterator: an Input with a nil Directives field
// (zero-valued Input) is also a no-op and must not panic.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0", len(res.Errors))
	}
}

// TestCanceledContext: the plugin honours context cancellation at
// entry and surfaces ctx.Err() unchanged so callers can compare with
// errors.Is.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{Directives: seqOf([]ast.Directive{txWithTags(1, "virtual")})})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
	if err != ctx.Err() {
		t.Errorf("apply error = %v, want ctx.Err() = %v", err, ctx.Err())
	}
}

// TestNoDirectiveMutation: input directives are passed through by
// pointer identity, never mutated. The Tags slice in particular is
// observed (read-only) and must keep its original contents and order.
func TestNoDirectiveMutation(t *testing.T) {
	keepTx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Tags: []string{"trip", "tax"},
	}
	dropTx := &ast.Transaction{
		Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Tags: []string{"virtual"},
	}
	origKeepTags := append([]string(nil), keepTx.Tags...)
	origDropTags := append([]string(nil), dropTx.Tags...)
	origKeepDate := keepTx.Date
	origDropDate := dropTx.Date

	in := api.Input{Directives: seqOf([]ast.Directive{keepTx, dropTx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(origKeepTags, keepTx.Tags); diff != "" {
		t.Errorf("apply mutated keepTx.Tags (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(origDropTags, dropTx.Tags); diff != "" {
		t.Errorf("apply mutated dropTx.Tags (-want +got):\n%s", diff)
	}
	if !keepTx.Date.Equal(origKeepDate) {
		t.Errorf("apply mutated keepTx.Date: %v -> %v", origKeepDate, keepTx.Date)
	}
	if !dropTx.Date.Equal(origDropDate) {
		t.Errorf("apply mutated dropTx.Date: %v -> %v", origDropDate, dropTx.Date)
	}
	// The kept directive must appear in the output by pointer identity
	// — the plugin must never copy directives.
	if len(res.Directives) != 1 || res.Directives[0] != ast.Directive(keepTx) {
		t.Errorf("res.Directives = %#v, want exactly [keepTx] by identity", res.Directives)
	}
}

// TestDefaultTagWhenConfigEmpty: an empty Config selects the upstream
// default tag ("virtual"), so a transaction tagged "virtual" is
// dropped while one tagged with anything else is kept.
func TestDefaultTagWhenConfigEmpty(t *testing.T) {
	drop := txWithTags(1, "virtual")
	keep := txWithTags(2, "draft")
	in := api.Input{
		Directives: seqOf([]ast.Directive{drop, keep}),
		Config:     "",
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Directive{keep}
	if diff := cmp.Diff(want, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("Directives mismatch (-want +got):\n%s", diff)
	}
}
