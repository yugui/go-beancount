package ast_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestLoad_SingleFile(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(root, []byte("2024-01-01 open Assets:Bank USD\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ledger, err := ast.Load(root)
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

	ledger, err := ast.Load(root)
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

	ledger, err := ast.Load(a)
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

	ledger, err := ast.Load(root)
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

	ledger, err := ast.Load(a)
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

	ledger, err := ast.Load(root)
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
	_, err := ast.Load("/nonexistent/path/file.beancount")
	// Should not panic. The root file not being found results in
	// a Ledger with diagnostics (not a returned error).
	_ = err
}
