package checkmetadata

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

func meta(kvs ...string) ast.Metadata {
	m := ast.Metadata{Props: make(map[string]ast.MetaValue)}
	for i := 0; i+1 < len(kvs); i += 2 {
		m.Props[kvs[i]] = ast.MetaValue{Kind: ast.MetaString, String: kvs[i+1]}
	}
	return m
}

func date(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// TestEmptyConfig: empty config produces no diagnostics.
func TestEmptyConfig(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank:Checking"},
	}
	in := api.Input{Directive: testPluginDir, Directives: seqOf(dirs)}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestUnknownDirectiveType: unknown directive type in config emits
// check-metadata-invalid-config and no further checks.
func TestUnknownDirectiveType(t *testing.T) {
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(nil),
		Config:     "transaction\nmetadata1",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1", len(res.Diagnostics))
	}
	if res.Diagnostics[0].Code != codeInvalidConfig {
		t.Errorf("code = %q, want %q", res.Diagnostics[0].Code, codeInvalidConfig)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "transaction") {
		t.Errorf("message %q does not mention directive type", res.Diagnostics[0].Message)
	}
}

// TestOpenLeafWithAllMetadata: leaf account that has all required metadata
// produces no diagnostic.
func TestOpenLeafWithAllMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank"},
		&ast.Open{Date: date(2020, 1, 2), Account: "Assets:Bank:Checking",
			Meta: meta("region", "US", "tax_category", "taxable")},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\nregion\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestOpenLeafMissingMetadata: leaf account missing required metadata
// emits check-metadata-missing.
func TestOpenLeafMissingMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank"},
		&ast.Open{Date: date(2020, 1, 2), Account: "Assets:Bank:Checking"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\nregion\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1", len(res.Diagnostics))
	}
	if res.Diagnostics[0].Code != codeMissing {
		t.Errorf("code = %q, want %q", res.Diagnostics[0].Code, codeMissing)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "Assets:Bank:Checking") {
		t.Errorf("message %q does not mention account", res.Diagnostics[0].Message)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "region") {
		t.Errorf("message %q does not mention 'region'", res.Diagnostics[0].Message)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "tax_category") {
		t.Errorf("message %q does not mention 'tax_category'", res.Diagnostics[0].Message)
	}
}

// TestNonLeafAccountNotChecked: a non-leaf account is skipped even when
// metadata is missing.
func TestNonLeafAccountNotChecked(t *testing.T) {
	dirs := []ast.Directive{
		// Assets:Bank is non-leaf (has child Assets:Bank:Checking)
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank"},
		&ast.Open{Date: date(2020, 1, 2), Account: "Assets:Bank:Checking",
			Meta: meta("region", "US")},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (non-leaf skipped); diagnostics = %v",
			len(res.Diagnostics), res.Diagnostics)
	}
}

// TestMixedLeafAndNonLeaf: only the leaf account without metadata emits a
// diagnostic; non-leaf and leaf-with-metadata are silent.
func TestMixedLeafAndNonLeaf(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank"},
		&ast.Open{Date: date(2020, 1, 2), Account: "Assets:Bank:Checking",
			Meta: meta("region", "US")},
		&ast.Open{Date: date(2020, 1, 3), Account: "Assets:Bank:Savings"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "Assets:Bank:Savings") {
		t.Errorf("message %q does not mention Assets:Bank:Savings", res.Diagnostics[0].Message)
	}
}

// TestAccountPrefixFilter: when an account prefix is specified, only accounts
// equal to or under that prefix are checked.
func TestAccountPrefixFilter(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank"},
		&ast.Open{Date: date(2020, 1, 2), Account: "Assets:Bank:Checking",
			Meta: meta("region", "US")},
		&ast.Open{Date: date(2020, 1, 3), Account: "Assets:Bank:Savings"},
		// Outside filter — must not be checked.
		&ast.Open{Date: date(2020, 1, 4), Account: "Assets:Crypto:Wallet"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open Assets:Bank\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "Assets:Bank:Savings") {
		t.Errorf("message %q should mention Assets:Bank:Savings", res.Diagnostics[0].Message)
	}
}

// TestAccountPrefixExcludesPartialMatch: "Assets:Bank" does not match
// "Assets:BankAccount".
func TestAccountPrefixExcludesPartialMatch(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:BankAccount"},
		&ast.Open{Date: date(2020, 1, 2), Account: "Assets:BankAccount:Checking"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open Assets:Bank\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 (prefix mismatch)", len(res.Diagnostics))
	}
}

// TestCommodityAlwaysChecked: Commodity directive is checked regardless of
// account hierarchy (no leaf scoping applies).
func TestCommodityAlwaysChecked(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Commodity{Date: date(2020, 1, 1), Currency: "JPY"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "commodity\nexport",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != codeMissing {
		t.Errorf("code = %q, want %q", res.Diagnostics[0].Code, codeMissing)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "JPY") {
		t.Errorf("message %q does not mention currency", res.Diagnostics[0].Message)
	}
}

// TestCommodityWithMetadata: Commodity directive with all required metadata
// produces no diagnostic.
func TestCommodityWithMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Commodity{Date: date(2020, 1, 1), Currency: "USD",
			Meta: meta("export", "yahoo/USD")},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "commodity\nexport",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestOnlySpecifiedDirectiveTypeChecked: config for "commodity" does not
// check Open directives that lack the same metadata.
func TestOnlySpecifiedDirectiveTypeChecked(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank"},
		&ast.Commodity{Date: date(2020, 1, 1), Currency: "USD"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "commodity\nexport",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "USD") {
		t.Errorf("message %q does not mention USD", res.Diagnostics[0].Message)
	}
}

// TestCloseLeafMissingMetadata: Close directive on leaf account missing
// required metadata emits a diagnostic.
func TestCloseLeafMissingMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bank:Old"},
		&ast.Close{Date: date(2020, 12, 31), Account: "Assets:Bank:Old"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "close\nreason",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "Assets:Bank:Old") {
		t.Errorf("message %q does not mention account", res.Diagnostics[0].Message)
	}
}

// TestBalanceLeafWithMetadata: Balance directive on leaf account with all
// required metadata produces no diagnostic.
func TestBalanceLeafWithMetadata(t *testing.T) {
	a := amt(100, "USD")
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Balance{Date: date(2020, 1, 15), Account: "Assets:Cash", Amount: a,
			Meta: meta("verified", "yes")},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "balance\nverified",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestDocumentLeafMissingMetadata: Document directive on leaf account missing
// required metadata emits a diagnostic.
func TestDocumentLeafMissingMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Documents"},
		&ast.Document{Date: date(2020, 1, 1), Account: "Assets:Documents",
			Path: "/path/to/doc.pdf"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "document\ncategory",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "Assets:Documents") {
		t.Errorf("message %q does not mention account", res.Diagnostics[0].Message)
	}
}

// TestNoteLeafWithMetadata: Note directive on leaf account with all required
// metadata produces no diagnostic.
func TestNoteLeafWithMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Account"},
		&ast.Note{Date: date(2020, 1, 1), Account: "Assets:Account",
			Comment: "Important note",
			Meta:    meta("importance", "high")},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "note\nimportance",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// TestMultipleMissingMetadata: a directive missing multiple required keys
// lists all of them in the single diagnostic.
func TestMultipleMissingMetadata(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Account"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\nregion\ntax_category\ntype",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	msg := res.Diagnostics[0].Message
	if !strings.Contains(msg, "region") || !strings.Contains(msg, "tax_category") || !strings.Contains(msg, "type") {
		t.Errorf("message %q does not list all missing metadata keys", msg)
	}
}

// TestCaseInsensitiveDirectiveName: directive name in config is case-insensitive.
func TestCaseInsensitiveDirectiveName(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Commodity{Date: date(2020, 1, 1), Currency: "EUR"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "COMMODITY\nexport",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Errorf("len(res.Diagnostics) = %d, want 1 (case-insensitive match)", len(res.Diagnostics))
	}
}

// TestDiagnosticSpanFromDirective: when the directive has a span, the
// diagnostic is anchored there rather than at the plugin span.
func TestDiagnosticSpanFromDirective(t *testing.T) {
	dirSpan := ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 10}}
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash", Span: dirSpan},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1", len(res.Diagnostics))
	}
	if res.Diagnostics[0].Span != dirSpan {
		t.Errorf("diagnostic span = %v, want directive span %v", res.Diagnostics[0].Span, dirSpan)
	}
}

// TestInvalidConfigDiagSpan: the invalid-config diagnostic is anchored at
// the plugin directive's span.
func TestInvalidConfigDiagSpan(t *testing.T) {
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(nil),
		Config:     "transaction\nfoo",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1", len(res.Diagnostics))
	}
	want := []ast.Diagnostic{{Code: codeInvalidConfig, Span: testPluginDir.Span}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Errorf("diagnostic mismatch (-want +got):\n%s", diff)
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

// TestNoDirectiveMutation: the plugin does not mutate any input directive.
func TestNoDirectiveMutation(t *testing.T) {
	open := &ast.Open{Date: date(2020, 1, 1), Account: "Assets:Account"}
	origAccount := open.Account
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{open}),
		Config:     "open\nregion",
	}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if open.Account != origAccount {
		t.Errorf("apply mutated directive account: %q -> %q", origAccount, open.Account)
	}
}

// TestNilDirectivesIterator: nil Directives is treated as empty — no panic.
func TestNilDirectivesIterator(t *testing.T) {
	in := api.Input{
		Directive: testPluginDir,
		Config:    "open\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 for nil input", len(res.Diagnostics))
	}
}

// TestBlankLinesInConfig: blank lines in the config are silently ignored.
func TestBlankLinesInConfig(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Config:     "open\n\n\nregion",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "region") {
		t.Errorf("message %q does not mention 'region'", res.Diagnostics[0].Message)
	}
}
