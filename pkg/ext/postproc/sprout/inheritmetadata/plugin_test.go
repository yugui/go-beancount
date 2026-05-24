package inheritmetadata

import (
	"context"
	"iter"
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

var testDate = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// metaStr builds an ast.Metadata with a single string key.
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

// openDir builds a minimal *ast.Open for the given account with optional metadata.
func openDir(account ast.Account, meta ast.Metadata) *ast.Open {
	return &ast.Open{Date: testDate, Account: account, Meta: meta}
}

// collectOpens returns the *ast.Open directives from res.Directives
// in index order.
func collectOpens(dirs []ast.Directive) []*ast.Open {
	var out []*ast.Open
	for _, d := range dirs {
		if o, ok := d.(*ast.Open); ok {
			out = append(out, o)
		}
	}
	return out
}

// TestInheritSingleMetadataFromParent mirrors test_inherit_single_metadata_from_parent.
func TestInheritSingleMetadataFromParent(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Diagnostics)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives (child was modified)")
	}

	opens := collectOpens(res.Directives)
	if len(opens) != 2 {
		t.Fatalf("len(opens) = %d, want 2", len(opens))
	}
	// Parent unchanged.
	if got := opens[0].Meta.Props["region"].String; got != "US" {
		t.Errorf("parent region = %q, want %q", got, "US")
	}
	// Child inherits.
	if got := opens[1].Meta.Props["region"].String; got != "US" {
		t.Errorf("child region = %q, want %q", got, "US")
	}
}

// TestInheritMultipleMetadataFromParent mirrors test_inherit_multiple_metadata_from_parent.
func TestInheritMultipleMetadataFromParent(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US", "tax_category", "taxable"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)
	if got := opens[1].Meta.Props["region"].String; got != "US" {
		t.Errorf("child region = %q, want %q", got, "US")
	}
	if got := opens[1].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("child tax_category = %q, want %q", got, "taxable")
	}
}

// TestPreserveExistingMetadata mirrors test_preserve_existing_metadata.
func TestPreserveExistingMetadata(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US"))
	child := openDir("Assets:Bank:Checking", metaStr("region", "JP"))

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No override needed — Directives should be nil (no modification).
	if res.Directives != nil {
		t.Errorf("expected nil Directives (child already has region), got %v", res.Directives)
	}

	// Confirm the child's own value is "JP" (original unchanged).
	if got := child.Meta.Props["region"].String; got != "JP" {
		t.Errorf("original child region mutated: %q, want %q", got, "JP")
	}
}

// TestInheritFromGrandparent mirrors test_inherit_from_grandparent.
func TestInheritFromGrandparent(t *testing.T) {
	grandparent := openDir("Assets", metaStr("region", "US"))
	parent := openDir("Assets:Bank", ast.Metadata{})
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{grandparent, parent, child}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)
	if got := opens[1].Meta.Props["region"].String; got != "US" {
		t.Errorf("parent region = %q, want %q", got, "US")
	}
	if got := opens[2].Meta.Props["region"].String; got != "US" {
		t.Errorf("child region = %q, want %q", got, "US")
	}
}

// TestPartialInheritance mirrors test_partial_inheritance.
func TestPartialInheritance(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US", "tax_category", "taxable"))
	child := openDir("Assets:Bank:Checking", metaStr("region", "JP"))

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives (tax_category was inherited)")
	}

	opens := collectOpens(res.Directives)
	// region kept, tax_category inherited.
	if got := opens[1].Meta.Props["region"].String; got != "JP" {
		t.Errorf("child region = %q, want %q (own value preserved)", got, "JP")
	}
	if got := opens[1].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("child tax_category = %q, want %q", got, "taxable")
	}
}

// TestNoParentWithMetadata mirrors test_no_parent_with_metadata.
func TestNoParentWithMetadata(t *testing.T) {
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{child}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("expected nil Directives (no ancestor has region), got %v", res.Directives)
	}
}

// TestEmptyConfig mirrors test_empty_config.
func TestEmptyConfig(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("expected nil Directives (no-op with empty config), got %v", res.Directives)
	}
}

// TestConfigWithWhitespace mirrors test_config_with_whitespace.
func TestConfigWithWhitespace(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US", "tax_category", "taxable"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "  region  \n  tax_category  ",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)
	if got := opens[1].Meta.Props["region"].String; got != "US" {
		t.Errorf("child region = %q, want %q", got, "US")
	}
	if got := opens[1].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("child tax_category = %q, want %q", got, "taxable")
	}
}

// TestNonOpenDirectivesUnchanged mirrors test_non_open_directives_unchanged.
func TestNonOpenDirectivesUnchanged(t *testing.T) {
	open := openDir("Assets:Bank", metaStr("region", "US"))
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: testDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	note := &ast.Note{Date: testDate, Account: "Assets:Bank", Comment: "test"}

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{open, tx, note}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No child Open to inherit — the only Open (Assets:Bank) already has region.
	// Directives may be nil (no modification) or non-nil but tx/note must be identical.
	dirs := res.Directives
	if dirs == nil {
		// no modification is fine
		return
	}
	if len(dirs) != 3 {
		t.Fatalf("len(dirs) = %d, want 3", len(dirs))
	}
	if dirs[1] != tx {
		t.Errorf("transaction pointer changed (got %p, want %p); non-Open directives must pass through unchanged", dirs[1], tx)
	}
	if dirs[2] != note {
		t.Errorf("note pointer changed (got %p, want %p); non-Open directives must pass through unchanged", dirs[2], note)
	}
}

// TestClosestParentWins mirrors test_closest_parent_wins.
func TestClosestParentWins(t *testing.T) {
	root := openDir("Assets", metaStr("region", "US"))
	parent := openDir("Assets:Bank", metaStr("region", "JP"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{root, parent, child}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)
	if got := opens[2].Meta.Props["region"].String; got != "JP" {
		t.Errorf("child region = %q, want %q (closest parent JP wins)", got, "JP")
	}
}

// TestComplexHierarchy mirrors test_complex_hierarchy.
func TestComplexHierarchy(t *testing.T) {
	root := openDir("Assets", metaStr("region", "US", "tax_category", "taxable"))
	level1 := openDir("Assets:Bank", metaStr("region", "JP"))
	level2 := openDir("Assets:Bank:Savings", ast.Metadata{})
	level3 := openDir("Assets:Bank:Savings:Emergency", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{root, level1, level2, level3}),
		Config:     "region\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)

	// level1: keeps own region=JP, inherits tax_category=taxable from root.
	if got := opens[1].Meta.Props["region"].String; got != "JP" {
		t.Errorf("level1 region = %q, want %q", got, "JP")
	}
	if got := opens[1].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("level1 tax_category = %q, want %q", got, "taxable")
	}

	// level2: inherits region=JP from level1, tax_category=taxable from root.
	if got := opens[2].Meta.Props["region"].String; got != "JP" {
		t.Errorf("level2 region = %q, want %q", got, "JP")
	}
	if got := opens[2].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("level2 tax_category = %q, want %q", got, "taxable")
	}

	// level3: same as level2.
	if got := opens[3].Meta.Props["region"].String; got != "JP" {
		t.Errorf("level3 region = %q, want %q", got, "JP")
	}
	if got := opens[3].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("level3 tax_category = %q, want %q", got, "taxable")
	}
}

// TestMetadataNotInConfigNotInherited mirrors test_metadata_not_in_config_not_inherited.
func TestMetadataNotInConfigNotInherited(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US", "tax_category", "taxable", "currency_type", "fiat"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)
	if got := opens[1].Meta.Props["region"].String; got != "US" {
		t.Errorf("child region = %q, want %q", got, "US")
	}
	if got := opens[1].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("child tax_category = %q, want %q", got, "taxable")
	}
	if _, ok := opens[1].Meta.Props["currency_type"]; ok {
		t.Errorf("child should not have currency_type (not in config)")
	}
}

// TestNoMutationOfOriginalOpen verifies that the plugin never mutates
// an input Open directive.
func TestNoMutationOfOriginalOpen(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	// The original child's Meta.Props must still be nil (not mutated).
	if child.Meta.Props != nil {
		t.Errorf("apply mutated child.Meta.Props (was nil, now %v)", child.Meta.Props)
	}

	// The returned Open must be a different pointer.
	opens := collectOpens(res.Directives)
	if opens[1] == child {
		t.Errorf("returned Open is the same pointer as the input (mutation risk)")
	}
}

// TestCommentLinesSkippedInConfig verifies that ';' comment lines in the
// config are ignored.
func TestCommentLinesSkippedInConfig(t *testing.T) {
	parent := openDir("Assets:Bank", metaStr("region", "US", "tax_category", "taxable"))
	child := openDir("Assets:Bank:Checking", ast.Metadata{})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{parent, child}),
		Config:     "region\n; this is a comment\ntax_category",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives == nil {
		t.Fatal("expected non-nil Directives")
	}

	opens := collectOpens(res.Directives)
	if got := opens[1].Meta.Props["region"].String; got != "US" {
		t.Errorf("child region = %q, want %q", got, "US")
	}
	if got := opens[1].Meta.Props["tax_category"].String; got != "taxable" {
		t.Errorf("child tax_category = %q, want %q", got, "taxable")
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
	res, err := apply(context.Background(), api.Input{Directive: testPluginDir, Config: "region"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil", res.Directives)
	}
}
