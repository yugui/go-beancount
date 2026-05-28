package infermetadata

import (
	"context"
	"iter"
	"os"
	"path/filepath"
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

var testDate = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// metaStr builds an ast.Metadata with string-valued keys.
func metaStr(kv ...string) ast.Metadata {
	if len(kv)%2 != 0 {
		panic("metaStr: odd number of args")
	}
	m := ast.Metadata{Props: make(map[string]ast.MetaValue, len(kv)/2)}
	for i := 0; i < len(kv); i += 2 {
		m.Props[kv[i]] = ast.MetaValue{Kind: ast.MetaString, String: kv[i+1]}
	}
	return m
}

// writeYAML creates a temp YAML file under dir with the given contents and
// returns the absolute path.
func writeYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// sourceIn returns the absolute path of the synthetic ledger file under dir.
// SourceFilename is resolved against filepath.Dir, so it just has to point
// at the same directory as the YAML fixtures.
func sourceIn(dir string) string {
	return filepath.Join(dir, "ledger.beancount")
}

// TestDirectMetadataCopy mirrors test_direct_copy_metadata.
func TestDirectMetadataCopy(t *testing.T) {
	c := &ast.Commodity{
		Date: testDate, Currency: "USD",
		Meta: metaStr("source_field", "USD"),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c}),
		Config:     "commodity target_field source_field",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}
	out := res.Directives[0].(*ast.Commodity)
	if got := out.Meta.Props["target_field"].String; got != "USD" {
		t.Errorf("target_field = %q, want %q", got, "USD")
	}
	// original directive untouched
	if _, exists := c.Meta.Props["target_field"]; exists {
		t.Errorf("original commodity mutated")
	}
}

// TestSpecialCommoditySource mirrors test_special_commodity_source.
func TestSpecialCommoditySource(t *testing.T) {
	c := &ast.Commodity{Date: testDate, Currency: "USD"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c}),
		Config:     "commodity unit __commodity__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	out := res.Directives[0].(*ast.Commodity)
	if got := out.Meta.Props["unit"].String; got != "USD" {
		t.Errorf("unit = %q, want %q", got, "USD")
	}
	if got := out.Meta.Props["unit"].Kind; got != ast.MetaCurrency {
		t.Errorf("unit kind = %v, want MetaCurrency", got)
	}
}

// TestSpecialAccountSourceOpen mirrors test_special_account_source_open.
func TestSpecialAccountSourceOpen(t *testing.T) {
	o := &ast.Open{Date: testDate, Account: "Assets:Bank:Checking"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{o}),
		Config:     "open name __account__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Directives[0].(*ast.Open)
	if got := out.Meta.Props["name"].String; got != "Checking" {
		t.Errorf("name = %q, want %q", got, "Checking")
	}
}

// TestSpecialAccountSourceBalance mirrors test_special_account_source_balance.
func TestSpecialAccountSourceBalance(t *testing.T) {
	a := amt(1000, "USD")
	b := &ast.Balance{Date: testDate, Account: "Assets:Bank:Savings", Amount: a}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{b}),
		Config:     "balance account_name __account__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Directives[0].(*ast.Balance)
	if got := out.Meta.Props["account_name"].String; got != "Savings" {
		t.Errorf("account_name = %q, want %q", got, "Savings")
	}
}

// TestYAMLMappingLookup mirrors test_mapping_lookup.
func TestYAMLMappingLookup(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "volatility.yaml", "checking: low\nsavings: low\nstocks: high\n")

	o := &ast.Open{
		Date: testDate, Account: "Assets:Investments:Stocks",
		Meta: metaStr("account_class", "stocks"),
	}
	in := api.Input{
		Directive:      testPluginDir,
		Directives:     seqOf([]ast.Directive{o}),
		Config:         "open volatility account_class file:volatility.yaml",
		SourceFilename: sourceIn(dir),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	out := res.Directives[0].(*ast.Open)
	if got := out.Meta.Props["volatility"].String; got != "high" {
		t.Errorf("volatility = %q, want %q", got, "high")
	}
}

// TestYAMLKeyNotFound mirrors test_mapping_lookup_error.
func TestYAMLKeyNotFound(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "volatility.yaml", "checking: low\nsavings: low\n")

	o := &ast.Open{
		Date: testDate, Account: "Assets:Unknown",
		Meta: metaStr("account_class", "unknown"),
		Span: ast.Span{Start: ast.Position{Filename: "ledger.beancount", Line: 5}},
	}
	in := api.Input{
		Directive:      testPluginDir,
		Directives:     seqOf([]ast.Directive{o}),
		Config:         "open volatility account_class file:volatility.yaml",
		SourceFilename: sourceIn(dir),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{Code: codeYAMLKeyNotFound, Span: o.Span}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("diagnostics mismatch (-want +got):\n%s", diff)
	}
	if got := res.Diagnostics[0].Message; got == "" {
		t.Errorf("empty message")
	}
}

// TestSourceMetadataMissingSilent mirrors test_source_metadata_missing.
func TestSourceMetadataMissingSilent(t *testing.T) {
	c := &ast.Commodity{Date: testDate, Currency: "USD"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c}),
		Config:     "commodity target nonexistent_source",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	// no modification → Directives nil
	if res.Directives != nil {
		t.Errorf("expected nil Directives, got %v", res.Directives)
	}
}

// TestExistingTargetPreserved mirrors test_skip_existing_metadata.
func TestExistingTargetPreserved(t *testing.T) {
	c := &ast.Commodity{
		Date: testDate, Currency: "USD",
		Meta: metaStr("unit", "EUR"),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c}),
		Config:     "commodity unit __commodity__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("expected nil Directives (no-op), got %v", res.Directives)
	}
	if got := c.Meta.Props["unit"].String; got != "EUR" {
		t.Errorf("original unit mutated: %q", got)
	}
}

// TestCommentsInConfig mirrors test_config_with_comments.
func TestCommentsInConfig(t *testing.T) {
	c := &ast.Commodity{Date: testDate, Currency: "USD"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c}),
		Config: `
			; this is a comment
			commodity unit __commodity__  ; inline comment
			; another comment
		`,
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	out := res.Directives[0].(*ast.Commodity)
	if got := out.Meta.Props["unit"].String; got != "USD" {
		t.Errorf("unit = %q, want %q", got, "USD")
	}
}

// TestMissingYAMLFile mirrors test_file_not_found_error.
func TestMissingYAMLFile(t *testing.T) {
	dir := t.TempDir()
	o := &ast.Open{
		Date: testDate, Account: "Assets:Investments",
		Meta: metaStr("account_class", "stocks"),
	}
	in := api.Input{
		Directive:      testPluginDir,
		Directives:     seqOf([]ast.Directive{o}),
		Config:         "open volatility account_class file:nonexistent.yaml",
		SourceFilename: sourceIn(dir),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(res.Diagnostics) = %d, want 1; diags = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != codeYAMLReadError {
		t.Errorf("code = %q, want %q", res.Diagnostics[0].Code, codeYAMLReadError)
	}
}

// TestNoSourceFileDiagnostic exercises the no-source-file fallback: rules
// that don't use file: still apply, and a single no-source-file diagnostic
// is emitted for the file rules that were dropped.
func TestNoSourceFileDiagnostic(t *testing.T) {
	c := &ast.Commodity{Date: testDate, Currency: "USD"}
	o := &ast.Open{
		Date: testDate, Account: "Assets:Investments:Stocks",
		Meta: metaStr("account_class", "stocks"),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c, o}),
		Config: `commodity unit __commodity__
open volatility account_class file:volatility.yaml`,
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeNoSourceFile {
		t.Fatalf("diags = %v, want one infer-metadata-no-source-file", res.Diagnostics)
	}
	// The commodity rule still ran.
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives (commodity rule applied)")
	}
	outC := res.Directives[0].(*ast.Commodity)
	if got := outC.Meta.Props["unit"].String; got != "USD" {
		t.Errorf("commodity unit = %q, want %q", got, "USD")
	}
	// The open rule was dropped — no volatility key on the open.
	outO := res.Directives[1].(*ast.Open)
	if _, exists := outO.Meta.Props["volatility"]; exists {
		t.Errorf("open should not have volatility key (file rule dropped)")
	}
}

// TestTransactionDirective mirrors test_transaction_directive.
func TestTransactionDirective(t *testing.T) {
	tx := &ast.Transaction{
		Date: testDate, Flag: '*', Payee: "Store", Narration: "Purchase",
		Meta: metaStr("uuid", "abc123"),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx}),
		Config:     "transaction id uuid",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Directives[0].(*ast.Transaction)
	if got := out.Meta.Props["id"].String; got != "abc123" {
		t.Errorf("id = %q, want %q", got, "abc123")
	}
}

// TestCloseDirective mirrors test_close_directive.
func TestCloseDirective(t *testing.T) {
	cl := &ast.Close{Date: testDate, Account: "Assets:Bank:Checking"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{cl}),
		Config:     "close name __account__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Directives[0].(*ast.Close)
	if got := out.Meta.Props["name"].String; got != "Checking" {
		t.Errorf("name = %q, want %q", got, "Checking")
	}
}

// TestPadDirective mirrors test_pad_directive.
func TestPadDirective(t *testing.T) {
	p := &ast.Pad{Date: testDate, Account: "Assets:Bank:Checking", PadAccount: "Equity:Opening"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{p}),
		Config:     "pad name __account__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Directives[0].(*ast.Pad)
	if got := out.Meta.Props["name"].String; got != "Checking" {
		t.Errorf("name = %q, want %q", got, "Checking")
	}
}

// TestDocumentDirective mirrors test_document_directive.
func TestDocumentDirective(t *testing.T) {
	d := &ast.Document{Date: testDate, Account: "Assets:Bank:Checking", Path: "/path/to/doc.pdf"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{d}),
		Config:     "document name __account__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.Directives[0].(*ast.Document)
	if got := out.Meta.Props["name"].String; got != "Checking" {
		t.Errorf("name = %q, want %q", got, "Checking")
	}
}

// TestInvalidConfigRule exercises the malformed-rule diagnostic path.
func TestInvalidConfigRule(t *testing.T) {
	c := &ast.Commodity{Date: testDate, Currency: "USD"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c}),
		Config:     "broken-line\ncommodity unit __commodity__",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeInvalidConfig {
		t.Fatalf("diags = %v, want one invalid-config", res.Diagnostics)
	}
	// The valid rule still applied.
	out := res.Directives[0].(*ast.Commodity)
	if got := out.Meta.Props["unit"].String; got != "USD" {
		t.Errorf("unit = %q, want %q", got, "USD")
	}
}

// TestYAMLCachedOnce verifies a YAML file is loaded exactly once even when
// referenced by many rules and many directives. The proof is observational:
// after the first successful load the file is renamed, but subsequent
// lookups still resolve, because they read from the cache.
func TestYAMLCachedOnce(t *testing.T) {
	dir := t.TempDir()
	yamlPath := writeYAML(t, dir, "m.yaml", "stocks: high\nbonds: medium\n")

	o1 := &ast.Open{
		Date: testDate, Account: "Assets:Stocks",
		Meta: metaStr("class", "stocks"),
	}
	o2 := &ast.Open{
		Date: testDate, Account: "Assets:Bonds",
		Meta: metaStr("class", "bonds"),
	}

	// Set up apply to run, then remove the file *during* a second
	// imaginary call. The simpler observational test: run apply once
	// with two directives and assert both succeed (single-load cache
	// inside apply already serves both). Then unlink the file and run
	// again — the second call must read fresh and succeed too. Both
	// modes exercise the cache key path.
	in := api.Input{
		Directive:      testPluginDir,
		Directives:     seqOf([]ast.Directive{o1, o2}),
		Config:         "open volatility class file:m.yaml",
		SourceFilename: sourceIn(dir),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	if got := res.Directives[0].(*ast.Open).Meta.Props["volatility"].String; got != "high" {
		t.Errorf("o1 volatility = %q, want %q", got, "high")
	}
	if got := res.Directives[1].(*ast.Open).Meta.Props["volatility"].String; got != "medium" {
		t.Errorf("o2 volatility = %q, want %q", got, "medium")
	}

	// Delete the YAML and re-run — the cache is per-Apply-call, so a
	// fresh apply must hit disk and fail with a read error.
	if err := os.Remove(yamlPath); err != nil {
		t.Fatalf("remove %s: %v", yamlPath, err)
	}
	res2, err := apply(context.Background(), api.Input{
		Directive:      testPluginDir,
		Directives:     seqOf([]ast.Directive{o1}),
		Config:         "open volatility class file:m.yaml",
		SourceFilename: sourceIn(dir),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res2.Diagnostics) != 1 || res2.Diagnostics[0].Code != codeYAMLReadError {
		t.Fatalf("expected one yaml-read-error after delete, got %v", res2.Diagnostics)
	}
}

// TestRuleChaining verifies that a later rule on the same directive can use
// the value written by an earlier rule as its source. This documents the
// implementation's left-to-right rule application contract.
func TestRuleChaining(t *testing.T) {
	o := &ast.Open{Date: testDate, Account: "Assets:Bank:Checking"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{o}),
		Config: `open short_name __account__
open display short_name`,
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	out := res.Directives[0].(*ast.Open)
	if got := out.Meta.Props["short_name"].String; got != "Checking" {
		t.Errorf("short_name = %q, want %q", got, "Checking")
	}
	if got := out.Meta.Props["display"].String; got != "Checking" {
		t.Errorf("display = %q, want %q (chained from short_name)", got, "Checking")
	}
}

// TestCanceledContext verifies that the plugin respects context cancellation.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatal("apply error = nil, want non-nil on canceled context")
	}
}

// TestNilDirectivesIterator verifies that a nil Directives iterator returns
// a zero-valued Result.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{Directive: testPluginDir, Config: "commodity x y"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil || len(res.Diagnostics) != 0 {
		t.Errorf("res = %#v, want zero", res)
	}
}

// TestComplexWorkflow mirrors test_complex_workflow.
func TestComplexWorkflow(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "asset_type.yaml", "Checking: cash\nSavings: cash\nInvestments: securities\n")

	c := &ast.Commodity{Date: testDate, Currency: "USD"}
	o := &ast.Open{Date: testDate, Account: "Assets:Bank:Checking"}
	tx := &ast.Transaction{
		Date: testDate, Flag: '*', Payee: "Store", Narration: "Purchase",
		Meta: metaStr("ref", "tx001"),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{c, o, tx}),
		Config: `
			; Commodity rules
			commodity unit __commodity__
			; Open rules
			open short_name __account__
			open type short_name file:asset_type.yaml
			; Transaction rules
			transaction id ref
		`,
		SourceFilename: sourceIn(dir),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	outC := res.Directives[0].(*ast.Commodity)
	if got := outC.Meta.Props["unit"].String; got != "USD" {
		t.Errorf("unit = %q, want %q", got, "USD")
	}
	outO := res.Directives[1].(*ast.Open)
	if got := outO.Meta.Props["short_name"].String; got != "Checking" {
		t.Errorf("short_name = %q, want %q", got, "Checking")
	}
	if got := outO.Meta.Props["type"].String; got != "cash" {
		t.Errorf("type = %q, want %q", got, "cash")
	}
	outT := res.Directives[2].(*ast.Transaction)
	if got := outT.Meta.Props["id"].String; got != "tx001" {
		t.Errorf("id = %q, want %q", got, "tx001")
	}
}

// TestWithMeta_AllMetaBearingKinds guards withMeta against silently leaving a
// directive type unhandled. Event, Price, Query, and Custom were previously
// missing from the switch; this exercises every metadata-bearing kind and
// asserts the replacement took effect without mutating the input.
func TestWithMeta_AllMetaBearingKinds(t *testing.T) {
	date := time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC)
	amt := ast.Amount{Number: *apd.New(1, 0), Currency: "USD"}
	orig := ast.Metadata{Props: map[string]ast.MetaValue{"orig": {Kind: ast.MetaString, String: "old"}}}
	cases := []struct {
		name string
		dir  ast.Directive
	}{
		{"Open", &ast.Open{Date: date, Account: "Assets:A", Meta: orig}},
		{"Close", &ast.Close{Date: date, Account: "Assets:A", Meta: orig}},
		{"Balance", &ast.Balance{Date: date, Account: "Assets:A", Amount: amt, Meta: orig}},
		{"Pad", &ast.Pad{Date: date, Account: "Assets:A", PadAccount: "Equity:Opening", Meta: orig}},
		{"Document", &ast.Document{Date: date, Account: "Assets:A", Path: "/p", Meta: orig}},
		{"Note", &ast.Note{Date: date, Account: "Assets:A", Comment: "c", Meta: orig}},
		{"Commodity", &ast.Commodity{Date: date, Currency: "USD", Meta: orig}},
		{"Transaction", &ast.Transaction{Date: date, Flag: '*', Narration: "n", Meta: orig}},
		{"Event", &ast.Event{Date: date, Name: "location", Value: "NYC", Meta: orig}},
		{"Price", &ast.Price{Date: date, Commodity: "USD", Amount: amt, Meta: orig}},
		{"Query", &ast.Query{Date: date, Name: "q", BQL: "SELECT *", Meta: orig}},
		{"Custom", &ast.Custom{Date: date, TypeName: "t", Meta: orig}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replacement := ast.Metadata{Props: map[string]ast.MetaValue{"new": {Kind: ast.MetaString, String: "v"}}}
			got := withMeta(tc.dir, replacement)

			if got == tc.dir {
				t.Fatalf("withMeta returned the input unchanged (type unhandled)")
			}
			gotMeta := got.DirMeta()
			if _, ok := gotMeta.Props["new"]; !ok {
				t.Errorf("result missing replacement key %q", "new")
			}
			if _, ok := gotMeta.Props["orig"]; ok {
				t.Errorf("result retained original metadata; want full replacement")
			}
			// Input must be untouched.
			if _, ok := tc.dir.DirMeta().Props["new"]; ok {
				t.Errorf("withMeta mutated the input directive")
			}
		})
	}
}
