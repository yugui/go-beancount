package coherentcost

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
// human-readable Message field to per-test substring or byte-exact
// assertions.
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
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "c.beancount", Line: 1}}}

// openSpanA / openSpanB are distinct non-zero spans attached to Open
// directives so tests can assert "anchored at Open" vs the plugin
// fallback.
var (
	openSpanA = ast.Span{Start: ast.Position{Filename: "c.beancount", Line: 100}}
	openSpanB = ast.Span{Start: ast.Position{Filename: "c.beancount", Line: 200}}
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

// amt builds an Amount value from a textual number and a currency.
func amt(t *testing.T, number, currency string) ast.Amount {
	t.Helper()
	return ast.Amount{Number: dec(t, number), Currency: currency}
}

// withCostPosting builds a Posting with a non-nil Amount and a non-nil
// CostSpec carrying a per-unit cost. The exact cost numbers are not
// asserted by the plugin; only Cost-non-nil-ness matters.
func withCostPosting(t *testing.T, account, number, currency, costNumber, costCurrency string) ast.Posting {
	t.Helper()
	a := amt(t, number, currency)
	c := amt(t, costNumber, costCurrency)
	return ast.Posting{
		Account: ast.Account(account),
		Amount:  &a,
		Cost:    &ast.CostSpec{PerUnit: &c},
	}
}

// withoutCostPosting builds a Posting with a non-nil Amount and a nil
// Cost (the default zero value for that field).
func withoutCostPosting(t *testing.T, account, number, currency string) ast.Posting {
	t.Helper()
	a := amt(t, number, currency)
	return ast.Posting{
		Account: ast.Account(account),
		Amount:  &a,
	}
}

// nilAmountPosting builds a Posting with a nil Amount (the auto-balance
// idiom used on a transaction's residual leg).
func nilAmountPosting(account string) ast.Posting {
	return ast.Posting{Account: ast.Account(account)}
}

// txn assembles a Transaction directive from the supplied postings.
func txn(year, month, day int, postings ...ast.Posting) *ast.Transaction {
	return &ast.Transaction{
		Date:     time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC),
		Flag:     '*',
		Postings: postings,
	}
}

// openOf builds an Open directive for the named account with the given
// span.
func openOf(account string, span ast.Span) *ast.Open {
	return &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: ast.Account(account),
		Span:    span,
	}
}

// TestAllWithCost: a single account whose every posting carries a cost
// produces no diagnostic — the (Account, Commodity) pair is only ever
// observed in the with-cost form.
func TestAllWithCost(t *testing.T) {
	open := openOf("Assets:Inv", openSpanA)
	t1 := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-1500.00", "USD"),
	)
	t2 := txn(2024, 2, 1,
		withCostPosting(t, "Assets:Inv", "5", "AAPL", "160.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-800.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, t1, t2})}

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

// TestAllWithoutCost: a single account whose postings carry no cost
// annotations produces no diagnostic.
func TestAllWithoutCost(t *testing.T) {
	open := openOf("Assets:Cash", openSpanA)
	t1 := txn(2024, 1, 1,
		withoutCostPosting(t, "Assets:Cash", "-3.50", "USD"),
		withoutCostPosting(t, "Expenses:Food", "3.50", "USD"),
	)
	t2 := txn(2024, 1, 2,
		withoutCostPosting(t, "Assets:Cash", "-9.00", "USD"),
		withoutCostPosting(t, "Expenses:Food", "9.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, t1, t2})}

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

// TestMixedSameCommodity: one (Account, Commodity) pair observed in
// both the with-cost and without-cost forms produces exactly one
// diagnostic. The exact message is asserted to lock the human-readable
// contract of this port (which deliberately diverges from upstream's
// per-currency wording — see doc.go).
func TestMixedSameCommodity(t *testing.T) {
	open := openOf("Assets:Inv", openSpanA)
	tWith := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-1500.00", "USD"),
	)
	tWithout := txn(2024, 2, 1,
		withoutCostPosting(t, "Assets:Inv", "5", "AAPL"),
		withoutCostPosting(t, "Income:Gift", "-5", "AAPL"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, tWith, tWithout})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []api.Error{{Code: codeIncoherentCost, Span: openSpanA}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	wantMsg := "Account 'Assets:Inv' holds 'AAPL' both with and without a cost"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestMixedDifferentCommodities: a single account holding AAPL with a
// cost basis and USD without one produces no diagnostic — the rule
// keys per (Account, Commodity), so distinct commodities are
// independent. This is the canonical brokerage shape (positions held
// at cost; cash legs without cost) and must not be flagged.
func TestMixedDifferentCommodities(t *testing.T) {
	open := openOf("Assets:Inv", openSpanA)
	tBuy := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Inv", "-1500.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, tBuy})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (different commodities are independent); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestMultipleOffenders: two distinct (Account, Commodity) pairs each
// observed in both forms produce two diagnostics in alphabetical order
// by (Account, Commodity).
func TestMultipleOffenders(t *testing.T) {
	openA := openOf("Assets:Broker", openSpanA)
	openB := openOf("Assets:Inv", openSpanB)

	// Broker: GOOG seen with-cost then without-cost.
	t1 := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Broker", "5", "GOOG", "100.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-500.00", "USD"),
	)
	t2 := txn(2024, 2, 1,
		withoutCostPosting(t, "Assets:Broker", "1", "GOOG"),
		withoutCostPosting(t, "Income:Gift", "-1", "GOOG"),
	)
	// Inv: AAPL seen with-cost then without-cost.
	t3 := txn(2024, 3, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-1500.00", "USD"),
	)
	t4 := txn(2024, 4, 1,
		withoutCostPosting(t, "Assets:Inv", "2", "AAPL"),
		withoutCostPosting(t, "Income:Gift", "-2", "AAPL"),
	)

	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openA, openB, t1, t2, t3, t4})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []api.Error{
		{Code: codeIncoherentCost, Span: openSpanA}, // Assets:Broker, GOOG
		{Code: codeIncoherentCost, Span: openSpanB}, // Assets:Inv, AAPL
	}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestNilAmountSkipped: an auto-balanced posting (Amount nil) carries
// no currency and must contribute to neither set; the plugin must not
// crash when it encounters one. The rest of the transaction supplies
// only with-cost AAPL, so no diagnostic is produced.
func TestNilAmountSkipped(t *testing.T) {
	open := openOf("Assets:Inv", openSpanA)
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
			nilAmountPosting("Assets:Cash"), // residual leg, balances the lot purchase
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (nil-Amount postings are skipped); errors = %v", len(res.Errors), res.Errors)
	}
}

// TestSpanAnchoring: when the offending account has an Open with a
// non-zero Span the diagnostic anchors at it; when no Open exists the
// diagnostic falls back to the triggering plugin directive's Span.
func TestSpanAnchoring(t *testing.T) {
	// Anchored-at-Open path.
	open := openOf("Assets:Inv", openSpanA)
	tWith := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-1500.00", "USD"),
	)
	tWithout := txn(2024, 2, 1,
		withoutCostPosting(t, "Assets:Inv", "5", "AAPL"),
		withoutCostPosting(t, "Income:Gift", "-5", "AAPL"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, tWith, tWithout})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if got := res.Errors[0].Span; got != openSpanA {
		t.Errorf("res.Errors[0].Span = %#v, want openSpanA %#v", got, openSpanA)
	}

	// Fallback-to-plugin-span path: same offense, no Open in the input.
	in2 := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tWith, tWithout})}
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
// Result with no errors and no replacement directives.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Errors != nil {
		t.Errorf("res.Errors = %v, want nil for empty input", res.Errors)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil for empty input", res.Directives)
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
// mutate the directives it observes. Asserted by snapshotting pre-apply
// state against post-apply state for an offending input that exercises
// the diagnostic emission path.
func TestNoDirectiveMutation(t *testing.T) {
	open := openOf("Assets:Inv", openSpanA)
	tWith := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-1500.00", "USD"),
	)
	tWithout := txn(2024, 2, 1,
		withoutCostPosting(t, "Assets:Inv", "5", "AAPL"),
		withoutCostPosting(t, "Income:Gift", "-5", "AAPL"),
	)

	snapOpen := *open
	snapWith := *tWith
	snapWithout := *tWithout

	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{open, tWith, tWithout})}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(snapOpen, *open, astCmpOpts); diff != "" {
		t.Errorf("open mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(snapWith, *tWith, astCmpOpts); diff != "" {
		t.Errorf("tWith mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(snapWithout, *tWithout, astCmpOpts); diff != "" {
		t.Errorf("tWithout mutated (-want +got):\n%s", diff)
	}
}

// TestExactMessageAssertion: locks the byte-exact diagnostic message
// for an offender even when the offender shares the ledger with
// non-offending pairs (different commodities under the same account)
// and other accounts. The assertion guards against accidental message
// drift such as quote-character or pluralization changes.
func TestExactMessageAssertion(t *testing.T) {
	openInv := openOf("Assets:Inv", openSpanA)
	openCash := openOf("Assets:Cash", openSpanB)

	tBuy := txn(2024, 1, 1,
		withCostPosting(t, "Assets:Inv", "10", "AAPL", "150.00", "USD"),
		withoutCostPosting(t, "Assets:Cash", "-1500.00", "USD"),
	)
	// Same account, same commodity, but no cost — turns AAPL into a
	// mixed (Assets:Inv, AAPL) pair.
	tGift := txn(2024, 2, 1,
		withoutCostPosting(t, "Assets:Inv", "1", "AAPL"),
		withoutCostPosting(t, "Income:Gift", "-1", "AAPL"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{openInv, openCash, tBuy, tGift})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	wantMsg := "Account 'Assets:Inv' holds 'AAPL' both with and without a cost"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
}
