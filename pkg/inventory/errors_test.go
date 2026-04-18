package inventory_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/validation"
)

func TestErrorError(t *testing.T) {
	span := ast.Span{Start: ast.Position{Filename: "f.bc", Line: 12, Column: 3}}
	acct := ast.Account("Assets:Cash")

	tests := []struct {
		name string
		err  inventory.Error
		want string
	}{
		{
			name: "span and account",
			err:  inventory.Error{Span: span, Account: acct, Message: "boom"},
			want: "f.bc:12:3: Assets:Cash: boom",
		},
		{
			name: "span only",
			err:  inventory.Error{Span: span, Message: "boom"},
			want: "f.bc:12:3: boom",
		},
		{
			name: "account only",
			err:  inventory.Error{Account: acct, Message: "boom"},
			want: "Assets:Cash: boom",
		},
		{
			name: "neither",
			err:  inventory.Error{Message: "boom"},
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
		err      inventory.Error
		wantCode validation.Code
		wantMsg  string
	}{
		{
			name: "invalid booking method maps directly",
			err: inventory.Error{
				Code:    inventory.CodeInvalidBookingMethod,
				Span:    span,
				Message: "unknown booking keyword",
			},
			wantCode: validation.CodeInvalidBookingMethod,
			wantMsg:  "unknown booking keyword",
		},
		{
			name: "multiple auto postings maps directly",
			err: inventory.Error{
				Code:    inventory.CodeMultipleAutoPostings,
				Span:    span,
				Message: "too many auto postings",
			},
			wantCode: validation.CodeMultipleAutoPostings,
			wantMsg:  "too many auto postings",
		},
		{
			name: "no matching lot falls back to internal error",
			err: inventory.Error{
				Code:    inventory.CodeNoMatchingLot,
				Span:    span,
				Message: "no lot",
			},
			wantCode: validation.CodeInternalError,
			wantMsg:  "no lot",
		},
		{
			name: "reduction exceeds inventory falls back to internal error",
			err: inventory.Error{
				Code:    inventory.CodeReductionExceedsInventory,
				Span:    span,
				Message: "too much",
			},
			wantCode: validation.CodeInternalError,
			wantMsg:  "too much",
		},
		{
			name: "account prefix is folded into message",
			err: inventory.Error{
				Code:    inventory.CodeNoMatchingLot,
				Span:    span,
				Account: acct,
				Message: "no lot",
			},
			wantCode: validation.CodeInternalError,
			wantMsg:  "Assets:Cash: no lot",
		},
		{
			name: "account prefix folded in even for mapped codes",
			err: inventory.Error{
				Code:    inventory.CodeInvalidBookingMethod,
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
			got := validation.FromInventoryError(tc.err)
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
