package tradingvalidation

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

func amtF(s string, cur string) ast.Amount {
	d, _, _ := apd.NewFromString(s)
	return ast.Amount{Number: *d, Currency: cur}
}

var testDate = time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)

func mkTx(postings []ast.Posting) *ast.Transaction {
	return &ast.Transaction{Date: testDate, Flag: '*', Postings: postings}
}

func mkTxSpan(postings []ast.Posting, line int) *ast.Transaction {
	tx := mkTx(postings)
	tx.Span = ast.Span{Start: ast.Position{Filename: "test.beancount", Line: line}}
	return tx
}

func mkCommodity(cur string, meta map[string]string) *ast.Commodity {
	c := &ast.Commodity{Date: testDate, Currency: cur}
	if len(meta) > 0 {
		c.Meta.Props = make(map[string]ast.MetaValue, len(meta))
		for k, v := range meta {
			c.Meta.Props[k] = ast.MetaValue{Kind: ast.MetaString, String: v}
		}
	}
	return c
}

func priceAnnotation(n int64, cur string) *ast.PriceAnnotation {
	a := amt(n, cur)
	return &ast.PriceAnnotation{Amount: a}
}

// TestNoTradingPosting: transactions without any trading-account posting
// are skipped — no diagnostics emitted.
func TestNoTradingPosting(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := mkTx([]ast.Posting{
		{Account: "Assets:Cash", Amount: &pos},
		{Account: "Expenses:Food", Amount: &neg},
	})
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestBalancedTradingTransaction: a correctly balanced trading transaction
// (all three rules satisfied) produces no diagnostics.
//
// Layout:
//   - Assets:Securities  1 STOCK @ 100 USD   (non-trading, weight = +100 USD)
//   - Assets:Cash       -100 USD              (non-trading, weight = -100 USD)
//   - Equity:Trading    -1 STOCK @ 100 USD   (trading,     weight = -100 USD)
//   - Equity:Trading   +100 USD              (trading,     weight = +100 USD)
//
// Rule 1: trading weights: -100 USD + 100 USD = 0 ✓
// Rule 2: non-trading weights: 100 USD - 100 USD = 0 ✓
// Rule 3: STOCK raw units: +1 - 1 = 0 ✓; USD raw units: -100 + 100 = 0 ✓
func TestBalancedTradingTransaction(t *testing.T) {
	stockAmt := amtF("1", "STOCK")
	cashAmt := amtF("-100", "USD")
	tradingStock := amtF("-1", "STOCK")
	tradingUSD := amt(100, "USD")

	tx := mkTx([]ast.Posting{
		{Account: "Assets:Securities", Amount: &stockAmt, Price: priceAnnotation(100, "USD")},
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingStock, Price: priceAnnotation(100, "USD")},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingUSD},
	})
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestUnbalancedTradingAccounts: rule 1 fails when the trading-account
// postings do not sum to zero in some currency.
func TestUnbalancedTradingAccounts(t *testing.T) {
	// Trading side: -1 STOCK only. Weight = -1 STOCK (unweighted).
	// Missing the offsetting USD entry on the trading side.
	cashAmt := amtF("-100", "USD")
	stockAmt := amtF("1", "STOCK")
	tradingStock := amtF("-1", "STOCK")

	tx := mkTxSpan([]ast.Posting{
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Assets:Securities", Amount: &stockAmt},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingStock},
	}, 10)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Code == codeTradingNotBalanced {
			return
		}
	}
	t.Errorf("want a %q diagnostic; got %v", codeTradingNotBalanced, res.Diagnostics)
}

// TestUnbalancedNonTradingAccounts: rule 2 fails when the non-trading
// postings do not sum to zero in some currency.
func TestUnbalancedNonTradingAccounts(t *testing.T) {
	// Non-trading: -50 USD + 1 STOCK (@ 100 USD → weight +100 USD).
	// Weights: -50 USD + 100 USD = +50 USD ≠ 0.
	cashAmt := amtF("-50", "USD")
	stockAmt := amtF("1", "STOCK")
	tradingStock := amtF("-1", "STOCK")
	tradingUSD := amt(100, "USD")

	tx := mkTxSpan([]ast.Posting{
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Assets:Securities", Amount: &stockAmt, Price: priceAnnotation(100, "USD")},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingStock, Price: priceAnnotation(100, "USD")},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingUSD},
	}, 20)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Code == codeTradingNotBalanced {
			return
		}
	}
	t.Errorf("want a %q diagnostic; got %v", codeTradingNotBalanced, res.Diagnostics)
}

// TestUnbalancedPerCommodity: rule 3 fails when per-commodity raw-unit
// sums do not cancel to zero.
func TestUnbalancedPerCommodity(t *testing.T) {
	// 2 STOCK bought, only 1 STOCK on the trading side → STOCK doesn't balance.
	stockAmt := amtF("2", "STOCK")
	cashAmt := amtF("-100", "USD")
	tradingStock := amtF("-1", "STOCK")
	tradingUSD := amt(100, "USD")

	tx := mkTxSpan([]ast.Posting{
		{Account: "Assets:Securities", Amount: &stockAmt},
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingStock},
		{Account: "Equity:Trading:STOCK-USD", Amount: &tradingUSD},
	}, 30)
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Code == codeTradingCommodityNotBalanced {
			return
		}
	}
	t.Errorf("want a %q diagnostic; got %v", codeTradingCommodityNotBalanced, res.Diagnostics)
}

// TestDisabledCommodityBalancedByPriceCurrency: a commodity flagged
// "trading-account: disabled" is grouped by price currency for rule 3.
// The transaction below is balanced in USD when the disabled posting's
// weight (1 × 100 = 100 USD) is offset by the -100 USD cash posting.
func TestDisabledCommodityBalancedByPriceCurrency(t *testing.T) {
	commodity := mkCommodity("DS", map[string]string{"trading-account": "disabled"})

	// Trading account must be present for the transaction to be checked.
	// We use a zero-USD trading posting so rule 1 and rule 2 don't fire.
	zeroTrading := amt(0, "USD")
	secAmt := amtF("1", "DS")
	cashAmt := amtF("-100", "USD")

	// Rule 3: effective commodity for "DS" is "USD" (price currency).
	// Postings grouped under USD: 1 DS @ 100 USD → contributes weight 100 USD.
	// Cash: -100 USD (not disabled, raw units).
	// Sum: 100 - 100 = 0 ✓
	tx := mkTx([]ast.Posting{
		{Account: "Assets:Securities", Amount: &secAmt, Price: priceAnnotation(100, "USD")},
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Equity:Trading:DS-USD", Amount: &zeroTrading},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{commodity, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Code == codeTradingCommodityNotBalanced {
			t.Errorf("unexpected commodity balance diagnostic: %v", d)
		}
	}
}

// TestDisabledCommodityUnbalanced: a disabled commodity that does not
// balance in the price currency triggers a commodity-not-balanced diagnostic.
func TestDisabledCommodityUnbalanced(t *testing.T) {
	commodity := mkCommodity("DS", map[string]string{"trading-account": "disabled"})

	zeroTrading := amt(0, "USD")
	secAmt := amtF("2", "DS") // weight = 2 × 100 = 200 USD
	cashAmt := amtF("-100", "USD")

	tx := mkTx([]ast.Posting{
		{Account: "Assets:Securities", Amount: &secAmt, Price: priceAnnotation(100, "USD")},
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Equity:Trading:DS-USD", Amount: &zeroTrading},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{commodity, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Code == codeTradingCommodityNotBalanced {
			return
		}
	}
	t.Errorf("want a %q diagnostic for unbalanced disabled commodity; got %v", codeTradingCommodityNotBalanced, res.Diagnostics)
}

// TestDisabledCommodityNoPrice: a disabled commodity posting without a
// price annotation is excluded from rule 3 — no commodity diagnostic is
// emitted for that posting.
func TestDisabledCommodityNoPrice(t *testing.T) {
	commodity := mkCommodity("DS", map[string]string{"trading-account": "disabled"})

	zeroTrading := amt(0, "USD")
	secAmt := amtF("1", "DS") // no price

	tx := mkTx([]ast.Posting{
		{Account: "Assets:Securities", Amount: &secAmt},
		{Account: "Equity:Trading:DS-USD", Amount: &zeroTrading},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{commodity, tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range res.Diagnostics {
		if d.Code == codeTradingCommodityNotBalanced {
			t.Errorf("unexpected commodity balance diagnostic for disabled commodity without price: %v", d)
		}
	}
}

// TestCustomTradingPrefix: a non-default prefix is respected.
func TestCustomTradingPrefix(t *testing.T) {
	cashAmt := amt(-100, "USD")
	tradingAmt := amt(100, "USD")

	tx := mkTx([]ast.Posting{
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Assets:Trading:CUSTOM", Amount: &tradingAmt},
	})

	// With prefix "Assets:Trading" both sub-groups are unbalanced (each
	// has only one side), so we expect diagnostics.
	inCustom := api.Input{
		Config:     "Assets:Trading",
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx}),
	}
	resCustom, err := apply(context.Background(), inCustom)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resCustom.Diagnostics) == 0 {
		t.Error("want diagnostics with custom prefix, got none")
	}

	// With the default prefix "Equity:Trading" the transaction has no
	// trading-account postings and is skipped — no diagnostics.
	inDefault := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx}),
	}
	resDefault, err := apply(context.Background(), inDefault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resDefault.Diagnostics) != 0 {
		t.Errorf("want no diagnostics with default prefix, got %v", resDefault.Diagnostics)
	}
}

// TestMultipleTradingSubAccounts: postings to multiple distinct trading
// sub-accounts are all inspected by the plugin.
func TestMultipleTradingSubAccounts(t *testing.T) {
	a1 := amtF("-1", "STOCK1")
	a2 := amtF("-1", "STOCK2")
	// Non-trading side is intentionally unbalanced to trigger diagnostics.
	cash := amtF("200", "USD")

	tx := mkTx([]ast.Posting{
		{Account: "Equity:Trading:STOCK1-USD", Amount: &a1},
		{Account: "Equity:Trading:STOCK2-USD", Amount: &a2},
		{Account: "Assets:Cash", Amount: &cash},
	})
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Error("want diagnostics for unbalanced multi-trading-account transaction, got none")
	}
}

// TestTxSpanUsed: diagnostics are anchored at the transaction's Span when
// it is non-zero.
func TestTxSpanUsed(t *testing.T) {
	cashAmt := amtF("-50", "USD")
	tradingAmt := amt(100, "USD")

	wantSpan := ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 42}}
	tx := &ast.Transaction{
		Date: testDate,
		Flag: '*',
		Span: wantSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &cashAmt},
			{Account: "Equity:Trading:TEST", Amount: &tradingAmt},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("want at least one diagnostic, got none")
	}
	for _, d := range res.Diagnostics {
		if d.Span != wantSpan {
			t.Errorf("diagnostic span = %v, want wantSpan %v", d.Span, wantSpan)
		}
	}
}

// TestPluginSpanFallback: when the transaction carries no Span,
// diagnostics fall back to the triggering plugin directive's Span.
func TestPluginSpanFallback(t *testing.T) {
	cashAmt := amtF("-50", "USD")
	tradingAmt := amt(100, "USD")

	tx := &ast.Transaction{
		Date: testDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &cashAmt},
			{Account: "Equity:Trading:TEST", Amount: &tradingAmt},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("want at least one diagnostic, got none")
	}
	for _, d := range res.Diagnostics {
		if d.Span != testPluginDir.Span {
			t.Errorf("diagnostic span = %v, want testPluginDir.Span %v", d.Span, testPluginDir.Span)
		}
	}
}

// TestNilDirectives: a nil Directives iterator is treated as empty input.
func TestNilDirectives(t *testing.T) {
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
		t.Fatal("apply error = nil, want non-nil on canceled context")
	}
}

// TestNoDirectiveMutation: the plugin must not mutate input directives
// and must return nil Result.Directives (diagnostic-only).
func TestNoDirectiveMutation(t *testing.T) {
	cashAmt := amt(-100, "USD")
	tradingAmt := amt(100, "USD")
	tx := mkTx([]ast.Posting{
		{Account: "Assets:Cash", Amount: &cashAmt},
		{Account: "Equity:Trading:TEST", Amount: &tradingAmt},
	})
	origLen := len(tx.Postings)
	origAcct := tx.Postings[0].Account

	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
	if len(tx.Postings) != origLen {
		t.Errorf("apply mutated transaction postings: %d -> %d", origLen, len(tx.Postings))
	}
	if tx.Postings[0].Account != origAcct {
		t.Errorf("apply mutated posting account: %q -> %q", origAcct, tx.Postings[0].Account)
	}
}

// TestDiagCmpOptsUsable verifies diagCmpOpts compiles and ignores Message.
func TestDiagCmpOptsUsable(t *testing.T) {
	a := ast.Diagnostic{Code: "x", Message: "hello"}
	b := ast.Diagnostic{Code: "x", Message: "world"}
	if diff := cmp.Diff(a, b, diagCmpOpts); diff != "" {
		t.Errorf("diagCmpOpts should ignore Message; diff:\n%s", diff)
	}
}
