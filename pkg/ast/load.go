package ast

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// Load reads and parses the beancount file at the given path, recursively
// resolving include directives. It returns a Ledger whose directives are
// ordered canonically by (date, kind, filename, offset) and accessible via
// Ledger.All.
//
// Include paths are resolved relative to the directory of the file
// containing the include directive.
func Load(filename string) (*Ledger, error) {
	absPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("resolving path %q: %w", filename, err)
	}

	l := &loader{
		visited: make(map[string]bool),
	}
	l.loadFile(absPath)

	ledger := &Ledger{
		Files:       l.files,
		Diagnostics: l.diagnostics,
	}
	// Bulk-insert all collected directives in a single stable sort.
	ledger.InsertAll(l.directives)
	return ledger, nil
}

type loader struct {
	visited     map[string]bool // absolute paths already loaded (cycle detection)
	files       []*File
	directives  []Directive
	diagnostics []Diagnostic
}

func (ld *loader) loadFile(absPath string) {
	// Cycle detection
	if ld.visited[absPath] {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Message:  fmt.Sprintf("circular include detected: %s", absPath),
			Severity: Error,
		})
		return
	}
	ld.visited[absPath] = true

	// Read and parse the file
	data, err := os.ReadFile(absPath)
	if err != nil {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Message:  fmt.Sprintf("reading file %s: %v", absPath, err),
			Severity: Error,
		})
		return
	}

	cst := syntax.Parse(string(data))
	file := Lower(absPath, cst)
	ld.files = append(ld.files, file)

	// Merge diagnostics from lowering.
	ld.diagnostics = append(ld.diagnostics, file.Diagnostics...)

	// Process directives: merge non-Include directives, recurse on Includes.
	dir := filepath.Dir(absPath)
	for _, d := range file.Directives {
		inc, ok := d.(*Include)
		if !ok {
			ld.directives = append(ld.directives, d)
			continue
		}
		// Resolve include path relative to the current file's directory.
		incPath := inc.Path
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(dir, incPath)
		}
		incAbs, err := filepath.Abs(incPath)
		if err != nil {
			ld.diagnostics = append(ld.diagnostics, Diagnostic{
				Span:     inc.Span,
				Message:  fmt.Sprintf("resolving include path %q: %v", inc.Path, err),
				Severity: Error,
			})
			continue
		}
		ld.loadFile(incAbs)
	}
}
