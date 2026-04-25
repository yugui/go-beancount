package ast

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/yugui/go-beancount/internal/loadopt"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// Load parses src as beancount source and recursively resolves include
// directives. Spans use a virtual filename ("<input>" by default; override
// with WithFilename). Relative include paths require WithBaseDir; without
// it they produce a diagnostic and are skipped.
func Load(src string, opts ...LoadOption) (*Ledger, error) {
	o := loadopt.Resolve(opts)
	ld := newLoader()
	ld.visited[o.VirtualFilename] = true
	ld.loadCST(syntax.Parse(src), o.VirtualFilename, o.BaseDir)
	return ld.finish(), nil
}

// LoadReader reads r in its entirety and behaves like Load on the result.
// Read errors from r are returned unwrapped.
func LoadReader(r io.Reader, opts ...LoadOption) (*Ledger, error) {
	cst, err := syntax.ParseReader(r)
	if err != nil {
		return nil, err
	}
	o := loadopt.Resolve(opts)
	ld := newLoader()
	ld.visited[o.VirtualFilename] = true
	ld.loadCST(cst, o.VirtualFilename, o.BaseDir)
	return ld.finish(), nil
}

// LoadFile reads and parses the beancount file at path, recursively
// resolving include directives. Include paths are resolved relative to the
// directory of the file containing the include directive. The absolute path
// is recorded in spans unless overridden with WithFilename, and the file's
// directory is used as the base directory unless overridden with
// WithBaseDir.
func LoadFile(path string, opts ...LoadOption) (*Ledger, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving path %q: %w", path, err)
	}
	defaults := []LoadOption{
		WithFilename(absPath),
		WithBaseDir(filepath.Dir(absPath)),
	}
	o := loadopt.Resolve(append(defaults, opts...))

	ld := newLoader()
	ld.loadFile(absPath, o.VirtualFilename, o.BaseDir)
	return ld.finish(), nil
}

type loader struct {
	visited     map[string]bool // filenames already loaded (cycle detection)
	files       []*File
	directives  []Directive
	diagnostics []Diagnostic
}

func newLoader() *loader {
	return &loader{visited: make(map[string]bool)}
}

func (ld *loader) finish() *Ledger {
	ledger := &Ledger{
		Files:       ld.files,
		Diagnostics: ld.diagnostics,
	}
	ledger.InsertAll(ld.directives)
	return ledger
}

// loadFile parses absPath through syntax.ParseFile and merges the result
// into the loader. filename is the cycle-detection key and span filename
// (typically absPath but may be overridden); baseDir anchors the includes
// contained in the loaded file.
func (ld *loader) loadFile(absPath, filename, baseDir string) {
	if ld.visited[filename] {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Message:  fmt.Sprintf("circular include detected: %s", filename),
			Severity: Error,
		})
		return
	}
	ld.visited[filename] = true
	cst, err := syntax.ParseFile(absPath)
	if err != nil {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Message:  fmt.Sprintf("reading file %s: %v", absPath, err),
			Severity: Error,
		})
		return
	}
	ld.loadCST(cst, filename, baseDir)
}

// loadCST lowers cst, records its file and diagnostics, and recursively
// resolves any include directives it contains. Cycle detection is the
// responsibility of the caller (already done by loadFile or by the
// top-level entry point).
func (ld *loader) loadCST(cst *syntax.File, filename, baseDir string) {
	file := Lower(filename, cst)
	ld.files = append(ld.files, file)
	ld.diagnostics = append(ld.diagnostics, file.Diagnostics...)

	for _, d := range file.Directives {
		inc, ok := d.(*Include)
		if !ok {
			ld.directives = append(ld.directives, d)
			continue
		}
		ld.handleInclude(inc, baseDir)
	}
}

func (ld *loader) handleInclude(inc *Include, baseDir string) {
	incPath := inc.Path
	if !filepath.IsAbs(incPath) {
		if baseDir == "" {
			ld.diagnostics = append(ld.diagnostics, Diagnostic{
				Span:     inc.Span,
				Message:  fmt.Sprintf("cannot resolve relative include %q: no base directory configured (use WithBaseDir)", inc.Path),
				Severity: Error,
			})
			return
		}
		incPath = filepath.Join(baseDir, incPath)
	}
	incAbs, err := filepath.Abs(incPath)
	if err != nil {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Span:     inc.Span,
			Message:  fmt.Sprintf("resolving include path %q: %v", inc.Path, err),
			Severity: Error,
		})
		return
	}
	ld.loadFile(incAbs, incAbs, filepath.Dir(incAbs))
}
