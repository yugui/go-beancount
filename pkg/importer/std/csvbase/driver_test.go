package csvbase_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// ---------------------------------------------------------------------------
// Gate tests
// ---------------------------------------------------------------------------

func TestDefaultGate(t *testing.T) {
	cases := []struct {
		name string
		in   importer.Input
		want bool
	}{
		{".csv extension", inputStr("/f.csv", ""), true},
		{".CSV uppercase", inputStr("/f.CSV", ""), true},
		{".tsv extension", inputStr("/f.tsv", ""), true},
		{".TSV uppercase", inputStr("/f.TSV", ""), true},
		{".txt extension", inputStr("/f.txt", ""), false},
		{"MIME text/csv", inputStrMIME("/f.dat", "text/csv", ""), true},
		{"MIME text/tab-separated-values", inputStrMIME("/f.dat", "text/tab-separated-values", ""), true},
		{"empty path and MIME", inputStr("", ""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := csvbase.DefaultGate(tc.in); got != tc.want {
				t.Errorf("DefaultGate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPathMatch(t *testing.T) {
	re := regexp.MustCompile(`^mybank`)
	gate := csvbase.PathMatch(re)
	if got := gate(inputStr("mybank-2024.csv", "")); !got {
		t.Errorf("PathMatch(%q) = %v, want true", "mybank-2024.csv", got)
	}
	if got := gate(inputStr("other.csv", "")); got {
		t.Errorf("PathMatch(%q) = %v, want false", "other.csv", got)
	}
}

func TestAllGates_NoArgs(t *testing.T) {
	gate := csvbase.AllGates()
	if got := gate(inputStr("", "")); !got {
		t.Errorf("AllGates()(%q) = %v, want true", "", got)
	}
}

func TestAllGates_And(t *testing.T) {
	re := regexp.MustCompile(`mybank`)
	gate := csvbase.AllGates(csvbase.DefaultGate, csvbase.PathMatch(re))

	if got := gate(inputStr("mybank.csv", "")); !got {
		t.Errorf("AllGates(DefaultGate, PathMatch)(%q) = %v, want true", "mybank.csv", got)
	}
	if got := gate(inputStr("mybank.txt", "")); got {
		t.Errorf("AllGates(DefaultGate, PathMatch)(%q) = %v, want false", "mybank.txt", got)
	}
	if got := gate(inputStr("other.csv", "")); got {
		t.Errorf("AllGates(DefaultGate, PathMatch)(%q) = %v, want false", "other.csv", got)
	}
}

// ---------------------------------------------------------------------------
// New validation tests
// ---------------------------------------------------------------------------

func TestNew_EmptyName(t *testing.T) {
	_, err := csvbase.New("", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, emitNote),
	})
	if err == nil {
		t.Fatal("New with empty name: want error, got nil")
	}
}

func TestNew_NilMapper(t *testing.T) {
	_, err := csvbase.New("test", csvbase.Config{})
	if err == nil {
		t.Fatal("New with nil Mapper: want error, got nil")
	}
}

func TestNew_ColumnsAndHeaderMatchBothSet(t *testing.T) {
	_, err := csvbase.New("test", csvbase.Config{
		Reader: csvkit.Reader{
			Columns:     map[string]int{"A": 0},
			HeaderMatch: func([]string) bool { return true },
		},
		Mapper: csvbase.MapperFunc(nil, emitNote),
	})
	if err == nil {
		t.Fatal("New with both Columns and HeaderMatch: want error, got nil")
	}
}

func TestNew_ValidConfig(t *testing.T) {
	d, err := csvbase.New("valid", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, emitNote),
	})
	if err != nil {
		t.Fatalf("New valid config: %v", err)
	}
	if d.Name() != "valid" {
		t.Errorf("Name() = %q, want %q", d.Name(), "valid")
	}
}

func TestNew_NilGateAccepted(t *testing.T) {
	// nil Gate is valid; DefaultGate is used at Identify time.
	d, err := csvbase.New("ng", csvbase.Config{
		Gate:   nil,
		Mapper: csvbase.MapperFunc(nil, emitNote),
	})
	if err != nil {
		t.Fatalf("New with nil Gate: %v", err)
	}
	// nil Gate => DefaultGate: .csv path should identify.
	if !d.Identify(context.Background(), inputStr("/x.csv", "A\n")) {
		t.Error("Identify with nil Gate and .csv path: want true")
	}
}

// ---------------------------------------------------------------------------
// Identify tests
// ---------------------------------------------------------------------------

func TestIdentify_GateRejects(t *testing.T) {
	neverGate := csvbase.Gate(func(importer.Input) bool { return false })
	d := minimalDriver(t, "g", neverGate)
	if got := d.Identify(context.Background(), inputStr("/f.csv", "")); got {
		t.Errorf("Identify(gate=never, %q) = %v, want false", "/f.csv", got)
	}
}

func TestIdentify_HeaderlessDoesNotOpenFile(t *testing.T) {
	d := headerlessDriver(t)
	// Opener panics or errors; Identify must not call it.
	in := importer.Input{
		Path: "data.csv",
		Opener: func() (io.ReadCloser, error) {
			return nil, fmt.Errorf("must not be called")
		},
	}
	// Gate defaults to DefaultGate; .csv passes. Headerless => true without opening.
	if got := d.Identify(context.Background(), in); !got {
		t.Errorf("Identify(headerless, %q) = %v, want true", "data.csv", got)
	}
}

func TestIdentify_RequiredColumnsPresent(t *testing.T) {
	d := requiredDriver(t)
	if got := d.Identify(context.Background(), inputStr("/f.csv", "Col\nval\n")); !got {
		t.Errorf("Identify(required=Col, header=Col) = %v, want true", got)
	}
}

func TestIdentify_RequiredColumnMissing(t *testing.T) {
	d := requiredDriver(t)
	if got := d.Identify(context.Background(), inputStr("/f.csv", "Other\nval\n")); got {
		t.Errorf("Identify(required=Col, header=Other) = %v, want false", got)
	}
}

func TestIdentify_OpenerError(t *testing.T) {
	d := requiredDriver(t)
	if got := d.Identify(context.Background(), inputErrOpener("/f.csv")); got {
		t.Errorf("Identify(opener=error) = %v, want false", got)
	}
}

func TestIdentify_MalformedHeader(t *testing.T) {
	d := requiredDriver(t)
	// Empty body: Records returns EOF error for the header read.
	if got := d.Identify(context.Background(), inputStr("/f.csv", "")); got {
		t.Errorf("Identify(empty body) = %v, want false", got)
	}
}

func TestIdentify_HeaderMatchBanner(t *testing.T) {
	// HeaderMatch scans past a banner to locate the header.
	d, err := csvbase.New("banner", csvbase.Config{
		Reader: csvkit.Reader{
			HeaderMatch: func(row []string) bool { return len(row) > 0 && row[0] == "Col" },
		},
		Mapper: csvbase.MapperFunc([]string{"Col"}, emitNote),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "Preamble\nCol\nval\n"
	if !d.Identify(context.Background(), inputStr("/f.csv", body)) {
		t.Error("Identify with HeaderMatch banner: want true when required column found past banner")
	}
}

// ---------------------------------------------------------------------------
// Extract happy-path tests
// ---------------------------------------------------------------------------

func TestExtract_HappyPath(t *testing.T) {
	d, err := csvbase.New("hp", csvbase.Config{
		Mapper: csvbase.MapperFunc([]string{"A"}, emitNote),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nfoo\nbar\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	// Source order preserved.
	if out.Directives[0].(*ast.Note).Comment != "foo" {
		t.Errorf("directive 0 comment = %q, want %q", out.Directives[0].(*ast.Note).Comment, "foo")
	}
	if out.Directives[1].(*ast.Note).Comment != "bar" {
		t.Errorf("directive 1 comment = %q, want %q", out.Directives[1].(*ast.Note).Comment, "bar")
	}
}

// ---------------------------------------------------------------------------
// Extract missing-column tests
// ---------------------------------------------------------------------------

func TestExtract_MissingColumn(t *testing.T) {
	d := requiredDriver(t)
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "Other\nval\n"))
	if err != nil {
		t.Fatalf("Extract: unexpected error %v", err)
	}
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) == 0 {
		t.Fatal("want DiagMissingColumn diagnostic, got none")
	}
	if out.Diagnostics[0].Code != csvbase.DiagMissingColumn {
		t.Errorf("diag code = %q, want %q", out.Diagnostics[0].Code, csvbase.DiagMissingColumn)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("diag severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

func TestExtract_MissingColumn_NoRowsMapped(t *testing.T) {
	var mapped int
	d, err := csvbase.New("mc", csvbase.Config{
		Mapper: csvbase.MapperFunc([]string{"Col"}, func(_ context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			mapped++
			return []ast.Directive{&ast.Note{Comment: rec.Fields[0]}}, nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := d.Extract(context.Background(), inputStr("/f.csv", "Other\nval\n")); err != nil {
		t.Fatalf("Extract: unexpected error %v", err)
	}
	if mapped != 0 {
		t.Errorf("mapper called %d times; must not be called when column is missing", mapped)
	}
}

// ---------------------------------------------------------------------------
// Extract blank-row skipping tests
// ---------------------------------------------------------------------------

func TestExtract_BlankRowSkipped(t *testing.T) {
	d := minimalDriver(t, "blank", nil)
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n\n   \n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Errorf("got %d directives, want 1 (blank rows skipped)", len(out.Directives))
	}
}

// ---------------------------------------------------------------------------
// Extract filter tests
// ---------------------------------------------------------------------------

func TestExtract_FilterDropsRowSilently(t *testing.T) {
	re := regexp.MustCompile(`^TOTAL`)
	d, err := csvbase.New("flt", csvbase.Config{
		Mapper:  csvbase.MapperFunc(nil, emitNote),
		Filters: []csvkit.RowFilter{csvkit.ExcludeAnyField(re)},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\nTOTAL\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Errorf("got %d directives, want 1 (TOTAL row filtered)", len(out.Directives))
	}
}

func TestExtract_FilterByColumn(t *testing.T) {
	re := regexp.MustCompile(`^Total$`)
	d, err := csvbase.New("fcol", csvbase.Config{
		Mapper:  csvbase.MapperFunc([]string{"Type"}, emitNote),
		Filters: []csvkit.RowFilter{csvkit.ExcludeMatching("Type", re)},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "Type,Amount\nDebit,5\nTotal,100\nCredit,3\n"
	out, err := d.Extract(context.Background(), inputStr("/f.csv", body))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 2 {
		t.Errorf("got %d directives, want 2 (Total row filtered)", len(out.Directives))
	}
}

// ---------------------------------------------------------------------------
// Extract per-row disposition tests
// ---------------------------------------------------------------------------

func TestExtract_RowDisposition_DropWithDiag(t *testing.T) {
	d, err := csvbase.New("disp", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, func(_ context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			dg := csvbase.ErrorDiag("test-drop", rec.Path, rec.Line, "dropped")
			return nil, []ast.Diagnostic{dg}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != "test-drop" {
		t.Errorf("diagnostics = %+v, want one test-drop diagnostic", out.Diagnostics)
	}
}

func TestExtract_RowDisposition_WarnKeep(t *testing.T) {
	d, err := csvbase.New("disp", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, func(_ context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			dg := csvbase.WarnDiag("test-warn", rec.Path, rec.Line, "warning")
			return []ast.Directive{&ast.Note{Comment: "kept"}}, []ast.Diagnostic{dg}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Errorf("got %d directives, want 1", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Severity != ast.Warning {
		t.Errorf("diagnostics = %+v, want one warning", out.Diagnostics)
	}
}

func TestExtract_RowDisposition_SilentSkip(t *testing.T) {
	d, err := csvbase.New("disp", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, func(_ context.Context, _ csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			return nil, nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nval\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 0 || len(out.Diagnostics) != 0 {
		t.Errorf("got directives=%d diagnostics=%d; want 0 each", len(out.Directives), len(out.Diagnostics))
	}
}

// ---------------------------------------------------------------------------
// Extract error-path tests
// ---------------------------------------------------------------------------

func TestExtract_OpenerError(t *testing.T) {
	d := requiredDriver(t)
	_, err := d.Extract(context.Background(), inputErrOpener("/f.csv"))
	if err == nil {
		t.Fatal("Extract with Opener error: want non-nil error")
	}
}

func TestExtract_IteratorParseError(t *testing.T) {
	// A bare double-quote in a cell triggers a CSV parse error mid-stream.
	d := minimalDriver(t, "pe", nil)
	// First row is valid (emits a directive), second row contains an unquoted ".
	body := "A\ngood\nbad\"cell\n"
	out, err := d.Extract(context.Background(), inputStr("/f.csv", body))
	if err == nil {
		t.Fatal("Extract with parse error: want non-nil error")
	}
	// Partial output: the first good row was emitted.
	if len(out.Directives) != 1 {
		t.Errorf("got %d partial directives, want 1", len(out.Directives))
	}
}

func TestExtract_MapperError(t *testing.T) {
	boom := errors.New("mapper boom")
	// Emit a directive on the first row, then fail on the second.
	var call int
	d, err := csvbase.New("me", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, func(_ context.Context, _ csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			call++
			if call == 1 {
				return []ast.Directive{&ast.Note{Comment: "first"}}, nil, nil
			}
			return nil, nil, boom
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nrow1\nrow2\n"))
	if err == nil {
		t.Fatal("Extract with Mapper error: want non-nil error")
	}
	// Partial output: first row was emitted before the failure.
	if len(out.Directives) != 1 {
		t.Errorf("got %d partial directives, want 1", len(out.Directives))
	}
}

func TestExtract_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// One row emitted, then cancel before the second row is processed.
	var call int
	d, err := csvbase.New("cc", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, func(c context.Context, rec csvbase.RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
			call++
			if call == 1 {
				cancel()
				return []ast.Directive{&ast.Note{Comment: rec.Fields[0]}}, nil, nil
			}
			return []ast.Directive{&ast.Note{Comment: rec.Fields[0]}}, nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(ctx, inputStr("/f.csv", "A\nfirst\nsecond\n"))
	if err == nil {
		t.Fatal("Extract after cancel: want non-nil error")
	}
	// At least one directive was emitted before cancellation.
	if len(out.Directives) == 0 {
		t.Error("want partial directives after cancellation, got none")
	}
}

// ---------------------------------------------------------------------------
// Extract Finalize tests
// ---------------------------------------------------------------------------

func TestExtract_FinalizeReceivesAndCanAppend(t *testing.T) {
	extra := &ast.Note{Comment: "finalized"}
	extraDiag := csvbase.WarnDiag("fin-warn", "", 0, "finalize warning")
	d, err := csvbase.New("fin", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, emitNote),
		Finalize: func(_ context.Context, dirs []ast.Directive, diags []ast.Diagnostic) ([]ast.Directive, []ast.Diagnostic, error) {
			return append(dirs, extra), append(diags, extraDiag), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := d.Extract(context.Background(), inputStr("/f.csv", "A\nrow\n"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2 (1 row + 1 finalize)", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != "fin-warn" {
		t.Errorf("diagnostics = %+v, want one fin-warn", out.Diagnostics)
	}
}

func TestExtract_FinalizeError(t *testing.T) {
	d, err := csvbase.New("fe", csvbase.Config{
		Mapper: csvbase.MapperFunc(nil, emitNote),
		Finalize: func(_ context.Context, dirs []ast.Directive, diags []ast.Diagnostic) ([]ast.Directive, []ast.Diagnostic, error) {
			return nil, nil, errors.New("finalize failed")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Extract(context.Background(), inputStr("/f.csv", "A\nrow\n"))
	if err == nil {
		t.Fatal("Extract with Finalize error: want non-nil error")
	}
}

// ---------------------------------------------------------------------------
// Concurrency test
// ---------------------------------------------------------------------------

func TestConcurrentIdentifyExtract(t *testing.T) {
	d, err := csvbase.New("concurrent", csvbase.Config{
		Mapper: csvbase.MapperFunc([]string{"A"}, emitNote),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "A\nfoo\nbar\n"

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			in := inputStr("/concurrent.csv", body)
			if !d.Identify(context.Background(), in) {
				t.Errorf("Identify returned false")
				return
			}
			out, err := d.Extract(context.Background(), in)
			if err != nil {
				t.Errorf("Extract: %v", err)
				return
			}
			if len(out.Directives) != 2 {
				t.Errorf("got %d directives, want 2", len(out.Directives))
			}
		}()
	}
	wg.Wait()
}
