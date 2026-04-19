package document_test

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/document"
)

func seqOf(ds []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range ds {
			if !yield(i, d) {
				return
			}
		}
	}
}

func date(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func TestPlugin_EmptyLedger(t *testing.T) {
	res, err := document.Plugin(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Directives = %v, want nil", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", res.Errors)
	}
}

func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := document.Plugin(ctx, api.Input{})
	if err == nil {
		t.Fatal("expected non-nil error for canceled context")
	}
}

func TestPlugin_NoDocumentDirectives(t *testing.T) {
	open := &ast.Open{Date: date(2024, 1, 1), Account: "Assets:Cash"}
	res, err := document.Plugin(context.Background(), api.Input{
		Directives: seqOf([]ast.Directive{open}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Directives = non-nil, want nil")
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", res.Errors)
	}
}

// TestPlugin_DocumentExists confirms that a Document directive whose file
// exists on disk produces no errors.
func TestPlugin_DocumentExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "receipt.pdf")
	if err := os.WriteFile(file, []byte("pdf"), 0600); err != nil {
		t.Fatal(err)
	}

	doc := &ast.Document{Date: date(2024, 1, 5), Account: "Assets:Cash", Path: file}
	res, err := document.Plugin(context.Background(), api.Input{
		Directives: seqOf([]ast.Directive{doc}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", res.Errors)
	}
}

// TestPlugin_DocumentMissing confirms that a Document directive whose file
// is absent produces a document-missing-file error carrying the directive's
// span.
func TestPlugin_DocumentMissing(t *testing.T) {
	span := ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 10}}
	doc := &ast.Document{
		Span:    span,
		Date:    date(2024, 1, 5),
		Account: "Assets:Cash",
		Path:    "/nonexistent/path/2024-01-05 receipt.pdf",
	}
	res, err := document.Plugin(context.Background(), api.Input{
		Directives: seqOf([]ast.Directive{doc}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != document.CodeDocumentMissing {
		t.Errorf("Code = %q, want %q", e.Code, document.CodeDocumentMissing)
	}
	if e.Span != span {
		t.Errorf("Span = %#v, want %#v", e.Span, span)
	}
}

// TestPlugin_MultipleDocuments confirms that each Document directive is
// checked independently: one existing and one missing file produce exactly
// one error for the missing one.
func TestPlugin_MultipleDocuments(t *testing.T) {
	dir := t.TempDir()
	existingFile := filepath.Join(dir, "2024-01-01 present.pdf")
	if err := os.WriteFile(existingFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	docOK := &ast.Document{Date: date(2024, 1, 1), Account: "Assets:Cash", Path: existingFile}
	missingSpan := ast.Span{Start: ast.Position{Filename: "m.beancount", Line: 5}}
	docMissing := &ast.Document{
		Span:    missingSpan,
		Date:    date(2024, 2, 1),
		Account: "Assets:Cash",
		Path:    "/nonexistent/2024-02-01 missing.pdf",
	}

	res, err := document.Plugin(context.Background(), api.Input{
		Directives: seqOf([]ast.Directive{docOK, docMissing}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if res.Errors[0].Code != document.CodeDocumentMissing {
		t.Errorf("Code = %q, want %q", res.Errors[0].Code, document.CodeDocumentMissing)
	}
	if res.Errors[0].Span != missingSpan {
		t.Errorf("Span mismatch: got %#v, want %#v", res.Errors[0].Span, missingSpan)
	}
}
