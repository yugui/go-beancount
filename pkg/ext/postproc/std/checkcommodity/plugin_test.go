package checkcommodity_test

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
	"github.com/yugui/go-beancount/pkg/ext/postproc/std/checkcommodity"
)

// errorCmpOpts compares api.Error values structurally while leaving
// the human-readable Message field to the test's own substring
// assertions. This keeps strict assertions on Code and Span (the
// programmable contract) without coupling tests to exact wording.
var errorCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(api.Error{}, "Message"),
}

// testPluginDir is a non-zero *ast.Plugin shared by every error-path
// test in this file. Threading the same directive through every
// api.Input lets each test assert that diagnostics anchor at
// testPluginDir.Span (the plugin's contract per [api.Input.Directive]),
// instead of silently checking that Span is the zero value.
//
// The plugin is contracted not to mutate api.Input.Directive (see
// TestNoDirectiveMutation, which asserts the snapshot of
// testPluginDir.Span is unchanged after Apply). Tests in this file
// must not call t.Parallel() because they share this pointer; if a
// future test needs parallelism, switch to a fresh-pointer constructor.
var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 1}}}

// seqOf adapts a directive slice into api.Input.Directives without
// needing a full *ast.Ledger fixture.
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

// assertErrors compares got against want using errorCmpOpts (which
// ignores Message). The parameter order follows the standard Go test
// idiom of "actual first, expected second"; internally the helper
// passes them to cmp.Diff in (want, got) order so the (-want +got)
// diff labels stay correct. On any mismatch it fails the test
// fatally so callers can safely index into got afterwards for
// per-position substring checks on Message — tests that need to keep
// going past a structural mismatch should call cmp.Diff inline with
// t.Errorf and an explicit length guard instead.
func assertErrors(t *testing.T, got, want []api.Error) {
	t.Helper()
	if diff := cmp.Diff(want, got, errorCmpOpts); diff != "" {
		t.Fatalf("checkcommodity.Plugin errors mismatch (-want +got):\n%s", diff)
	}
}

// TestMissingCommodityInTransaction: a transaction uses a currency that
// has no Commodity directive; a single missing-commodity error is
// emitted, anchored at the triggering plugin directive's span.
func TestMissingCommodityInTransaction(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertErrors(t, res.Errors, []api.Error{{Code: "missing-commodity", Span: testPluginDir.Span}})

	if got := res.Errors[0].Message; !strings.Contains(got, "USD") {
		t.Errorf("checkcommodity.Plugin errors[0].Message = %q, want it to mention USD", got)
	}
}

// TestDeclaredCommoditySilencesError: a Commodity directive for USD
// suppresses the missing-commodity error.
func TestDeclaredCommoditySilencesError(t *testing.T) {
	decl := &ast.Commodity{
		Date:     time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Currency: "USD",
	}
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{decl, tx})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestPriceContextReported: a Price directive uses an undeclared
// currency; the error uses the "Price Directive Context" sentinel (the
// exact upstream spelling).
func TestPriceContextReported(t *testing.T) {
	decl := &ast.Commodity{Currency: "USD"}
	price := &ast.Price{
		Date:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Commodity: "HOOL",
		Amount:    amt(1234, "USD"),
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{decl, price})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertErrors(t, res.Errors, []api.Error{{Code: "missing-commodity", Span: testPluginDir.Span}})

	got := res.Errors[0].Message
	if !strings.Contains(got, "HOOL") {
		t.Errorf("got.Message = %q, want it to mention HOOL", got)
	}
	if !strings.Contains(got, "Price Directive Context") {
		t.Errorf("got.Message = %q, want it to mention the Price Directive Context sentinel", got)
	}
}

// TestMissingReportedOncePerCurrency: three separate uses of the same
// undeclared currency produce exactly one error — upstream's
// "Process it only once" contract.
func TestMissingReportedOncePerCurrency(t *testing.T) {
	pos := amt(1, "HOOL")
	neg := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker", Amount: &pos},
			{Account: "Income:Gains", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Broker",
		Amount:  amt(1, "HOOL"),
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx, bal})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Exactly one error per currency: HOOL appears in three places
	// but only the first sorted (account, currency) occurrence emits.
	assertErrors(t, res.Errors, []api.Error{{Code: "missing-commodity", Span: testPluginDir.Span}})
}

// TestIgnoreMapSuppresses: when every occurrence of a currency is
// covered by the ignore map, no error is reported. Upstream behavior:
// a pair that matches the ignore map is moved to the `ignored` set,
// but another occurrence of the same currency on an unrelated account
// still emits (the ignore check is per-pair, not per-currency). This
// test uses a single-account pairing so the ignore map fully covers
// the occurrence.
func TestIgnoreMapSuppresses(t *testing.T) {
	pos := amt(1, "SPX_121622P3300")
	neg := amt(-1, "SPX_121622P3300")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker:Options", Amount: &pos},
			// Pair the asset with its own currency so the ignore
			// map covers every (account, currency) occurrence.
			{Account: "Assets:Broker:Options:Cleared", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Config:     `{"Assets:Broker:.*": "SPX_.*"}`,
		Directives: seqOf([]ast.Directive{tx}),
	}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("len(res.Errors) = %d, want 0 with ignore map applied; errors = %v", len(res.Errors), res.Errors)
	}
}

// TestIgnoreMapPerPair: the ignore map is matched per (account,
// currency) pair. An unrelated account using the same currency still
// triggers an error.
func TestIgnoreMapPerPair(t *testing.T) {
	pos := amt(1, "SPX_121622P3300")
	neg := amt(-1, "SPX_121622P3300")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker:Options", Amount: &pos},
			{Account: "Income:PnL", Amount: &neg},
		},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Config:     `{"Assets:Broker:Options": "SPX_.*"}`,
		Directives: seqOf([]ast.Directive{tx}),
	}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The (Assets:Broker:Options, SPX_...) pair is ignored; the
	// (Income:PnL, SPX_...) pair still triggers an error. Upstream's
	// first pass short-circuits on the `issued` set only — not on
	// `ignored`.
	assertErrors(t, res.Errors, []api.Error{{Code: "missing-commodity", Span: testPluginDir.Span}})
	if !strings.Contains(res.Errors[0].Message, "Income:PnL") {
		t.Errorf("res.Errors[0].Message = %q, want it to cite Income:PnL", res.Errors[0].Message)
	}
}

// TestInvalidJSONConfigFatal: a malformed JSON config surfaces as
// "invalid-config" and the plugin reports no further diagnostics.
func TestInvalidJSONConfigFatal(t *testing.T) {
	pos := amt(1, "HOOL")
	neg := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker", Amount: &pos},
			{Account: "Income:Gains", Amount: &neg},
		},
	}
	in := api.Input{Directive: testPluginDir, Config: `not json`, Directives: seqOf([]ast.Directive{tx})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertErrors(t, res.Errors, []api.Error{{Code: "invalid-config", Span: testPluginDir.Span}})
}

// TestInvalidRegexpSkipsPair: a malformed regex emits an
// "invalid-regexp" per bad pattern but processing continues with
// surviving pairs. Both postings live under Assets:Broker:* so the
// valid pair fully covers the currency.
func TestInvalidRegexpSkipsPair(t *testing.T) {
	pos := amt(1, "HOOL")
	neg := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker:Primary", Amount: &pos},
			{Account: "Assets:Broker:Secondary", Amount: &neg},
		},
	}
	// `(` is a malformed regex in Go's RE2; the "good" pair should
	// still match and ignore the currency on every posting.
	in := api.Input{
		Directive:  testPluginDir,
		Config:     `{"(": "bad", "Assets:Broker:.*": "HOOL"}`,
		Directives: seqOf([]ast.Directive{tx}),
	}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sawInvalidRegexp := false
	sawMissing := false
	for _, e := range res.Errors {
		switch e.Code {
		case "invalid-regexp":
			sawInvalidRegexp = true
			if e.Span != testPluginDir.Span {
				t.Errorf("invalid-regexp error Span = %#v, want testPluginDir.Span %#v", e.Span, testPluginDir.Span)
			}
		case "missing-commodity":
			sawMissing = true
		}
	}
	if !sawInvalidRegexp {
		t.Errorf("checkcommodity.Plugin errors = %v, want at least one invalid-regexp diagnostic", res.Errors)
	}
	if sawMissing {
		t.Errorf("checkcommodity.Plugin emitted missing-commodity, want it suppressed by the valid ignore pair; errors = %v", res.Errors)
	}
}

// TestDeterministicOrder: three undeclared currencies used in accounts
// whose names sort non-alphabetically yield errors in (account,
// currency) lexicographic order.
func TestDeterministicOrder(t *testing.T) {
	a := amt(1, "JPY")
	b := amt(1, "EUR")
	c := amt(1, "CAD")
	na := amt(-1, "JPY")
	nb := amt(-1, "EUR")
	nc := amt(-1, "CAD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Z", Amount: &a},
			{Account: "Income:Z", Amount: &na},
			{Account: "Assets:A", Amount: &b},
			{Account: "Income:A", Amount: &nb},
			{Account: "Assets:M", Amount: &c},
			{Account: "Income:M", Amount: &nc},
		},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Inline cmp.Diff with t.Errorf (instead of assertErrors) so a
	// count mismatch on the structural check does not suppress the
	// independent per-position ordering check below.
	wantErr := api.Error{Code: "missing-commodity", Span: testPluginDir.Span}
	wantErrors := []api.Error{wantErr, wantErr, wantErr}
	if diff := cmp.Diff(wantErrors, res.Errors, errorCmpOpts); diff != "" {
		t.Errorf("checkcommodity.Plugin errors mismatch (-want +got):\n%s", diff)
	}

	// Sort key is (account, currency): "Assets:A" < "Assets:M" <
	// "Assets:Z", so errors must appear in EUR, CAD, JPY currency
	// order (EUR lives on Assets:A, CAD on Assets:M, JPY on Assets:Z).
	// api.Error has no structured currency field, so the
	// per-position assertion substring-matches Message.
	wantCurrencies := []string{"EUR", "CAD", "JPY"}
	if len(res.Errors) < len(wantCurrencies) {
		t.Fatalf("len(res.Errors) = %d, want >= %d for ordering check", len(res.Errors), len(wantCurrencies))
	}
	for i, want := range wantCurrencies {
		if !strings.Contains(res.Errors[i].Message, want) {
			t.Errorf("res.Errors[%d].Message = %q, want it to mention %q", i, res.Errors[i].Message, want)
		}
	}
}

// TestOpenCurrenciesChecked: a currency appearing only in an Open
// directive's currency constraint still triggers the diagnostic.
func TestOpenCurrenciesChecked(t *testing.T) {
	op := &ast.Open{
		Date:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Cash",
		Currencies: []string{"ZZZ"},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{op})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertErrors(t, res.Errors, []api.Error{{Code: "missing-commodity", Span: testPluginDir.Span}})
}

// TestAccountReportSuppressesPrice: a currency that appears in both an
// account-bound context and a Price directive is reported only in the
// account context; upstream's second pass skips currencies already
// issued.
func TestAccountReportSuppressesPrice(t *testing.T) {
	pos := amt(1, "HOOL")
	neg := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker", Amount: &pos},
			{Account: "Income:Gains", Amount: &neg},
		},
	}
	price := &ast.Price{
		Date:      time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		Commodity: "HOOL",
		Amount:    amt(100, "USD"),
	}
	usdDecl := &ast.Commodity{Currency: "USD"}
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{usdDecl, tx, price})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The plugin must report HOOL exactly once (in the
	// account-context pass).
	assertErrors(t, res.Errors, []api.Error{{Code: "missing-commodity", Span: testPluginDir.Span}})
	if !strings.Contains(res.Errors[0].Message, "Assets:Broker") {
		t.Errorf("res.Errors[0].Message = %q, want it to cite the account context, not Price Directive Context", res.Errors[0].Message)
	}
}

// TestNoDirectiveMutation verifies the plugin does not mutate input
// directives. The plugin is diagnostic-only; Result.Directives must be
// nil.
func TestNoDirectiveMutation(t *testing.T) {
	pos := amt(1, "HOOL")
	neg := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Broker", Amount: &pos},
			{Account: "Income:Gains", Amount: &neg},
		},
	}
	origPostings := len(tx.Postings)
	// Snapshot the shared testPluginDir so the test catches any
	// accidental write-back through api.Input.Directive.
	origPluginSpan := testPluginDir.Span
	in := api.Input{Directive: testPluginDir, Directives: seqOf([]ast.Directive{tx})}

	res, err := checkcommodity.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (diagnostic-only plugin)", res.Directives)
	}
	if len(tx.Postings) != origPostings {
		t.Errorf("checkcommodity.Plugin mutated input transaction: len(tx.Postings) %d -> %d", origPostings, len(tx.Postings))
	}
	if testPluginDir.Span != origPluginSpan {
		t.Errorf("checkcommodity.Plugin mutated input plugin directive: testPluginDir.Span = %#v, want %#v (the pre-call snapshot)", testPluginDir.Span, origPluginSpan)
	}
}

// TestCanceledContext: the plugin respects a canceled context.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := checkcommodity.Plugin(ctx, api.Input{})
	if err == nil {
		t.Fatalf("checkcommodity.Plugin error = nil, want non-nil on canceled context")
	}
}
