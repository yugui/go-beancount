package commoditypattern

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

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

func openWith(account ast.Account, pattern string) *ast.Open {
	o := &ast.Open{
		Date:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: account,
	}
	if pattern != "" {
		o.Meta = ast.Metadata{Props: map[string]ast.MetaValue{
			metadataKey: {Kind: ast.MetaString, String: pattern},
		}}
	}
	return o
}

func makeTx(postings []ast.Posting) *ast.Transaction {
	return &ast.Transaction{
		Date:     time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag:     '*',
		Postings: postings,
	}
}

// TestNoPatterns: accounts without commodity-pattern metadata are ignored.
func TestNoPatterns(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &pos},
		{Account: "Expenses:Test", Amount: &neg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", ""), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestMatchingCommodity: posting currency that matches the pattern emits no diagnostic.
func TestMatchingCommodity(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &pos},
		{Account: "Expenses:Test", Amount: &neg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD|EUR"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil (diagnostic-only)", res.Directives)
	}
}

// TestNonMatchingCommodity: a posting currency that doesn't match the pattern
// emits a mismatch diagnostic naming the commodity, account, and pattern.
func TestNonMatchingCommodity(t *testing.T) {
	pos := amt(100, "JPY")
	neg := amt(-100, "JPY")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &pos},
		{Account: "Expenses:Test", Amount: &neg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD|EUR"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != codeMismatch {
		t.Errorf("Code = %q, want %q", res.Diagnostics[0].Code, codeMismatch)
	}
	if res.Diagnostics[0].Severity != ast.Error {
		t.Errorf("Severity = %v, want Error", res.Diagnostics[0].Severity)
	}
	msg := res.Diagnostics[0].Message
	if !strings.Contains(msg, "JPY") {
		t.Errorf("message %q missing commodity JPY", msg)
	}
	if !strings.Contains(msg, "Assets:Bank") {
		t.Errorf("message %q missing account Assets:Bank", msg)
	}
	if !strings.Contains(msg, "USD|EUR") {
		t.Errorf("message %q missing pattern USD|EUR", msg)
	}
}

// TestMultiplePostingsSameAccount: only the non-matching posting emits a diagnostic.
func TestMultiplePostingsSameAccount(t *testing.T) {
	p1 := amt(100, "USD")
	p2 := amt(50, "EUR")
	neg := amt(-150, "USD")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &p1},
		{Account: "Assets:Bank", Amount: &p2},
		{Account: "Equity:Opening", Amount: &neg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "EUR") {
		t.Errorf("message %q missing EUR", res.Diagnostics[0].Message)
	}
}

// TestMultipleAccountsDifferentPatterns: each account validates independently.
func TestMultipleAccountsDifferentPatterns(t *testing.T) {
	fiat := amt(-1000, "USD")
	crypto := amt(1, "BTC")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank:Fiat", Amount: &fiat},
		{Account: "Assets:Crypto", Amount: &crypto},
	})
	in := api.Input{
		Directive: testPluginDir,
		Directives: seqOf([]ast.Directive{
			openWith("Assets:Bank:Fiat", "USD|EUR"),
			openWith("Assets:Crypto", "BTC|ETH"),
			tx,
		}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestAccountWithoutPatternIgnored: account without commodity-pattern is not validated
// even if its currency would not match some other account's pattern.
func TestAccountWithoutPatternIgnored(t *testing.T) {
	bank := amt(-20, "USD")
	food := amt(20, "JPY")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &bank},
		{Account: "Expenses:Food", Amount: &food},
	})
	in := api.Input{
		Directive: testPluginDir,
		Directives: seqOf([]ast.Directive{
			openWith("Assets:Bank", "USD"),
			openWith("Expenses:Food", ""),
			tx,
		}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestInvalidRegexpPattern: an invalid regexp in an Open directive emits
// invalid-regexp and skips transaction validation entirely.
func TestInvalidRegexpPattern(t *testing.T) {
	pos := amt(100, "USD")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &pos},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "[invalid"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != codeInvalidRegexp {
		t.Errorf("Code = %q, want %q", res.Diagnostics[0].Code, codeInvalidRegexp)
	}
	if res.Diagnostics[0].Severity != ast.Error {
		t.Errorf("Severity = %v, want Error", res.Diagnostics[0].Severity)
	}
	msg := res.Diagnostics[0].Message
	if !strings.Contains(msg, "[invalid") {
		t.Errorf("message %q missing pattern [invalid", msg)
	}
	if !strings.Contains(msg, "Assets:Bank") {
		t.Errorf("message %q missing account Assets:Bank", msg)
	}
}

// TestAutoBalancedPostingSkipped: postings without a units amount are not checked.
func TestAutoBalancedPostingSkipped(t *testing.T) {
	bank := amt(-100, "USD")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &bank},
		{Account: "Expenses:Test", Amount: nil}, // auto-balanced
	})
	in := api.Input{
		Directive: testPluginDir,
		Directives: seqOf([]ast.Directive{
			openWith("Assets:Bank", "USD"),
			openWith("Expenses:Test", "USD"),
			tx,
		}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (auto-balanced posting skipped); got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestFullMatchNotPartial: the pattern must match the whole currency string.
func TestFullMatchNotPartial(t *testing.T) {
	pos := amt(100, "USDC")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &pos},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1 (USDC must not match USD via partial); got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "USDC") {
		t.Errorf("message %q missing USDC", res.Diagnostics[0].Message)
	}
}

// TestMultipleErrorsSameTransaction: each offending posting yields its own diagnostic.
func TestMultipleErrorsSameTransaction(t *testing.T) {
	p1 := amt(100, "EUR")
	p2 := amt(50, "JPY")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Bank", Amount: &p1},
		{Account: "Assets:Bank", Amount: &p2},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 2 {
		t.Fatalf("len(res.Diagnostics) = %d, want 2; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	currencies := map[string]bool{}
	for _, d := range res.Diagnostics {
		for _, c := range []string{"EUR", "JPY"} {
			if strings.Contains(d.Message, c) {
				currencies[c] = true
			}
		}
	}
	for _, c := range []string{"EUR", "JPY"} {
		if !currencies[c] {
			t.Errorf("no diagnostic message mentions %q", c)
		}
	}
}

// TestComplexRegexPattern: a pattern with character classes matches correctly.
func TestComplexRegexPattern(t *testing.T) {
	pos := amt(10, "STOCK-AAPL")
	neg := amt(-1000, "USD")
	tx := makeTx([]ast.Posting{
		{Account: "Assets:Stocks", Amount: &pos},
		{Account: "Assets:Cash", Amount: &neg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Stocks", `STOCK-[A-Z]+`), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; got %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestPostingSpanPreferred: when a posting has its own span, the diagnostic
// is anchored there rather than at the enclosing transaction.
func TestPostingSpanPreferred(t *testing.T) {
	postingSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 42}}
	txSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 40}}
	pos := amt(100, "EUR")
	tx := &ast.Transaction{
		Date: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: txSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: &pos, Span: postingSpan},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != postingSpan {
		t.Errorf("Span = %#v, want postingSpan %#v", res.Diagnostics[0].Span, postingSpan)
	}
}

// TestTransactionSpanFallback: when the posting has no span, the diagnostic
// is anchored at the transaction span.
func TestTransactionSpanFallback(t *testing.T) {
	txSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 40}}
	pos := amt(100, "EUR")
	tx := &ast.Transaction{
		Date: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Span: txSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: &pos},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != txSpan {
		t.Errorf("Span = %#v, want txSpan %#v", res.Diagnostics[0].Span, txSpan)
	}
}

// TestPluginSpanFallback: when neither posting nor transaction carries a span,
// the diagnostic falls back to the plugin directive's span.
func TestPluginSpanFallback(t *testing.T) {
	pos := amt(100, "EUR")
	tx := &ast.Transaction{
		Date: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: &pos},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{openWith("Assets:Bank", "USD"), tx}),
	}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != testPluginDir.Span {
		t.Errorf("Span = %#v, want plugin span %#v", res.Diagnostics[0].Span, testPluginDir.Span)
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

// TestEmptyInput: an empty directive sequence yields a zero-valued Result.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
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

// TestNoDirectiveMutation: the plugin must not mutate input directives.
func TestNoDirectiveMutation(t *testing.T) {
	open := openWith("Assets:Bank", "USD")
	pos := amt(100, "EUR")
	tx := makeTx([]ast.Posting{{Account: "Assets:Bank", Amount: &pos}})

	origMeta := open.Meta.Props[metadataKey]
	origAccount := open.Account
	origPostings := len(tx.Postings)

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{open, tx}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives non-nil (diagnostic-only plugin)")
	}
	if open.Account != origAccount {
		t.Errorf("open.Account mutated: %q -> %q", origAccount, open.Account)
	}
	if open.Meta.Props[metadataKey] != origMeta {
		t.Errorf("open metadata mutated")
	}
	if len(tx.Postings) != origPostings {
		t.Errorf("tx.Postings mutated: %d -> %d", origPostings, len(tx.Postings))
	}
}
