package ast_test

import (
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
		t.Fatalf("LoadFile(%q): %v", root, err)
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
		t.Fatalf("LoadFile(%q): %v", root, err)
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
		t.Fatalf("LoadFile(%q): %v", a, err)
	}
	// TODO(diag-code): Diagnostic carries no machine-readable code
	// today, so the test matches on the message substring. Switch to a
	// typed comparison when a Code/Kind field is added.
	found := false
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error && strings.Contains(d.Message, "circular include") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LoadFile(%q): no circular-include diagnostic; got %d diagnostics", a, len(ledger.Diagnostics))
		for _, d := range ledger.Diagnostics {
			t.Logf("  %s", d.Message)
		}
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
		t.Fatalf("LoadFile(%q): %v", root, err)
	}
	if got := len(ledger.Diagnostics); got == 0 {
		t.Errorf("LoadFile(%q): got %d diagnostics, want at least 1 for missing include file", root, got)
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
		t.Fatalf("LoadFile(%q): %v", a, err)
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
		t.Fatalf("LoadFile(%q): %v", root, err)
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
	// The root file not being found results in a Ledger with an error
	// diagnostic, not a returned error: callers can still inspect any
	// directives loaded before the failure.
	const path = "/nonexistent/path/file.beancount"
	ledger, err := ast.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): unexpected error return: %v", path, err)
	}
	if ledger == nil {
		t.Fatalf("LoadFile(%q): ledger is nil, want non-nil", path)
	}
	found := false
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LoadFile(%q): no error diagnostic for missing root file; got %d diagnostics",
			path, len(ledger.Diagnostics))
	}
}
