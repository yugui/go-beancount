package inventory_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
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
