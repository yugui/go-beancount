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
	diags := []ast.Diagnostic{
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
		{
			Code:     "foo",
			Message:  "third problem",
			Severity: ast.Error,
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
