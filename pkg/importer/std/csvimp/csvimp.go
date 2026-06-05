package csvimp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yugui/go-beancount/pkg/importer"
)

// Importer extracts beancount Transactions from one CSV/TSV shape.
// It is produced by the package's [importer.Factory] (registered under
// kind "csv"); its internal state is frozen at construction and all
// methods are safe for concurrent invocation.
type Importer struct {
	name string
	s    *shape
}

// Name returns the instance name supplied to the factory at construction time.
func (i *Importer) Name() string { return i.name }

// Identify reports whether the importer can extract directives from in.
// It applies an extension/MIME gate and then checks whether the single
// configured shape's match regex (if any) and required columns are
// satisfied by in's header. Identify never mutates the Importer.
func (i *Importer) Identify(ctx context.Context, in importer.Input) bool {
	if !extensionOrMIMEMatch(in) {
		return false
	}
	if i.s.compiledMatch != nil && !i.s.compiledMatch.MatchString(in.Path) {
		return false
	}
	// Headerless input has no header to inspect; gate on path/MIME/match only.
	if i.s.columns != nil {
		return true
	}
	hdr, ok := readHeader(in, i.s)
	if !ok {
		return false
	}
	idx := buildColumnIndex(hdr)
	return columnsPresent(i.s, idx)
}

// Extract reads the CSV/TSV body of in and returns one Transaction per
// data row. Per-row parse errors are returned as Diagnostics; structural
// failures (opener error, header mismatch) return a non-nil error.
func (i *Importer) Extract(ctx context.Context, in importer.Input) (importer.Output, error) {
	if i.s.compiledMatch != nil && !i.s.compiledMatch.MatchString(in.Path) {
		return importer.Output{}, fmt.Errorf("csvimp: no shape matched for %q", in.Path)
	}
	return extractRows(ctx, in, i.name, i.s)
}

func extensionOrMIMEMatch(in importer.Input) bool {
	ext := strings.ToLower(filepath.Ext(in.Path))
	if ext == ".csv" || ext == ".tsv" {
		return true
	}
	switch in.MIME {
	case "text/csv", "text/tab-separated-values":
		return true
	}
	return false
}

func readHeader(in importer.Input, s *shape) ([]string, bool) {
	rc, err := in.Opener()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	hdr, _, err := s.reader().Records(rc)
	if err != nil {
		return nil, false
	}
	return hdr, true
}

func buildColumnIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		trimmed := strings.TrimSpace(h)
		if _, exists := idx[trimmed]; !exists {
			idx[trimmed] = i
		}
	}
	return idx
}

func columnsPresent(s *shape, idx map[string]int) bool {
	for _, name := range requiredColumns(s) {
		if _, ok := idx[name]; !ok {
			return false
		}
	}
	return true
}

func requiredColumns(s *shape) []string {
	n := 1 + len(s.amounts) + len(s.narrationCols) + len(s.payeeCols) +
		len(s.accountCols) + len(s.counterAccountCols)
	if s.currencyCol != "" {
		n++
	}
	out := make([]string, 0, n)
	out = append(out, s.dateCol)
	for _, a := range s.amounts {
		out = append(out, a.Col)
	}
	out = append(out, s.narrationCols...)
	out = append(out, s.payeeCols...)
	if s.currencyCol != "" {
		out = append(out, s.currencyCol)
	}
	out = append(out, s.accountCols...)
	out = append(out, s.counterAccountCols...)
	if s.split == nil {
		return out
	}
	// Synthetic split columns are produced per row, not present in the
	// header; only the split source column is required.
	out = append(out, s.split.col)
	// filter in-place: groups[c] is true only for synthetic names, never
	// the real columns appended above, so writes stay within out's bounds.
	filtered := out[:0]
	for _, c := range out {
		if !s.split.groups[c] {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
