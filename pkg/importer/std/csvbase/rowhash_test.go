package csvbase_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func TestRowHash_StampsDefaultKey(t *testing.T) {
	d, err := csvbase.New("rh-test", csvbase.Config{
		Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
		RowHash: &csvbase.RowHash{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	note := out.Directives[0].(*ast.Note)
	v, ok := note.Meta.Props[csvbase.DefaultRowHashKey]
	if !ok {
		t.Fatalf("directive missing key %q", csvbase.DefaultRowHashKey)
	}
	if v.Kind != ast.MetaString {
		t.Errorf("MetaValue.Kind = %v, want MetaString", v.Kind)
	}
	if len(v.String) != 16 {
		t.Errorf("hash length = %d, want 16", len(v.String))
	}
}

func TestRowHash_StampsCustomKey(t *testing.T) {
	d, err := csvbase.New("rh-test", csvbase.Config{
		Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
		RowHash: &csvbase.RowHash{KeyFunc: csvbase.StaticRowHashKey("my-hash")},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	note := out.Directives[0].(*ast.Note)
	if _, ok := note.Meta.Props["my-hash"]; !ok {
		t.Errorf("directive missing custom key %q", "my-hash")
	}
	if _, bad := note.Meta.Props[csvbase.DefaultRowHashKey]; bad {
		t.Errorf("directive has default key when custom key is configured")
	}
}

func TestRowHash_NilDisablesStamping(t *testing.T) {
	d, err := csvbase.New("rh-test", csvbase.Config{
		Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
		RowHash: nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	note := out.Directives[0].(*ast.Note)
	if _, ok := note.Meta.Props[csvbase.DefaultRowHashKey]; ok {
		t.Errorf("directive has rowhash key when RowHash is nil")
	}
}

func TestRowHash_StableAcrossExtracts(t *testing.T) {
	d, err := csvbase.New("rh-test", csvbase.Config{
		Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
		RowHash: &csvbase.RowHash{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := inputStr("/f.csv", "A\nval\n")
	out1, err := d.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract (1): %v", err)
	}
	out2, err := d.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract (2): %v", err)
	}
	h1 := out1.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	h2 := out2.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	if h1 != h2 {
		t.Errorf("hash unstable: %q != %q", h1, h2)
	}
}

func TestRowHash_DiffersAcrossDriverNames(t *testing.T) {
	newDriver := func(name string) *csvbase.Driver {
		d, err := csvbase.New(name, csvbase.Config{
			Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
			RowHash: &csvbase.RowHash{},
		})
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		return d
	}
	d1 := newDriver("name-one")
	d2 := newDriver("name-two")
	in := inputStr("/f.csv", "A\nval\n")
	out1, err := d1.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract (d1): %v", err)
	}
	out2, err := d2.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract (d2): %v", err)
	}
	h1 := out1.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	h2 := out2.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	if h1 == h2 {
		t.Errorf("hashes should differ across driver names, both = %q", h1)
	}
}

func TestRowHash_RawFieldsNotMapperTransformed(t *testing.T) {
	// The hash is over raw fields; transforming the field value in the mapper
	// must not change the hash.
	makeMapper := func(transform func(string) string) csvbase.RowMapper {
		return csvbase.MapperFunc([]string{"A"}, func(ctx context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			v := transform(rec.Fields[rec.Index["A"]])
			return []ast.Directive{&ast.Note{Comment: v}}, nil, nil
		})
	}
	cfg := func(m csvbase.RowMapper) *csvbase.Driver {
		d, err := csvbase.New("rh-test", csvbase.Config{
			Mapper:  m,
			RowHash: &csvbase.RowHash{},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	}
	in := inputStr("/f.csv", "A\nval\n")
	outA, err := cfg(makeMapper(func(s string) string { return s })).Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract (identity): %v", err)
	}
	outB, err := cfg(makeMapper(func(s string) string { return strings.ToUpper(s) })).Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract (upper): %v", err)
	}
	hA := outA.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	hB := outB.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	if hA != hB {
		t.Errorf("hashes differ (%q vs %q): hash must be computed over raw fields", hA, hB)
	}
}

func TestRowHash_FieldBoundarySeparatorPreventsCollision(t *testing.T) {
	d, err := csvbase.New("rh-test", csvbase.Config{
		Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
		RowHash: &csvbase.RowHash{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// ["ab","cd"] and ["a","bcd"] have the same field concatenation but differ
	// at the field boundary; the unit separator must keep their hashes distinct.
	out1, err := d.Extract(context.Background(), inputStr("/f.csv", "A,B\nab,cd\n"))
	if err != nil {
		t.Fatalf("Extract (ab,cd): %v", err)
	}
	out2, err := d.Extract(context.Background(), inputStr("/f.csv", "A,B\na,bcd\n"))
	if err != nil {
		t.Fatalf("Extract (a,bcd): %v", err)
	}
	h1 := out1.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	h2 := out2.Directives[0].(*ast.Note).Meta.Props[csvbase.DefaultRowHashKey].String
	if h1 == h2 {
		t.Errorf("hashes should differ at the field boundary, both = %q", h1)
	}
}

func TestRowHash_KeyFuncResolvesPerRow(t *testing.T) {
	// KeyFunc picks the metadata key from the row's first cell, so different
	// rows are stamped under different keys.
	d, err := csvbase.New("rh-test", csvbase.Config{
		Mapper: csvbase.MapperFunc([]string{"Kind"}, emitNote),
		RowHash: &csvbase.RowHash{KeyFunc: func(rec csvbase.RowContext) string {
			return rec.Fields[rec.Index["Kind"]] + "-rowhash"
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "Kind\nbank\ncard\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("Extract: got %d directives, want 2", len(out.Directives))
	}
	want := []struct {
		i   int
		key string
	}{{0, "bank-rowhash"}, {1, "card-rowhash"}}
	for _, w := range want {
		note, ok := out.Directives[w.i].(*ast.Note)
		if !ok {
			t.Fatalf("Extract: directive %d is %T, want *ast.Note", w.i, out.Directives[w.i])
		}
		if _, ok := note.Meta.Props[w.key]; !ok {
			t.Errorf("Extract: row %d missing key %q; got %v", w.i, w.key, note.Meta.Props)
		}
	}
}

func TestRowHashValue_MatchesStampedHash(t *testing.T) {
	// RowHashValue exposed as a pipeline Key must equal the value RowHash stamps
	// for the same instance name and raw row.
	in := inputStr("/f.csv", "A\nval\n")

	stamped, err := func() (string, error) {
		d, err := csvbase.New("rh-test", csvbase.Config{
			Mapper:  csvbase.MapperFunc([]string{"A"}, emitNote),
			RowHash: &csvbase.RowHash{KeyFunc: csvbase.StaticRowHashKey("k")},
		})
		if err != nil {
			return "", err
		}
		out, err := d.Extract(context.Background(), in)
		if err != nil {
			return "", err
		}
		note, ok := out.Directives[0].(*ast.Note)
		if !ok {
			return "", fmt.Errorf("directive 0 is %T, want *ast.Note", out.Directives[0])
		}
		return note.Meta.Props["k"].String, nil
	}()
	if err != nil {
		t.Fatalf("stamped extract: %v", err)
	}

	b := csvbase.NewBuilder()
	hk := csvbase.RowHashValue(b, "rh-test")
	pipeline := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		v, _ := csvbase.Value(c, hk)
		return []ast.Directive{&ast.Note{Comment: v}}, nil, nil
	})
	d, err := csvbase.New("rh-test", csvbase.Config{Mapper: pipeline})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("value extract: %v", err)
	}
	note, ok := out.Directives[0].(*ast.Note)
	if !ok {
		t.Fatalf("value extract: directive 0 is %T, want *ast.Note", out.Directives[0])
	}
	if got := note.Comment; got != stamped {
		t.Errorf("RowHashValue = %q, stamped = %q; must match", got, stamped)
	}
}

// emitNote is a minimal mapper that emits one *ast.Note per data row.
func emitNote(_ context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
	return []ast.Directive{&ast.Note{Comment: rec.Fields[0]}}, nil, nil
}

// inputStr constructs an importer.Input backed by an in-memory string.
func inputStr(path, body string) importer.Input {
	return importer.Input{
		Path: path,
		Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		},
	}
}

// inputStrMIME is like inputStr but also sets the MIME field.
func inputStrMIME(path, mime, body string) importer.Input {
	in := inputStr(path, body)
	in.MIME = mime
	return in
}

// inputErrOpener returns an Input whose Opener always fails.
func inputErrOpener(path string) importer.Input {
	return importer.Input{
		Path: path,
		Opener: func() (io.ReadCloser, error) {
			return nil, io.ErrUnexpectedEOF
		},
	}
}

// minimalDriver returns a Driver with the given gate (nil => DefaultGate)
// and no required columns.
func minimalDriver(t *testing.T, name string, gate csvbase.Gate) *csvbase.Driver {
	t.Helper()
	d, err := csvbase.New(name, csvbase.Config{
		Gate:   gate,
		Mapper: csvbase.MapperFunc(nil, emitNote),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// requiredDriver returns a Driver that requires column "Col".
func requiredDriver(t *testing.T) *csvbase.Driver {
	t.Helper()
	d, err := csvbase.New("req-test", csvbase.Config{
		Mapper: csvbase.MapperFunc([]string{"Col"}, emitNote),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// headerlessDriver returns a Driver configured with explicit Columns (headerless).
func headerlessDriver(t *testing.T) *csvbase.Driver {
	t.Helper()
	d, err := csvbase.New("headerless", csvbase.Config{
		Reader: csvkit.Reader{
			Columns: map[string]int{"A": 0},
		},
		Mapper: csvbase.MapperFunc([]string{"A"}, emitNote),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}
