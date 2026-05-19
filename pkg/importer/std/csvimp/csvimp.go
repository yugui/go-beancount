package csvimp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yugui/go-beancount/pkg/importer"
)

// Importer is the CSV/TSV reference importer. It is a process-global
// singleton — the package init function registers a single value
// under both "csv" and the package's Go import path, and looking up
// either name returns the same pointer.
//
// Individual method calls (Configure, Identify, Extract) are
// goroutine-safe via an internal mutex. However, an Identify→Extract
// sequence on the same Input is not atomic: a concurrent Configure or
// Identify call with a different Input may invalidate the cached shape
// selection between the two calls. Callers that rely on the cache (i.e.,
// do not call Identify before every Extract) must serialise externally.
type Importer struct {
	mu     sync.Mutex
	shapes []*shape
	cache  identifyCache
}

type identifyCache struct {
	inputKey string
	shape    *shape
}

// Name returns "csv".
func (i *Importer) Name() string { return "csv" }

// Identify reports whether the importer can extract directives from
// in. It applies an extension/MIME gate, then walks the configured
// shapes in lexicographic name order and returns true on the first
// shape whose match regex (if any) and required columns are satisfied
// by in's header. The selected shape is cached for a subsequent
// Extract on the same Input.
func (i *Importer) Identify(ctx context.Context, in importer.Input) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.shapes) == 0 {
		return false
	}
	if !extensionOrMIMEMatch(in) {
		return false
	}
	s, _, ok := i.selectShape(in)
	if !ok {
		return false
	}
	i.cache = identifyCache{
		inputKey: cacheKey(in),
		shape:    s,
	}
	return true
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

func cacheKey(in importer.Input) string {
	return in.Path + "\x1f" + in.MIME
}

// selectShape returns the first shape (in lex order) whose match regex and
// required columns are satisfied by in's header, along with its column index.
func (i *Importer) selectShape(in importer.Input) (*shape, map[string]int, bool) {
	if in.Opener == nil {
		return nil, nil, false
	}
	for _, s := range i.shapes {
		if s.compiledMatch != nil && !s.compiledMatch.MatchString(in.Path) {
			continue
		}
		hdr, ok := readHeader(in, s)
		if !ok {
			continue
		}
		idx := buildColumnIndex(hdr)
		if !columnsPresent(s, idx) {
			continue
		}
		return s, idx, true
	}
	return nil, nil, false
}

func readHeader(in importer.Input, s *shape) ([]string, bool) {
	rc, err := in.Opener()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	_, hdr, err := openCSVAtBody(rc, s)
	if err != nil {
		return nil, false
	}
	return hdr, true
}

func skipRawLines(br *bufio.Reader, skipLines int) error {
	for n := 0; n < skipLines; n++ {
		if _, err := readLine(br); err != nil {
			return err
		}
	}
	return nil
}

// readLine reads one line up to (and including) '\n', strips the
// trailing CR/LF, and returns the line body. A trailing partial line
// without a final newline is returned as success; only an EOF with no
// data returns io.EOF.
func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
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
	out := make([]string, 0, 4+len(s.amounts)+len(s.narrationCols))
	out = append(out, s.dateCol)
	for _, a := range s.amounts {
		out = append(out, a.Col)
	}
	out = append(out, s.narrationCols...)
	if s.payeeCol != "" {
		out = append(out, s.payeeCol)
	}
	if s.currencyCol != "" {
		out = append(out, s.currencyCol)
	}
	return out
}

// Extract reads the CSV/TSV and emits one single-leg Transaction per
// non-skipped row, stamped with csvimp-rowhash metadata. Per-row
// failures (bad date, unparseable amount, missing currency, missing
// account) produce a single diagnostic and skip the row. A structural
// failure (no matching shape, Opener error, header parse failure)
// returns a framework error.
func (i *Importer) Extract(ctx context.Context, in importer.Input) (importer.Output, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	s, err := i.resolveShape(in)
	if err != nil {
		return importer.Output{}, err
	}
	return extractRows(ctx, in, s)
}

func (i *Importer) resolveShape(in importer.Input) (*shape, error) {
	if len(i.shapes) == 0 {
		return nil, fmt.Errorf("csvimp: not configured")
	}
	if i.cache.shape != nil && i.cache.inputKey == cacheKey(in) {
		return i.cache.shape, nil
	}
	s, _, ok := i.selectShape(in)
	if !ok {
		return nil, fmt.Errorf("csvimp: no shape matched for %q", in.Path)
	}
	i.cache = identifyCache{
		inputKey: cacheKey(in),
		shape:    s,
	}
	return s, nil
}
