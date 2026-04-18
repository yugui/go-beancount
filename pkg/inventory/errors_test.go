package inventory

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
)

func TestErrorError(t *testing.T) {
	span := ast.Span{Start: ast.Position{Filename: "f.bc", Line: 12, Column: 3}}
	acct := ast.Account("Assets:Cash")

	tests := []struct {
		name string
		err  Error
		want string
	}{
		{
			name: "span and account",
			err:  Error{Span: span, Account: acct, Message: "boom"},
			want: "f.bc:12:3: Assets:Cash: boom",
		},
		{
			name: "span only",
			err:  Error{Span: span, Message: "boom"},
			want: "f.bc:12:3: boom",
		},
		{
			name: "account only",
			err:  Error{Account: acct, Message: "boom"},
			want: "Assets:Cash: boom",
		},
		{
			name: "neither",
			err:  Error{Message: "boom"},
			want: "boom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorAsValidationError(t *testing.T) {
	span := ast.Span{Start: ast.Position{Filename: "f.bc", Line: 12, Column: 3}}
	acct := ast.Account("Assets:Cash")

	tests := []struct {
		name     string
		err      Error
		wantCode validation.Code
		wantMsg  string
	}{
		{
			name: "invalid booking method maps directly",
			err: Error{
				Code:    CodeInvalidBookingMethod,
				Span:    span,
				Message: "unknown booking keyword",
			},
			wantCode: validation.CodeInvalidBookingMethod,
			wantMsg:  "unknown booking keyword",
		},
		{
			name: "multiple auto postings maps directly",
			err: Error{
				Code:    CodeMultipleAutoPostings,
				Span:    span,
				Message: "too many auto postings",
			},
			wantCode: validation.CodeMultipleAutoPostings,
			wantMsg:  "too many auto postings",
		},
		{
			name: "no matching lot falls back to internal error",
			err: Error{
				Code:    CodeNoMatchingLot,
				Span:    span,
				Message: "no lot",
			},
			wantCode: validation.CodeInternalError,
			wantMsg:  "no lot",
		},
		{
			name: "reduction exceeds inventory falls back to internal error",
			err: Error{
				Code:    CodeReductionExceedsInventory,
				Span:    span,
				Message: "too much",
			},
			wantCode: validation.CodeInternalError,
			wantMsg:  "too much",
		},
		{
			name: "account prefix is folded into message",
			err: Error{
				Code:    CodeNoMatchingLot,
				Span:    span,
				Account: acct,
				Message: "no lot",
			},
			wantCode: validation.CodeInternalError,
			wantMsg:  "Assets:Cash: no lot",
		},
		{
			name: "account prefix folded in even for mapped codes",
			err: Error{
				Code:    CodeInvalidBookingMethod,
				Span:    span,
				Account: acct,
				Message: "unknown booking keyword",
			},
			wantCode: validation.CodeInvalidBookingMethod,
			wantMsg:  "Assets:Cash: unknown booking keyword",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.AsValidationError()
			if got.Code != tc.wantCode {
				t.Errorf("Code = %v, want %v", got.Code, tc.wantCode)
			}
			if got.Span != tc.err.Span {
				t.Errorf("Span = %+v, want %+v", got.Span, tc.err.Span)
			}
			if got.Message != tc.wantMsg {
				t.Errorf("Message = %q, want %q", got.Message, tc.wantMsg)
			}
		})
	}
}
