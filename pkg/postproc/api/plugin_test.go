package api

import (
	"context"
	"errors"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestPluginFunc_Apply(t *testing.T) {
	wantErr := errors.New("boom")
	wantResult := Result{Errors: []Error{{Code: "x", Message: "y"}}}

	var gotCtx context.Context
	var gotIn Input
	f := PluginFunc(func(ctx context.Context, in Input) (Result, error) {
		gotCtx = ctx
		gotIn = in
		return wantResult, wantErr
	})

	ctx := context.Background()
	in := Input{Config: "cfg"}
	got, err := f.Apply(ctx, in)
	if err != wantErr {
		t.Errorf("Apply err = %v, want %v", err, wantErr)
	}
	if len(got.Errors) != 1 || got.Errors[0].Code != "x" {
		t.Errorf("Apply result = %+v, want %+v", got, wantResult)
	}
	if gotCtx != ctx {
		t.Error("Apply did not pass ctx through")
	}
	if gotIn.Config != "cfg" {
		t.Errorf("Apply Input.Config = %q, want %q", gotIn.Config, "cfg")
	}
}

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
