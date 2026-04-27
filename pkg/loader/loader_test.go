package loader_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

const minimalSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Opening
2024-01-15 * "deposit"
  Assets:Bank        100 USD
  Equity:Opening    -100 USD
`

func TestLoad_String(t *testing.T) {
	ctx := context.Background()
	ledger, err := loader.Load(ctx, minimalSrc)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("Load returned unexpected diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 3 {
		t.Errorf("Directives count = %d, want 3", got)
	}
}

func TestLoadReader_RunsPlugins(t *testing.T) {
	// Unbalanced transaction — the validations plugin must report it.
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Opening
2024-01-15 * "broken"
  Assets:Bank        100 USD
  Equity:Opening     -50 USD
`
	ctx := context.Background()
	ledger, err := loader.LoadReader(ctx, strings.NewReader(src))
	if err != nil {
		t.Fatalf("loader.LoadReader: %v", err)
	}
	var errCount int
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			errCount++
		}
	}
	if errCount == 0 {
		t.Fatal("expected at least one diagnostic for unbalanced transaction")
	}
}

func TestLoadFile_Equivalent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte(minimalSrc), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	ledger, err := loader.LoadFile(ctx, path)
	if err != nil {
		t.Fatalf("loader.LoadFile: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("LoadFile returned unexpected diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 3 {
		t.Errorf("Directives count = %d, want 3", got)
	}
	// LoadFile must stamp the absolute path into spans.
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Files[0].Filename; got != abs {
		t.Errorf("Files[0].Filename = %q, want %q", got, abs)
	}
}
