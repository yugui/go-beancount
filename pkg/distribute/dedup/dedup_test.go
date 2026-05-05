package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

func mustParse(t *testing.T, src string) ast.Directive {
	t.Helper()
	ledger, err := ast.Load(src, ast.WithBaseDir(""))
	if err != nil {
		t.Fatalf("ast.Load(%q): %v", src, err)
	}
	if ledger.Len() == 0 {
		t.Fatalf("ast.Load(%q): no directives parsed", src)
	}
	return ledger.At(0)
}

// writeFile writes contents under dir/relPath, creating parents as
// needed, and returns the absolute path.
func writeFile(t *testing.T, dir, relPath, contents string) string {
	t.Helper()
	abs := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %q: %v", abs, err)
	}
	return abs
}

func TestBuildIndex_RecordsActive(t *testing.T) {
	root := t.TempDir()
	ledgerPath := writeFile(t, root, "main.beancount", `2024-01-15 price USD 110 JPY
`)
	idx, diags, err := BuildIndex(context.Background(), ledgerPath, root)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v, want none", diags)
	}
	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")
	matched, kind := idx.InDestination("main.beancount", d, nil)
	if !matched || kind != MatchAST {
		t.Errorf("InDestination: matched=%v kind=%v, want true MatchAST", matched, kind)
	}
}

func TestBuildIndex_RecordsCommented(t *testing.T) {
	root := t.TempDir()
	ledgerPath := writeFile(t, root, "main.beancount", `; 2024-01-15 price USD 110 JPY
`)
	idx, _, err := BuildIndex(context.Background(), ledgerPath, root)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")
	matched, kind := idx.InDestination("main.beancount", d, nil)
	if !matched || kind != MatchAST {
		t.Errorf("InDestination over commented: matched=%v kind=%v, want true MatchAST", matched, kind)
	}
	// Commented entries elsewhere must not satisfy InOtherActive.
	matched, _ = idx.InOtherActive("other.beancount", d, nil)
	if matched {
		t.Errorf("InOtherActive matched a commented entry; want false")
	}
}

func TestBuildIndex_PathCanonicalization(t *testing.T) {
	configRoot := t.TempDir()
	outsideDir := t.TempDir()

	// Write the active price into a file outside configRoot, then a
	// root ledger inside configRoot that includes it via absolute path.
	outsidePath := writeFile(t, outsideDir, "elsewhere.beancount", `2024-01-15 price USD 110 JPY
`)
	rootLedger := writeFile(t, configRoot, "main.beancount", `include "`+outsidePath+`"
`)

	idx, _, err := BuildIndex(context.Background(), rootLedger, configRoot)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")

	// The included file is outside configRoot, so its key is `..`-prefixed.
	// A query for any in-root path must miss it under InDestination.
	matched, _ := idx.InDestination("quotes/USD/202401.beancount", d, nil)
	if matched {
		t.Errorf("InDestination from in-root path matched an outside-root entry; want false")
	}
	// But InOtherActive should still see the active outside entry: the
	// key differs from the queried path, satisfying the "elsewhere"
	// scope, and outside-root files are walked just like in-root ones.
	matched, _ = idx.InOtherActive("quotes/USD/202401.beancount", d, nil)
	if !matched {
		t.Errorf("InOtherActive did not see the outside-root active entry; want true")
	}
}

func TestBuildIndex_ContextCancellation(t *testing.T) {
	root := t.TempDir()
	incPath := writeFile(t, root, "inc.beancount", `2024-01-15 price USD 110 JPY
`)
	ledgerPath := writeFile(t, root, "main.beancount", `include "`+incPath+`"
2024-02-15 price USD 111 JPY
`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := BuildIndex(ctx, ledgerPath, root)
	if err == nil {
		t.Fatal("BuildIndex with cancelled ctx: got nil error, want ctx.Err()")
	}
	if err != context.Canceled {
		t.Errorf("BuildIndex error = %v, want context.Canceled", err)
	}
}

func TestEqualityOpts_IgnoresSpan(t *testing.T) {
	a := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	a.Span = ast.Span{Start: ast.Position{Filename: "x.beancount", Line: 1}}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	b.Span = ast.Span{Start: ast.Position{Filename: "y.beancount", Line: 99}}
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent ignoring Span: got %v, want MatchAST", k)
	}
}

func TestEqualityOpts_IgnoresOverrideKey(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"route-account": {Kind: ast.MetaString, String: "Assets:Other"}}},
	}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent ignoring override key (nil meta): got %v, want MatchAST", k)
	}
	// Two directives that disagree only on the override key value also compare equal.
	c := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"route-account": {Kind: ast.MetaString, String: "Assets:X"}}},
	}
	if k := equivalent(a, c, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent with both override keys: got %v, want MatchAST", k)
	}
}

func TestEquivalent_MetaKeyMatch(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"import-id": {Kind: ast.MetaString, String: "abc"}}},
	}
	b := &ast.Open{
		Date:    mustDate(t, "2024-02-20"),
		Account: "Assets:B",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"import-id": {Kind: ast.MetaString, String: "abc"}}},
	}
	if k := equivalent(a, b, "route-account", []string{"import-id"}); k != MatchMeta {
		t.Errorf("equivalent with import-id match: got %v, want MatchMeta", k)
	}
	// Different values must not match.
	b.Meta.Props["import-id"] = ast.MetaValue{Kind: ast.MetaString, String: "xyz"}
	if k := equivalent(a, b, "route-account", []string{"import-id"}); k != MatchNone {
		t.Errorf("equivalent with differing import-id: got %v, want MatchNone", k)
	}
}

func TestIndex_InOtherActiveScope(t *testing.T) {
	idx := &memoryIndex{}
	d := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	idx.Add("Q.beancount", d, false)

	probe := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if matched, _ := idx.InOtherActive("P.beancount", probe, nil); !matched {
		t.Error("InOtherActive: active entry at Q should match query at P")
	}

	idx2 := &memoryIndex{}
	idx2.Add("Q.beancount", d, true)
	if matched, _ := idx2.InOtherActive("P.beancount", probe, nil); matched {
		t.Error("InOtherActive: commented entry at Q must not match query at P")
	}
}

func TestIndex_AddAffectsSubsequentQueries(t *testing.T) {
	idx := &memoryIndex{}
	d := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if matched, _ := idx.InDestination("P.beancount", d, nil); matched {
		t.Fatal("InDestination on empty index: matched=true, want false")
	}

	idx.Add("P.beancount", d, false)
	if matched, kind := idx.InDestination("P.beancount", d, nil); !matched || kind != MatchAST {
		t.Errorf("after Add(active): matched=%v kind=%v, want true MatchAST", matched, kind)
	}

	idx2 := &memoryIndex{}
	idx2.Add("P.beancount", d, true)
	if matched, kind := idx2.InDestination("P.beancount", d, nil); !matched || kind != MatchAST {
		t.Errorf("after Add(commented): matched=%v kind=%v, want true MatchAST", matched, kind)
	}
}

// Sanity: the MatchKind String form is not part of the API but making
// failures readable helps debug; the test keeps it import-less by
// using a simple compare against raw integers.
func TestMatchKindValues(t *testing.T) {
	if MatchNone != 0 || MatchAST != 1 || MatchMeta != 2 {
		t.Errorf("MatchKind iota drifted: None=%d AST=%d Meta=%d", MatchNone, MatchAST, MatchMeta)
	}
}

// BuildIndex must surface ledger diagnostics so the CLI's policy can
// decide whether to abort. Use a missing include to provoke an error.
func TestBuildIndex_SurfacesDiagnostics(t *testing.T) {
	root := t.TempDir()
	ledgerPath := writeFile(t, root, "main.beancount", `include "missing.beancount"
`)
	_, diags, err := BuildIndex(context.Background(), ledgerPath, root)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	hasError := false
	for _, d := range diags {
		if d.Severity == ast.Error {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("expected error diagnostic for unresolved include; got %+v", diags)
	}
}
