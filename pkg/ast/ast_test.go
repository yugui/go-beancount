package ast

import (
	"errors"
	"testing"
)

// TestSeverityZeroValueIsError pins the invariant that a freshly
// constructed Diagnostic literal omitting Severity defaults to Error.
// Every Diagnostic emitter in the codebase relies on this; if a future
// edit ever makes Error not the iota-0 constant, this test fails loudly
// instead of silently flipping every diagnostic's severity.
func TestSeverityZeroValueIsError(t *testing.T) {
	var s Severity
	if s != Error {
		t.Errorf("Severity zero value = %d, want %d (Error)", s, Error)
	}
	if got := (Diagnostic{}).Severity; got != Error {
		t.Errorf("Diagnostic{}.Severity = %d, want %d (Error)", got, Error)
	}
}

func TestDiagnosticError(t *testing.T) {
	tests := []struct {
		name string
		in   Diagnostic
		want string
	}{
		{
			name: "error with location",
			in: Diagnostic{
				Span:     Span{Start: Position{Filename: "main.beancount", Line: 10, Column: 3}},
				Message:  "unknown account",
				Severity: Error,
			},
			want: "main.beancount:10:3: error: unknown account",
		},
		{
			name: "warning with location",
			in: Diagnostic{
				Span:     Span{Start: Position{Filename: "x.beancount", Line: 1, Column: 1}},
				Message:  "deprecated syntax",
				Severity: Warning,
			},
			want: "x.beancount:1:1: warning: deprecated syntax",
		},
		{
			name: "error without filename",
			in: Diagnostic{
				Message:  "synthetic problem",
				Severity: Error,
			},
			want: "error: synthetic problem",
		},
		{
			name: "code is appended in brackets",
			in: Diagnostic{
				Code:     "balance-mismatch",
				Span:     Span{Start: Position{Filename: "m.beancount", Line: 5, Column: 2}},
				Message:  "amount differs",
				Severity: Error,
			},
			want: "m.beancount:5:2: error: amount differs [balance-mismatch]",
		},
		{
			name: "no location with code",
			in: Diagnostic{
				Code:     "plugin-not-registered",
				Message:  "boom",
				Severity: Error,
			},
			want: "error: boom [plugin-not-registered]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Error(); got != tc.want {
				t.Errorf("Diagnostic.Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDiagnosticIs(t *testing.T) {
	balanceMismatch := Diagnostic{Code: "balance-mismatch"}
	noOpen := Diagnostic{Code: "account-not-open"}

	cases := []struct {
		name   string
		err    Diagnostic
		target error
		want   bool
	}{
		{
			name:   "same code matches regardless of message and span",
			err:    Diagnostic{Code: "balance-mismatch", Message: "x", Span: Span{Start: Position{Line: 1}}},
			target: balanceMismatch,
			want:   true,
		},
		{
			name:   "different code does not match",
			err:    Diagnostic{Code: "balance-mismatch"},
			target: noOpen,
			want:   false,
		},
		{
			// errors.Is short-circuits when err == target, so two
			// distinct empty-code diagnostics with different messages
			// exercise Is() itself rather than the equality fast path.
			name:   "empty code never matches by code alone",
			err:    Diagnostic{Message: "lhs"},
			target: Diagnostic{Message: "rhs"},
			want:   false,
		},
		{
			name:   "non-Diagnostic target does not match",
			err:    balanceMismatch,
			target: errors.New("balance-mismatch"),
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errors.Is(tc.err, tc.target); got != tc.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", tc.err, tc.target, got, tc.want)
			}
		})
	}
}

func TestDiagnosticErrorsAs(t *testing.T) {
	orig := Diagnostic{
		Code:    "balance-mismatch",
		Span:    Span{Start: Position{Filename: "f.beancount", Line: 3, Column: 1}},
		Message: "amount differs",
	}
	var wrapped error = orig
	var got Diagnostic
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As failed to extract Diagnostic from %T", wrapped)
	}
	if got != orig {
		t.Errorf("extracted Diagnostic = %+v, want %+v", got, orig)
	}
}
