package main

import (
	"bytes"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestReportExitCodes(t *testing.T) {
	warning := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 1, Column: 1}},
		Message:  "stylistic nitpick",
		Severity: ast.Warning,
	}
	errorDiag := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 2, Column: 1}},
		Message:  "broken directive",
		Severity: ast.Error,
	}
	pluginDiag := ast.Diagnostic{
		Code:     "plugin-not-registered",
		Message:  `plugin "missing" is not registered`,
		Severity: ast.Error,
	}

	tests := []struct {
		name   string
		diags  []ast.Diagnostic
		strict bool
		want   int
	}{
		{
			name: "no diagnostics is clean",
			want: 0,
		},
		{
			name:   "warnings without strict are clean",
			diags:  []ast.Diagnostic{warning},
			strict: false,
			want:   0,
		},
		{
			name:   "warnings with strict fail",
			diags:  []ast.Diagnostic{warning},
			strict: true,
			want:   1,
		},
		{
			name:  "errors always fail",
			diags: []ast.Diagnostic{errorDiag},
			want:  1,
		},
		{
			name:  "plugin diagnostics fail",
			diags: []ast.Diagnostic{pluginDiag},
			want:  1,
		},
		{
			name:   "mix of warning and plugin diagnostic still fails without strict",
			diags:  []ast.Diagnostic{warning, pluginDiag},
			strict: false,
			want:   1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := report(&buf, tc.diags, tc.strict)
			if got != tc.want {
				t.Errorf("report(strict=%v) = %d, want %d (stderr: %q)", tc.strict, got, tc.want, buf.String())
			}
		})
	}
}

func TestFormatDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		in   ast.Diagnostic
		want string
	}{
		{
			name: "error with location",
			in: ast.Diagnostic{
				Span:     ast.Span{Start: ast.Position{Filename: "main.beancount", Line: 10, Column: 3}},
				Message:  "unknown account",
				Severity: ast.Error,
			},
			want: "main.beancount:10:3: error: unknown account",
		},
		{
			name: "warning with location",
			in: ast.Diagnostic{
				Span:     ast.Span{Start: ast.Position{Filename: "x.beancount", Line: 1, Column: 1}},
				Message:  "deprecated syntax",
				Severity: ast.Warning,
			},
			want: "x.beancount:1:1: warning: deprecated syntax",
		},
		{
			name: "error without filename",
			in: ast.Diagnostic{
				Message:  "synthetic problem",
				Severity: ast.Error,
			},
			want: "error: synthetic problem",
		},
		{
			name: "code is appended in brackets",
			in: ast.Diagnostic{
				Code:     "balance-mismatch",
				Span:     ast.Span{Start: ast.Position{Filename: "m.beancount", Line: 5, Column: 2}},
				Message:  "amount differs",
				Severity: ast.Error,
			},
			want: "m.beancount:5:2: error: amount differs [balance-mismatch]",
		},
		{
			name: "no location with code",
			in: ast.Diagnostic{
				Code:     "plugin-not-registered",
				Message:  "boom",
				Severity: ast.Error,
			},
			want: "error: boom [plugin-not-registered]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDiagnostic(tc.in)
			if got != tc.want {
				t.Errorf("formatDiagnostic(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReportWritesAllDiagnostics(t *testing.T) {
	// Diagnostics are pre-sorted by (Filename, Line, Column): the
	// empty-filename diagnostic sorts first, then the two f.beancount
	// entries in line order. This test exercises the "every diagnostic
	// reaches the writer" contract; the dedicated sort tests below cover
	// reordering behavior.
	diags := []ast.Diagnostic{
		{
			Code:     "foo",
			Message:  "third problem",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "f.beancount", Line: 1, Column: 1}},
			Message:  "first problem",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "f.beancount", Line: 2, Column: 1}},
			Message:  "second problem",
			Severity: ast.Warning,
		},
	}
	var buf bytes.Buffer
	report(&buf, diags, false)
	want := formatDiagnostic(diags[0]) + "\n" +
		formatDiagnostic(diags[1]) + "\n" +
		formatDiagnostic(diags[2]) + "\n"
	if got := buf.String(); got != want {
		t.Errorf("report() wrote %q, want %q", got, want)
	}
}

func TestReportSortsDiagnosticsByPosition(t *testing.T) {
	// Mix files and lines in non-sorted order so the sort has to do real
	// work. Expected output orders by (Filename, Line, Column).
	diags := []ast.Diagnostic{
		{
			Span:     ast.Span{Start: ast.Position{Filename: "b.beancount", Line: 3, Column: 1}},
			Message:  "b3",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 10, Column: 5}},
			Message:  "a10c5",
			Severity: ast.Warning,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 2, Column: 1}},
			Message:  "a2",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 10, Column: 1}},
			Message:  "a10c1",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "b.beancount", Line: 1, Column: 4}},
			Message:  "b1",
			Severity: ast.Error,
		},
	}
	var buf bytes.Buffer
	got := report(&buf, diags, false)
	if got != 1 {
		t.Errorf("report() exit = %d, want 1 (errors present)", got)
	}
	want := "a.beancount:2:1: error: a2\n" +
		"a.beancount:10:1: error: a10c1\n" +
		"a.beancount:10:5: warning: a10c5\n" +
		"b.beancount:1:4: error: b1\n" +
		"b.beancount:3:1: error: b3\n"
	if out := buf.String(); out != want {
		t.Errorf("report() wrote\n%q\nwant\n%q", out, want)
	}
}

func TestReportDoesNotMutateCallerSlice(t *testing.T) {
	diags := []ast.Diagnostic{
		{
			Span:     ast.Span{Start: ast.Position{Filename: "b.beancount", Line: 1, Column: 1}},
			Message:  "later",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 1, Column: 1}},
			Message:  "earlier",
			Severity: ast.Error,
		},
	}
	var buf bytes.Buffer
	report(&buf, diags, false)
	if diags[0].Message != "later" || diags[1].Message != "earlier" {
		t.Errorf("report() mutated caller slice: %+v", diags)
	}
}

func TestReportStableSortPreservesAppendOrder(t *testing.T) {
	// Two diagnostics share an identical position; stable sort must
	// preserve their input order so plugin output stays deterministic.
	diags := []ast.Diagnostic{
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 5, Column: 2}},
			Message:  "first at L5",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 5, Column: 2}},
			Message:  "second at L5",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 5, Column: 2}},
			Message:  "third at L5",
			Severity: ast.Error,
		},
	}
	var buf bytes.Buffer
	report(&buf, diags, false)
	want := "a.beancount:5:2: error: first at L5\n" +
		"a.beancount:5:2: error: second at L5\n" +
		"a.beancount:5:2: error: third at L5\n"
	if got := buf.String(); got != want {
		t.Errorf("report() wrote\n%q\nwant\n%q", got, want)
	}
}

func TestReportExitCodeAfterSort(t *testing.T) {
	// Ensure exit-code computation iterates the sorted copy, not the
	// original. An error-severity diagnostic anywhere in the input must
	// produce exit 1 regardless of where it sorts.
	warn := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 1, Column: 1}},
		Message:  "warn",
		Severity: ast.Warning,
	}
	errAtEnd := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "z.beancount", Line: 99, Column: 1}},
		Message:  "boom",
		Severity: ast.Error,
	}
	var buf bytes.Buffer
	if got := report(&buf, []ast.Diagnostic{warn, errAtEnd}, false); got != 1 {
		t.Errorf("report(error+warning) exit = %d, want 1", got)
	}
	buf.Reset()
	if got := report(&buf, []ast.Diagnostic{warn}, true); got != 1 {
		t.Errorf("report(warning, strict=true) exit = %d, want 1", got)
	}
	buf.Reset()
	if got := report(&buf, []ast.Diagnostic{warn}, false); got != 0 {
		t.Errorf("report(warning, strict=false) exit = %d, want 0", got)
	}
}

func TestReportSortsEmptyFilenameFirst(t *testing.T) {
	// Diagnostics with an empty Filename (e.g. plugin-level errors that
	// have no source position) must sort before any file-bound diagnostic
	// because the empty string is lexicographically smallest. The
	// formatter omits the path/line/col prefix for those, so the line
	// starts with "<severity>: <message>".
	diags := []ast.Diagnostic{
		{
			Span:     ast.Span{Start: ast.Position{Filename: "b.beancount", Line: 1, Column: 1}},
			Message:  "in b",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 1, Column: 1}},
			Message:  "in a",
			Severity: ast.Error,
		},
		{
			Code:     "plugin-not-registered",
			Message:  "no source",
			Severity: ast.Error,
		},
	}
	var buf bytes.Buffer
	report(&buf, diags, false)
	want := "error: no source [plugin-not-registered]\n" +
		"a.beancount:1:1: error: in a\n" +
		"b.beancount:1:1: error: in b\n"
	if got := buf.String(); got != want {
		t.Errorf("report() wrote\n%q\nwant\n%q", got, want)
	}
}

func TestReportEmptyInput(t *testing.T) {
	// report must accept both nil and an empty slice and produce no
	// output. This pins the no-diagnostics fast path so a future
	// "skip sort when len <= 1" optimization cannot regress to printing
	// stray bytes.
	cases := []struct {
		name  string
		diags []ast.Diagnostic
	}{
		{name: "nil slice", diags: nil},
		{name: "empty slice", diags: []ast.Diagnostic{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := report(&buf, tc.diags, false)
			if got != 0 {
				t.Errorf("report() exit = %d, want 0", got)
			}
			if out := buf.String(); out != "" {
				t.Errorf("report() wrote %q, want empty", out)
			}
		})
	}
}

func TestReportSingleElement(t *testing.T) {
	// A single diagnostic must still be printed and produce the right
	// exit code; this guards the trivial-input path against any future
	// "skip when len <= 1" shortcut that forgets to emit output.
	diag := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "only.beancount", Line: 7, Column: 4}},
		Message:  "lonely",
		Severity: ast.Error,
	}
	var buf bytes.Buffer
	got := report(&buf, []ast.Diagnostic{diag}, false)
	if got != 1 {
		t.Errorf("report() exit = %d, want 1", got)
	}
	want := "only.beancount:7:4: error: lonely\n"
	if out := buf.String(); out != want {
		t.Errorf("report() wrote %q, want %q", out, want)
	}
}

func TestReportOffsetTiebreaker(t *testing.T) {
	// When (Filename, Line, Column) collide, Offset is the final
	// tiebreaker. Pin that behavior so the comparator's last branch is
	// covered and a future refactor cannot silently drop it.
	diags := []ast.Diagnostic{
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 1, Column: 1, Offset: 42}},
			Message:  "later offset",
			Severity: ast.Error,
		},
		{
			Span:     ast.Span{Start: ast.Position{Filename: "a.beancount", Line: 1, Column: 1, Offset: 7}},
			Message:  "earlier offset",
			Severity: ast.Error,
		},
	}
	var buf bytes.Buffer
	report(&buf, diags, false)
	want := "a.beancount:1:1: error: earlier offset\n" +
		"a.beancount:1:1: error: later offset\n"
	if got := buf.String(); got != want {
		t.Errorf("report() wrote\n%q\nwant\n%q", got, want)
	}
}
