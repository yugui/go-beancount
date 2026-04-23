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
	"path/filepath"

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
		path := resolvePath(doc.Path, doc.Span.Start.Filename, in.LedgerRoot)
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

// resolvePath resolves docPath to an absolute path using the following chain:
//
//  1. If docPath is already absolute, return it as-is.
//  2. Otherwise, anchor it to the directory of spanFilename.
//  3. If spanFilename is itself relative (or empty), first resolve it against
//     the directory of ledgerRoot.
//  4. If ledgerRoot is also relative (or empty), resolve it against the
//     process working directory.
func resolvePath(docPath, spanFilename, ledgerRoot string) string {
	if filepath.IsAbs(docPath) {
		return docPath
	}

	var baseDir string
	if filepath.IsAbs(spanFilename) {
		baseDir = filepath.Dir(spanFilename)
	} else {
		rootAbs := ledgerRoot
		if !filepath.IsAbs(rootAbs) {
			cwd, _ := os.Getwd()
			rootAbs = filepath.Join(cwd, rootAbs)
		}
		rootDir := filepath.Dir(rootAbs)
		if spanFilename == "" {
			baseDir = rootDir
		} else {
			baseDir = filepath.Dir(filepath.Join(rootDir, spanFilename))
		}
	}

	return filepath.Join(baseDir, docPath)
}

func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/document", Plugin)
}
