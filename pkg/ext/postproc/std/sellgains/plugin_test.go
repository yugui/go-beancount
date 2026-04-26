package sellgains

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// errorCmpOpts compares api.Error values structurally while leaving
// the human-readable Message field to per-test substring or byte-exact
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
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "s.beancount", Line: 1}}}

// txnSpan is a distinct non-zero span attached to test Transaction
// directives so tests can assert that diagnostics anchor at the
// Transaction rather than at the plugin fallback.
var txnSpan = ast.Span{Start: ast.Position{Filename: "s.beancount", Line: 100}}

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

// amtPtr builds a pointer to an Amount value.
func amtPtr(t *testing.T, number, currency string) *ast.Amount {
	t.Helper()
	a := amt(t, number, currency)
	return &a
}

// salePosting builds an at-cost, at-price posting representing the
// asset leg of a sale: negative units, with a {cost} and an @ price.
func salePosting(t *testing.T, account, units, unitsCur, costPerUnit, costCur, price, priceCur string) ast.Posting {
	t.Helper()
	a := amt(t, units, unitsCur)
	c := amt(t, costPerUnit, costCur)
	pr := amt(t, price, priceCur)
	return ast.Posting{
		Account: ast.Account(account),
		Amount:  &a,
		Cost:    &ast.CostSpec{PerUnit: &c},
		Price:   &ast.PriceAnnotation{Amount: pr},
	}
}

// plainPosting builds a posting with just an amount, no cost, no price.
func plainPosting(t *testing.T, account, number, currency string) ast.Posting {
	t.Helper()
	a := amt(t, number, currency)
	return ast.Posting{Account: ast.Account(account), Amount: &a}
}

// nilAmountPosting builds a posting with a nil Amount (the residual
// auto-balanced leg). Used for the elided-Income-amount case.
func nilAmountPosting(account string) ast.Posting {
	return ast.Posting{Account: ast.Account(account)}
}

// txn assembles a Transaction directive with txnSpan attached so
// diagnostic anchoring can be asserted.
func txn(year, month, day int, postings ...ast.Posting) *ast.Transaction {
	return &ast.Transaction{
		Span:     txnSpan,
		Date:     time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC),
		Flag:     '*',
		Postings: postings,
	}
}

// TestCleanSale: a textbook sale with the price annotation matching
// the proceeds (Cash + Fees) within tolerance produces no diagnostic.
// Numbers are taken from the package godoc example: -81 ADSK at cost
// 26.3125 USD, sale price 26.4375 USD; expected proceeds 2141.4375
// USD; entered proceeds 2141.36 + 0.08 = 2141.44 USD. The 0.0025 USD
// diff sits inside the 0.5×10^-2×2 = 0.01 USD tolerance.
func TestCleanSale(t *testing.T) {
	tx := txn(1999, 7, 31,
		salePosting(t, "Assets:US:BRS:Company:ESPP", "-81", "ADSK", "26.3125", "USD", "26.4375", "USD"),
		plainPosting(t, "Assets:US:BRS:Company:Cash", "2141.36", "USD"),
		plainPosting(t, "Expenses:Financial:Fees", "0.08", "USD"),
		nilAmountPosting("Income:US:Company:ESPP:PnL"),
	)
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

// TestWrongProceedsAmount: a sale whose cash proceeds disagree with
// the price-implied proceeds by more than the tolerance produces one
// diagnostic anchored at the offending Transaction's Span. The diff
// here is roughly 58.64 USD — well outside any reasonable tolerance.
func TestWrongProceedsAmount(t *testing.T) {
	tx := txn(1999, 7, 31,
		salePosting(t, "Assets:US:BRS:Company:ESPP", "-81", "ADSK", "26.3125", "USD", "26.4375", "USD"),
		plainPosting(t, "Assets:US:BRS:Company:Cash", "2200.00", "USD"), // wrong
		plainPosting(t, "Expenses:Financial:Fees", "0.08", "USD"),
		nilAmountPosting("Income:US:Company:ESPP:PnL"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []api.Error{{Code: codeInvalidSellGains, Span: txnSpan}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(res.Errors[0].Message, "1999-07-31") {
		t.Errorf("res.Errors[0].Message = %q, expected to mention transaction date 1999-07-31", res.Errors[0].Message)
	}
	if !strings.Contains(res.Errors[0].Message, "USD") {
		t.Errorf("res.Errors[0].Message = %q, expected to mention currency USD", res.Errors[0].Message)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
}

// TestMissingProceedsCurrency: a sale where every cost-bearing posting
// carries a price, but no non-Income posting contributes to the price
// currency at all (all proceeds went to an Income account by mistake)
// produces one diagnostic — the price side has 2141.4375 USD, the
// proceeds side has 0 USD, far outside any tolerance.
func TestMissingProceedsCurrency(t *testing.T) {
	tx := txn(1999, 7, 31,
		salePosting(t, "Assets:US:BRS:Company:ESPP", "-81", "ADSK", "26.3125", "USD", "26.4375", "USD"),
		plainPosting(t, "Income:US:Company:ESPP:PnL", "-2141.4375", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []api.Error{{Code: codeInvalidSellGains, Span: txnSpan}}
	if diff := cmp.Diff(want, res.Errors, errorCmpOpts); diff != "" {
		t.Fatalf("apply errors mismatch (-want +got):\n%s", diff)
	}
}

// TestNonSaleTxnIgnored: a transaction without any cost-bearing
// postings is not a sale — the plugin must skip it silently. The
// transaction here is an ordinary food expense, far outside the
// plugin's scope.
func TestNonSaleTxnIgnored(t *testing.T) {
	tx := txn(2024, 5, 1,
		plainPosting(t, "Expenses:Food", "10.00", "USD"),
		plainPosting(t, "Assets:Cash", "-10.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestCostWithoutPrice: a transaction where a cost-bearing posting
// has no price annotation is skipped — without an independent price
// the plugin has nothing to validate against, matching upstream's
// `not all(posting.price is not None for posting in postings_at_cost)`
// guard.
func TestCostWithoutPrice(t *testing.T) {
	a := amt(t, "-10", "AAPL")
	cost := amt(t, "150.00", "USD")
	cur := ast.Posting{
		Account: ast.Account("Assets:Inv"),
		Amount:  &a,
		Cost:    &ast.CostSpec{PerUnit: &cost},
	}
	tx := txn(2024, 6, 1,
		cur,
		plainPosting(t, "Assets:Cash", "1500.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestMultipleSalesInOneTxn: a composite transaction reducing two
// distinct lots of the same commodity, both at price, with the cash
// leg covering the combined proceeds within tolerance produces no
// diagnostic. Lot 1: -10 ADSK @ 30.00 USD = 300.00. Lot 2: -5 ADSK @
// 32.00 USD = 160.00. Combined price-side total: 460.00 USD.
// Proceeds: 459.95 USD (cash) + 0.05 USD (fee) = 460.00 USD. Diff is
// zero.
func TestMultipleSalesInOneTxn(t *testing.T) {
	tx := txn(2024, 7, 1,
		salePosting(t, "Assets:Inv", "-10", "ADSK", "25.00", "USD", "30.00", "USD"),
		salePosting(t, "Assets:Inv", "-5", "ADSK", "27.00", "USD", "32.00", "USD"),
		plainPosting(t, "Assets:Cash", "459.95", "USD"),
		plainPosting(t, "Expenses:Fees", "0.05", "USD"),
		nilAmountPosting("Income:Gains:ADSK"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
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
// pre-apply state against post-apply state for an offending input
// that exercises the diagnostic emission path (a sale with a wrong
// proceeds amount).
func TestNoDirectiveMutation(t *testing.T) {
	tx := txn(1999, 7, 31,
		salePosting(t, "Assets:US:BRS:Company:ESPP", "-81", "ADSK", "26.3125", "USD", "26.4375", "USD"),
		plainPosting(t, "Assets:US:BRS:Company:Cash", "2200.00", "USD"),
		plainPosting(t, "Expenses:Financial:Fees", "0.08", "USD"),
		nilAmountPosting("Income:US:Company:ESPP:PnL"),
	)
	snap := *tx
	snapPostings := append([]ast.Posting(nil), tx.Postings...)

	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff(snap, *tx, astCmpOpts); diff != "" {
		t.Errorf("transaction header mutated (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(snapPostings, tx.Postings, astCmpOpts); diff != "" {
		t.Errorf("transaction postings mutated (-want +got):\n%s", diff)
	}
}

// TestExactMessageAssertion: locks the byte-exact diagnostic message
// for a wrong-proceeds case. The numbers are chosen so the message
// stays stable across apd.Decimal versions: -2 ADSK at sale price
// 100.00 USD, with cash 250.00 USD and an excluded Income leg. Price
// side = 100.00 × 2 = 200.00 USD. Proceeds = 250.00 USD. Diff = -50
// USD; |diff| = 50 USD, far outside the 0.01 tolerance.
func TestExactMessageAssertion(t *testing.T) {
	tx := txn(2024, 8, 15,
		salePosting(t, "Assets:Inv", "-2", "ADSK", "90.00", "USD", "100.00", "USD"),
		plainPosting(t, "Assets:Cash", "250.00", "USD"),
		nilAmountPosting("Income:Gains:ADSK"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	wantMsg := "Invalid price vs. proceeds for 2024-08-15: USD: price=200.00 proceeds=250.00 diff=-50.00 tol=0.010"
	if got := res.Errors[0].Message; got != wantMsg {
		t.Errorf("res.Errors[0].Message = %q, want %q", got, wantMsg)
	}
}

// TestSpanFallbackToPlugin: a transaction whose Span is the zero
// value falls back to the triggering plugin directive's Span. This
// matches the convention used by sibling diagnostic ports and keeps
// fixture-built tests that omit per-directive spans usable.
func TestSpanFallbackToPlugin(t *testing.T) {
	tx := &ast.Transaction{
		// No Span set (zero value).
		Date: time.Date(2024, 8, 15, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			salePosting(t, "Assets:Inv", "-2", "ADSK", "90.00", "USD", "100.00", "USD"),
			plainPosting(t, "Assets:Cash", "250.00", "USD"),
			nilAmountPosting("Income:Gains:ADSK"),
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(res.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if got := res.Errors[0].Span; got != testPluginDir.Span {
		t.Errorf("res.Errors[0].Span = %#v, want testPluginDir.Span %#v (fallback)", got, testPluginDir.Span)
	}
}

// TestIncomePostingExcluded: an Income-rooted posting with a
// numerical amount must NOT contribute to the proceeds sum. Here the
// price-implied side is -2 × 100.00 USD inverted = 200.00 USD; the
// Assets:Cash leg supplies exactly 200.00 USD; and the Income leg
// (-20.00 USD, well below the cost basis) is excluded by the rule.
// The diff is zero, so no diagnostic fires.  If the plugin
// erroneously included Income postings in proceeds the proceeds
// total would be 180.00 USD and the diff would be 20.00 USD, far
// outside the tolerance — i.e., this test would fail loudly.
func TestIncomePostingExcluded(t *testing.T) {
	tx := txn(2024, 9, 1,
		salePosting(t, "Assets:Inv", "-2", "ADSK", "90.00", "USD", "100.00", "USD"),
		plainPosting(t, "Assets:Cash", "200.00", "USD"),
		plainPosting(t, "Income:Gains:ADSK", "-20.00", "USD"),
	)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 (Income postings must be excluded); errors = %v", len(res.Errors), res.Errors)
	}
}
