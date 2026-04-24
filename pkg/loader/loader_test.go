package loader_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
	"github.com/yugui/go-beancount/pkg/validation/document"
)

// TestLoad_StringRunsPipeline confirms loader.Load runs the default plugin
// pipeline against an inline source: the document plugin should report a
// missing-file diagnostic for a Document directive that points at a file
// that does not exist.
func TestLoad_StringRunsPipeline(t *testing.T) {
	src := `2024-01-01 open Assets:Cash
2024-01-05 document Assets:Cash "/nonexistent/receipt.pdf"
`
	ledger, errs := loader.Load(context.Background(), src)
	if ledger == nil {
		t.Fatal("ledger is nil")
	}
	if len(errs) != 1 {
		t.Fatalf("len(errs) = %d, want 1", len(errs))
	}
	if errs[0].Code != document.CodeDocumentMissing {
		t.Errorf("err code = %q, want %q", errs[0].Code, document.CodeDocumentMissing)
	}
}

// TestLoadReader_RunsPipeline confirms loader.LoadReader threads the source
// through the same pipeline as loader.Load.
func TestLoadReader_RunsPipeline(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "receipt.pdf")
	if err := os.WriteFile(doc, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := "2024-01-01 open Assets:Cash\n" +
		`2024-01-05 document Assets:Cash "` + doc + `"` + "\n"

	ledger, errs, err := loader.LoadReader(context.Background(), strings.NewReader(src))
	if err != nil {
		t.Fatalf("loader.LoadReader: %v", err)
	}
	if ledger == nil {
		t.Fatal("ledger is nil")
	}
	if len(errs) != 0 {
		t.Errorf("len(errs) = %d, want 0; errs = %v", len(errs), errs)
	}
}

// TestLoadReader_PropagatesReadError confirms a Reader failure surfaces as
// the third return value rather than as a diagnostic.
func TestLoadReader_PropagatesReadError(t *testing.T) {
	want := errors.New("synthetic read failure")
	ledger, errs, err := loader.LoadReader(context.Background(), iotest.ErrReader(want))
	if ledger != nil {
		t.Errorf("ledger = %v, want nil on read failure", ledger)
	}
	if errs != nil {
		t.Errorf("errs = %v, want nil on read failure", errs)
	}
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want wrap of %v", err, want)
	}
}

// TestLoad_DocumentPluginUsesLedgerForResolution confirms the document
// plugin can resolve a relative document path through the ledger's root
// file (provided by api.Input.Ledger via ast.ResolvePath).
func TestLoad_DocumentPluginUsesLedgerForResolution(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "receipt.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := "2024-01-01 open Assets:Cash\n" +
		`2024-01-05 document Assets:Cash "receipt.pdf"` + "\n"

	ledger, errs := loader.Load(
		context.Background(),
		src,
		ast.WithFilename(filepath.Join(dir, "main.beancount")),
	)
	if ledger == nil {
		t.Fatal("ledger is nil")
	}
	if len(errs) != 0 {
		t.Errorf("len(errs) = %d, want 0; errs = %v", len(errs), errs)
	}
}
