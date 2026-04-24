package ast

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// LoadFile reads and parses the beancount file at the given path,
// recursively resolving include directives. It returns a Ledger whose
// directives are ordered canonically by (date, kind, filename, offset)
// and accessible via Ledger.All.
//
// Include paths are resolved relative to the directory of the file
// containing the include directive.
//
// A non-nil error is returned only when filename cannot be made
// absolute, which in practice only happens if the process working
// directory is unavailable. I/O failures while reading the root file
// or any included file surface as error-severity entries on
// Ledger.Diagnostics so the caller can still inspect any directives
// loaded before the failure.
func LoadFile(filename string) (*Ledger, error) {
	absPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("resolving path %q: %w", filename, err)
	}

	l := &loader{visited: make(map[string]bool)}
	l.loadFile(absPath)
	return l.finish(), nil
}

// Load parses src as beancount source. Includes inside src are resolved
// relative to the working directory unless [WithFilename] supplies a
// synthetic source path, in which case its directory is the base.
//
// Load performs no top-level I/O, so it has no error return; parse and
// include errors surface as Diagnostics on the returned Ledger so
// partial results remain inspectable.
func Load(src string, opts ...LoadOption) *Ledger {
	o := resolveLoadOptions(opts)
	cst := syntax.Parse(src)

	absPath := syntheticAbsPath(o.filename)
	l := &loader{visited: make(map[string]bool)}
	l.loadFromCST(o.filename, absPath, cst)
	return l.finish()
}

// LoadReader reads the entire contents of r and parses it as beancount
// source via Load. Read errors are wrapped and returned; parse and include
// errors surface as Diagnostics on the returned Ledger.
func LoadReader(r io.Reader, opts ...LoadOption) (*Ledger, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading source: %w", err)
	}
	return Load(string(data), opts...), nil
}

// syntheticAbsPath returns an absolute path used for cycle detection and as
// the include base when loading inline source. An empty result means no
// named anchor is available — Load will then leave includeBaseDir to fall
// back to os.Getwd(), and only fail if that also fails.
func syntheticAbsPath(filename string) string {
	if filename == "" {
		return ""
	}
	abs, err := filepath.Abs(filename)
	if err != nil {
		// filepath.Abs only fails when the OS refuses to provide the
		// working directory. The empty return funnels into the same
		// no-anchor path as an empty filename: includeBaseDir then
		// retries os.Getwd, and a relative include surfaces an error
		// diagnostic if even that fails.
		return ""
	}
	return abs
}

// loader carries the in-progress state of a single Load/LoadReader/LoadFile
// invocation: the set of files visited so far (for circular-include
// detection), the lowered files in load order, the merged directive slice
// to be sorted into the final Ledger, and accumulated diagnostics.
type loader struct {
	visited     map[string]bool // absolute paths already loaded (cycle detection)
	files       []*File
	directives  []Directive
	diagnostics []Diagnostic
}

// finish materializes the in-progress loader state as a Ledger, sorting
// the accumulated directives into canonical order in a single pass.
func (ld *loader) finish() *Ledger {
	ledger := &Ledger{
		Files:       ld.files,
		Diagnostics: ld.diagnostics,
	}
	ledger.InsertAll(ld.directives)
	return ledger
}

// loadFile reads, parses, and lowers the file at absPath, then recursively
// processes its include directives. Cycle detection runs before the file
// is opened so revisits short-circuit without spurious read-error
// diagnostics.
func (ld *loader) loadFile(absPath string) {
	if ld.markVisited(absPath) {
		return
	}
	cst, err := syntax.ParseFile(absPath)
	if err != nil {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Message:  fmt.Sprintf("reading file %q: %v", absPath, err),
			Severity: Error,
		})
		return
	}
	ld.lowerAndProcess(absPath, absPath, cst)
}

// loadFromCST is the inline-source entry point: it stamps absPath in the
// visited set (when non-empty) and lowers cst.
func (ld *loader) loadFromCST(filename, absPath string, cst *syntax.File) {
	if absPath != "" && ld.markVisited(absPath) {
		return
	}
	ld.lowerAndProcess(filename, absPath, cst)
}

// markVisited records absPath as loaded. It returns true and emits a
// circular-include diagnostic when absPath was already in the visited set;
// callers must skip lowering in that case.
func (ld *loader) markVisited(absPath string) bool {
	if ld.visited[absPath] {
		ld.diagnostics = append(ld.diagnostics, Diagnostic{
			Message:  fmt.Sprintf("circular include detected: %q", absPath),
			Severity: Error,
		})
		return true
	}
	ld.visited[absPath] = true
	return false
}

// lowerAndProcess lowers cst into an AST File, appends it to the loader,
// and recursively resolves its include directives. filename is passed to
// Lower, which records it on the resulting File and on the Span positions
// it derives (verbatim, possibly empty for inline source). absPath is the
// absolute path used as the include base; it may be empty when loading
// inline source without WithFilename and Getwd fails.
func (ld *loader) lowerAndProcess(filename, absPath string, cst *syntax.File) {
	file := Lower(filename, cst)
	ld.files = append(ld.files, file)
	ld.diagnostics = append(ld.diagnostics, file.Diagnostics...)

	baseDir := includeBaseDir(absPath)
	for _, d := range file.Directives {
		inc, ok := d.(*Include)
		if !ok {
			ld.directives = append(ld.directives, d)
			continue
		}
		ld.resolveInclude(inc, baseDir)
	}
}

// resolveInclude resolves inc's path against baseDir and recurses into the
// referenced file. A relative inc.Path with no base directory (inline
// source loaded without WithFilename and with Getwd unavailable) is
// reported as an error diagnostic and not followed.
func (ld *loader) resolveInclude(inc *Include, baseDir string) {
	incPath := inc.Path
	if !filepath.IsAbs(incPath) {
		if baseDir == "" {
			ld.diagnostics = append(ld.diagnostics, Diagnostic{
				Span:     inc.Span,
				Message:  fmt.Sprintf("cannot resolve relative include %q: no base directory", inc.Path),
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
	ld.loadFile(incAbs)
}

// includeBaseDir returns the directory anchoring relative include paths in
// a file with the given absolute path. When absPath is empty (inline source
// with no WithFilename), it falls back to the working directory; an empty
// result means no anchor is available.
func includeBaseDir(absPath string) string {
	if absPath != "" {
		return filepath.Dir(absPath)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
