// Package document implements the document-directive pre-builtin plugin,
// mirroring the verify_document_files_exist phase from
// beancount/ops/documents.py: every Document directive in the ledger is
// checked against the filesystem, and a document-missing-file diagnostic is
// emitted for any path that does not exist.
//
// The document-directory scanning phase (process_documents in upstream) is
// intentionally omitted: walking a directory tree requires os.File.ReadDir,
// which is currently registered as a vulnerable symbol in the Go vulnerability
// database. That phase will be re-introduced once the underlying stdlib issue
// is resolved upstream.
//
// The plugin is also self-registered under its import path so that a
// `plugin "github.com/yugui/go-beancount/pkg/validation/document"` directive
// in a beancount file can activate it explicitly.
package document

import (
	"context"
	"fmt"
	"os"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// CodeDocumentMissing is emitted when a Document directive references a path
// that does not exist on the filesystem.
const CodeDocumentMissing = "document-missing-file"

// Plugin is the pre-builtin plugin for document directive verification.
var Plugin api.PluginFunc = func(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	var errs []api.Error
	for _, d := range in.Directives {
		doc, ok := d.(*ast.Document)
		if !ok {
			continue
		}
		path := ast.ResolvePath(in.Ledger, doc.Span.Start.Filename, doc.Path)
		if _, err := os.Stat(path); err != nil {
			errs = append(errs, api.Error{
				Code:    CodeDocumentMissing,
				Span:    doc.Span,
				Message: fmt.Sprintf("document %q does not exist", path),
			})
		}
	}
	return api.Result{Errors: errs}, nil
}

func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/document", Plugin)
}
