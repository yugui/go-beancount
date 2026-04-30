package ast_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestLoad_SingleFile(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte("2024-01-01 open Assets:Bank USD\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ledger.Files); got != 1 {
		t.Errorf("Files count = %d, want 1", got)
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
	if got := len(ledger.Diagnostics); got != 0 {
		t.Errorf("Diagnostics count = %d, want 0", got)
	}
}

func TestLoad_WithInclude(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "accounts.beancount"), []byte(
		"2024-01-01 open Assets:Bank USD\n2024-01-01 open Expenses:Food\n",
	), 0644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte(
		"option \"title\" \"Test\"\ninclude \"accounts.beancount\"\n2024-01-15 * \"Test\"\n  Expenses:Food  50 USD\n  Assets:Bank\n",
	), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ledger.Files); got != 2 {
		t.Errorf("Files count = %d, want 2", got)
	}
	// Directives: 1 option + 2 opens + 1 transaction = 4
	// (Include directive is consumed, not in Directives)
	if got := ledger.Len(); got != 4 {
		t.Errorf("Directives count = %d, want 4", got)
	}
}

func TestLoad_CircularInclude(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.beancount")
	b := filepath.Join(dir, "b.beancount")
	if err := os.WriteFile(a, []byte("include \"b.beancount\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("include \"a.beancount\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected diagnostic for circular include")
	}
}

func TestLoad_MissingInclude(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte("include \"nonexistent.beancount\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Diagnostics) == 0 {
		t.Error("expected diagnostic for missing include file")
	}
}

func TestLoad_NestedIncludes(t *testing.T) {
	dir := t.TempDir()
	c := filepath.Join(dir, "c.beancount")
	if err := os.WriteFile(c, []byte("2024-01-01 open Equity:Opening\n"), 0644); err != nil {
		t.Fatal(err)
	}

	b := filepath.Join(dir, "b.beancount")
	if err := os.WriteFile(b, []byte("include \"c.beancount\"\n2024-01-01 open Expenses:Food\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := filepath.Join(dir, "a.beancount")
	if err := os.WriteFile(a, []byte("include \"b.beancount\"\n2024-01-01 open Assets:Bank\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ledger.Files); got != 3 {
		t.Errorf("Files count = %d, want 3", got)
	}
	// Directives: from c (1 open) + from b (1 open) + from a (1 open) = 3
	if got := ledger.Len(); got != 3 {
		t.Errorf("Directives count = %d, want 3", got)
	}
}

func TestLoad_WithHeadings(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	content := `* Assets
2024-01-01 open Assets:Bank USD

** Expenses
2024-01-01 open Expenses:Food

* Transactions
2024-01-15 * "Store" "Groceries"
  Expenses:Food  50 USD
  Assets:Bank
`
	if err := os.WriteFile(root, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Len(); got != 3 {
		t.Errorf("Directives count = %d, want 3 (headings should be trivia, not directives)", got)
	}
	if got := len(ledger.Diagnostics); got != 0 {
		t.Errorf("Diagnostics count = %d, want 0 (headings should not produce errors)", got)
		for _, d := range ledger.Diagnostics {
			t.Logf("  diagnostic: %s", d.Message)
		}
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := ast.LoadFile("/nonexistent/path/file.beancount")
	// Should not panic. The root file not being found results in
	// a Ledger with diagnostics (not a returned error).
	_ = err
}

func TestLoad_String_NoIncludes(t *testing.T) {
	ledger, err := ast.Load("2024-01-01 open Assets:Bank USD\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
	if got := len(ledger.Files); got != 1 {
		t.Fatalf("Files count = %d, want 1", got)
	}
	if got := ledger.Files[0].Filename; got != "<input>" {
		t.Errorf("Files[0].Filename = %q, want %q", got, "<input>")
	}
}

func TestLoad_String_WithVirtualFilename(t *testing.T) {
	ledger, err := ast.Load(
		"2024-01-01 open Assets:Bank USD\n",
		ast.WithFilename("fixture.bean"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Files[0].Filename; got != "fixture.bean" {
		t.Errorf("Files[0].Filename = %q, want %q", got, "fixture.bean")
	}
}

func TestLoad_String_RelativeInclude_NoBaseDir(t *testing.T) {
	ledger, err := ast.Load(`include "foo.beancount"` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error && strings.Contains(d.Message, "no base directory configured") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic mentioning missing base directory; got %+v", ledger.Diagnostics)
	}
}

func TestLoad_String_RelativeInclude_WithBaseDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leaf.beancount"),
		[]byte("2024-01-01 open Equity:Opening\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.Load(
		`include "leaf.beancount"`+"\n",
		ast.WithBaseDir(dir),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
}

func TestLoad_String_AbsoluteInclude(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.beancount")
	if err := os.WriteFile(leaf, []byte("2024-01-01 open Equity:Opening\n"), 0644); err != nil {
		t.Fatal(err)
	}

	src := `include "` + leaf + `"` + "\n"
	ledger, err := ast.Load(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
}

func TestLoadReader_HappyPath(t *testing.T) {
	src := "2024-01-01 open Assets:Bank USD\n2024-01-01 open Expenses:Food\n"
	ledger, err := ast.LoadReader(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Len(); got != 2 {
		t.Errorf("Directives count = %d, want 2", got)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestLoadReader_ReadError(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := ast.LoadReader(errReader{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want to wrap %v", err, wantErr)
	}
}

func TestLoad_String_SelfInclude(t *testing.T) {
	// Use an absolute virtual filename so the include path matches the
	// visited-set key and cycle detection fires (relative paths require a
	// base dir, which is a separate code path).
	abs, err := filepath.Abs("self")
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := ast.Load(
		`include "`+abs+`"`+"\n",
		ast.WithFilename(abs),
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ledger.Diagnostics {
		if strings.Contains(d.Message, "circular include") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected circular-include diagnostic; got %+v", ledger.Diagnostics)
	}
}

func TestLoad_GlobInclude_DoubleStar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub", "a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub", "b", "c"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a", "inc.beancount"),
		[]byte("2024-01-01 open Assets:Bank USD\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b", "c", "inc.beancount"),
		[]byte("2024-01-01 open Expenses:Food\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// A non-matching extension should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "sub", "notes.txt"),
		[]byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte(`include "sub/**/*.beancount"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Message)
		}
	}
	if got := len(ledger.Files); got != 3 {
		t.Errorf("Files count = %d, want 3 (root + 2 globbed includes)", got)
	}
	if got := ledger.Len(); got != 2 {
		t.Errorf("Directives count = %d, want 2", got)
	}
}

func TestLoad_GlobInclude_SingleStar(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.beancount", "b.beancount"} {
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte("2024-01-01 open Assets:X\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte(`include "*.beancount"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Message)
		}
	}
	// main.beancount is loaded once via LoadFile; the * glob also picks
	// it up but is filtered by the cycle detector. So Files = main +
	// a + b = 3.
	if got := len(ledger.Files); got != 3 {
		t.Errorf("Files count = %d, want 3", got)
	}
}

func TestLoad_GlobInclude_NoMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte(`include "sub/**/*.beancount"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("unexpected error diagnostic: %s", d.Message)
		}
		if d.Severity == ast.Warning && strings.Contains(d.Message, "matched no files") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning diagnostic for glob with no matches; got %+v", ledger.Diagnostics)
	}
}

func TestLoadFile_OverrideFilename(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte("2024-01-01 open Assets:Bank USD\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.LoadFile(root, ast.WithFilename("virtual"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Files[0].Filename; got != "virtual" {
		t.Errorf("Files[0].Filename = %q, want %q", got, "virtual")
	}
}
