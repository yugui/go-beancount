package ast_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestLoad_StringNoIncludes(t *testing.T) {
	src := "2024-01-01 open Assets:Bank USD\n"
	ledger := ast.Load(src)
	if ledger == nil {
		t.Fatal("Load returned nil")
	}
	if got := len(ledger.Diagnostics); got != 0 {
		t.Errorf("Diagnostics count = %d, want 0", got)
		for _, d := range ledger.Diagnostics {
			t.Logf("  %s", d.Message)
		}
	}
	if got := len(ledger.Files); got != 1 {
		t.Fatalf("Files count = %d, want 1", got)
	}
	if got := ledger.Files[0].Filename; got != "" {
		t.Errorf("Files[0].Filename = %q, want empty (no WithFilename)", got)
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
}

func TestLoad_StringWithRelativeIncludeUsesPwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sub.beancount"),
		[]byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	ledger := ast.Load(`include "sub.beancount"` + "\n")

	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1 (open from sub.beancount)", got)
	}
}

func TestLoad_StringWithFilenameResolvesRelativeTo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sub.beancount"),
		[]byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledger := ast.Load(
		`include "sub.beancount"`+"\n",
		ast.WithFilename(filepath.Join(dir, "main.beancount")),
	)

	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
	if got := ledger.Files[0].Filename; got != filepath.Join(dir, "main.beancount") {
		t.Errorf("Files[0].Filename = %q, want %q", got, filepath.Join(dir, "main.beancount"))
	}
}

func TestLoad_StringSelfInclude(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.beancount")

	ledger := ast.Load(
		`include "main.beancount"`+"\n",
		ast.WithFilename(main),
	)

	found := false
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error && strings.Contains(d.Message, "circular include") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected circular-include diagnostic; got %d diagnostics", len(ledger.Diagnostics))
		for _, d := range ledger.Diagnostics {
			t.Logf("  %s", d.Message)
		}
	}
}

func TestLoadReader_PropagatesReadError(t *testing.T) {
	want := errors.New("synthetic read failure")
	ledger, err := ast.LoadReader(iotest.ErrReader(want))
	if ledger != nil {
		t.Errorf("ledger = %v, want nil on read error", ledger)
	}
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want wrap of %v", err, want)
	}
}

func TestLoadReader_DelegatesToLoad(t *testing.T) {
	src := "2024-01-01 open Assets:Bank USD\n"
	ledger, err := ast.LoadReader(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadReader: %v", err)
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
}

// TestLoadReader_OptionsAreThreaded confirms WithFilename reaches LoadReader.
func TestLoadReader_OptionsAreThreaded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sub.beancount"),
		[]byte("2024-01-01 open Assets:Bank USD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := io.Reader(strings.NewReader(`include "sub.beancount"` + "\n"))
	ledger, err := ast.LoadReader(r, ast.WithFilename(filepath.Join(dir, "main.beancount")))
	if err != nil {
		t.Fatalf("LoadReader: %v", err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}
	if got := ledger.Len(); got != 1 {
		t.Errorf("Directives count = %d, want 1", got)
	}
}
