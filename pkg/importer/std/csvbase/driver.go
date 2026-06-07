package csvbase

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// Gate reports whether an Input is a candidate for this importer. It must be
// side-effect-free and must not consume in.Opener; true = candidate, false = not.
type Gate func(in importer.Input) bool

// DefaultGate accepts an Input whose path ends in .csv or .tsv
// (case-insensitive) or whose MIME is text/csv or text/tab-separated-values.
func DefaultGate(in importer.Input) bool {
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

// PathMatch returns a Gate that accepts an Input whose Path matches re.
func PathMatch(re *regexp.Regexp) Gate {
	return func(in importer.Input) bool {
		return re.MatchString(in.Path)
	}
}

// AllGates returns a Gate that accepts only when every g accepts. With no
// arguments it accepts everything.
func AllGates(gs ...Gate) Gate {
	return func(in importer.Input) bool {
		for _, g := range gs {
			if !g(in) {
				return false
			}
		}
		return true
	}
}

// FinalizeFunc post-processes the whole extracted output before Extract
// returns. A non-nil error causes Extract to return that error (system-level failure).
type FinalizeFunc func(ctx context.Context, dirs []ast.Directive, diags []ast.Diagnostic) ([]ast.Directive, []ast.Diagnostic, error)

// RowContext is the per-row input handed to a RowMapper.
type RowContext struct {
	Path   string            // Input.Path (display name; may be "")
	Line   int               // 1-based source line of the record
	Fields []string          // raw cells of the record
	Index  map[string]int    // column name -> 0-based position
	Hints  map[string]string // Input.Hints (may be nil)
}

// RowMapper turns one parsed record into zero or more directives plus
// per-record diagnostics. Implementations must be safe for concurrent use.
type RowMapper interface {
	// Required reports the header columns that must be present for this
	// mapper to operate; the Driver presence-checks them in Identify and
	// Extract.
	Required() []string
	// Map maps one record. The (directives, diagnostics, error) triple
	// encodes every per-row disposition:
	//   ([d], nil, nil)         emit d
	//   (nil, nil, nil)         skip the row silently
	//   (nil, []{errDiag}, nil) drop the row with a diagnostic
	//   ([d], []{warnDiag}, nil) emit d and also report a warning
	//   (_, _, err)             fatal: Extract aborts
	// A row may yield any number and any kind of directive.
	Map(ctx context.Context, rec RowContext) ([]ast.Directive, []ast.Diagnostic, error)
}

type mapperFunc struct {
	required []string
	f        func(ctx context.Context, rec RowContext) ([]ast.Directive, []ast.Diagnostic, error)
}

func (m *mapperFunc) Required() []string { return m.required }
func (m *mapperFunc) Map(ctx context.Context, rec RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
	return m.f(ctx, rec)
}

// MapperFunc adapts a function plus an explicit required-column list to
// RowMapper. The required slice is stored without copying; callers must not
// modify it after the call.
func MapperFunc(required []string, f func(ctx context.Context, rec RowContext) ([]ast.Directive, []ast.Diagnostic, error)) RowMapper {
	return &mapperFunc{required: required, f: f}
}

// Config configures a Driver. Reader and Mapper are required.
type Config struct {
	Reader   csvkit.Reader      // parsing config (delimiter/encoding/skip/header)
	Gate     Gate               // Identify gate; nil selects DefaultGate
	Mapper   RowMapper          // per-row mapping; must be non-nil
	Filters  []csvkit.RowFilter // rows any filter drops are skipped silently
	RowHash  *RowHash           // nil disables idempotency stamping
	Finalize FinalizeFunc       // nil disables post-processing
}

// Driver is a reusable CSV/TSV importer skeleton. It implements
// importer.Importer. Its state is frozen at construction; Identify and
// Extract are safe for concurrent use.
type Driver struct {
	name string
	cfg  Config
}

// New validates cfg and returns a Driver bound to name. It returns an error
// when name is empty, Mapper is nil, or Reader.Columns and Reader.HeaderMatch
// are both set (mutually exclusive). Errors are prefixed "csvbase: ".
func New(name string, cfg Config) (*Driver, error) {
	if name == "" {
		return nil, fmt.Errorf("csvbase: name must not be empty")
	}
	if cfg.Mapper == nil {
		return nil, fmt.Errorf("csvbase: Mapper must not be nil")
	}
	if cfg.Reader.Columns != nil && cfg.Reader.HeaderMatch != nil {
		return nil, fmt.Errorf("csvbase: Reader.Columns and Reader.HeaderMatch are mutually exclusive")
	}
	return &Driver{name: name, cfg: cfg}, nil
}

// Name returns the instance name supplied to New.
func (d *Driver) Name() string { return d.name }

// Identify reports whether the Driver can extract directives from in. It
// applies the gate and, for header-bearing inputs, lazily reads the header
// to check required columns. It never mutates the Driver and reports no
// error (failure => false).
func (d *Driver) Identify(ctx context.Context, in importer.Input) bool {
	gate := d.cfg.Gate
	if gate == nil {
		gate = DefaultGate
	}
	if !gate(in) {
		return false
	}
	// headerless: no header to inspect
	if d.cfg.Reader.Columns != nil {
		return true
	}
	hdr, ok := d.readHeader(in)
	if !ok {
		return false
	}
	idx := buildColumnIndex(hdr)
	for _, col := range d.cfg.Mapper.Required() {
		if _, ok := idx[col]; !ok {
			return false
		}
	}
	return true
}

// Extract reads the CSV/TSV body of in and returns directives in
// source-encounter order plus per-record diagnostics. A non-nil error is
// reserved for system-level failures (I/O, ctx cancellation, format
// corruption); ledger-content problems are Diagnostics. Context
// cancellation surfaces as a non-nil error.
func (d *Driver) Extract(ctx context.Context, in importer.Input) (importer.Output, error) {
	rc, err := in.Opener()
	if err != nil {
		return importer.Output{}, fmt.Errorf("csvbase: opening %q: %w", in.Path, err)
	}
	defer rc.Close()

	hdr, rows, err := d.cfg.Reader.Records(rc)
	if err != nil {
		return importer.Output{}, fmt.Errorf("csvbase: reading header from %q: %w", in.Path, err)
	}

	idx := d.cfg.Reader.Columns
	if idx == nil {
		idx = buildColumnIndex(hdr)
	}

	var diags []ast.Diagnostic
	for _, col := range d.cfg.Mapper.Required() {
		if _, ok := idx[col]; !ok {
			diags = append(diags, ErrorDiag(DiagMissingColumn, in.Path, 0,
				fmt.Sprintf("required column %q not present in header (driver %q)", col, d.name)))
		}
	}
	if len(diags) > 0 {
		return importer.Output{Diagnostics: diags}, nil
	}

	var directives []ast.Directive
	for rec, rerr := range rows {
		if err := ctx.Err(); err != nil {
			return importer.Output{Directives: directives, Diagnostics: diags}, err
		}
		if rerr != nil {
			return importer.Output{Directives: directives, Diagnostics: diags},
				fmt.Errorf("csvbase: parsing row in %q: %w", in.Path, rerr)
		}
		if len(rec.Fields) == 0 || allBlank(rec.Fields) {
			continue
		}
		if d.skipFiltered(rec.Fields, idx) {
			continue
		}
		rctx := RowContext{
			Path:   in.Path,
			Line:   rec.Line,
			Fields: rec.Fields,
			Index:  idx,
			Hints:  in.Hints,
		}
		rowDirs, rowDiags, err := d.cfg.Mapper.Map(ctx, rctx)
		if err != nil {
			return importer.Output{Directives: directives, Diagnostics: diags},
				fmt.Errorf("csvbase: mapping row in %q: %w", in.Path, err)
		}
		diags = append(diags, rowDiags...)
		if d.cfg.RowHash != nil {
			key := d.cfg.RowHash.Key
			if key == "" {
				key = DefaultRowHashKey
			}
			hash := computeHash(d.name, rec.Fields)
			for _, dir := range rowDirs {
				directives = append(directives, importerutil.StampMetadata(dir, key, hash))
			}
		} else {
			directives = append(directives, rowDirs...)
		}
	}

	if d.cfg.Finalize != nil {
		dirs, dgs, err := d.cfg.Finalize(ctx, directives, diags)
		if err != nil {
			return importer.Output{Directives: directives, Diagnostics: diags},
				fmt.Errorf("csvbase: finalize %q: %w", in.Path, err)
		}
		directives, diags = dirs, dgs
	}

	return importer.Output{Directives: directives, Diagnostics: diags}, nil
}

func (d *Driver) readHeader(in importer.Input) ([]string, bool) {
	rc, err := in.Opener()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	hdr, _, err := d.cfg.Reader.Records(rc)
	if err != nil {
		return nil, false
	}
	return hdr, true
}

func (d *Driver) skipFiltered(fields []string, idx map[string]int) bool {
	if len(d.cfg.Filters) == 0 {
		return false
	}
	get := func(col string) string { return fieldAt(fields, idx, col) }
	for _, f := range d.cfg.Filters {
		if f.Skip(fields, get) {
			return true
		}
	}
	return false
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

func allBlank(row []string) bool {
	for _, f := range row {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// fieldAt returns row[idx[col]] or "" when col is unknown or the row is
// shorter than the header.
func fieldAt(row []string, idx map[string]int, col string) string {
	i, ok := idx[col]
	if !ok || i >= len(row) {
		return ""
	}
	return row[i]
}
