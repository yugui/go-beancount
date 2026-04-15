package api

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestErrorFormat(t *testing.T) {
	tests := []struct {
		name string
		err  Error
		want string
	}{
		{
			name: "with location",
			err: Error{
				Code: "test-error",
				Span: ast.Span{
					Start: ast.Position{
						Filename: "main.beancount",
						Line:     42,
						Column:   5,
					},
				},
				Message: "something went wrong",
			},
			want: "main.beancount:42:5: something went wrong",
		},
		{
			name: "without location",
			err: Error{
				Code:    "test-error",
				Message: "no source location",
			},
			want: "no source location",
		},
		{
			name: "empty filename with line",
			err: Error{
				Code: "test-error",
				Span: ast.Span{
					Start: ast.Position{
						Line:   10,
						Column: 3,
					},
				},
				Message: "line but no file",
			},
			want: "line but no file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("Error.Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
