package api

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

func TestPluginFunc_Apply(t *testing.T) {
	wantErr := errors.New("boom")
	wantResult := Result{Diagnostics: []ast.Diagnostic{{Code: "x", Message: "y"}}}

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
	if diff := cmp.Diff(wantResult, got); diff != "" {
		t.Errorf("Apply() mismatch (-want +got):\n%s", diff)
	}
	if gotCtx != ctx {
		t.Errorf("Apply() gotCtx = %v, want %v", gotCtx, ctx)
	}
	if gotIn.Config != "cfg" {
		t.Errorf("Apply Input.Config = %q, want %q", gotIn.Config, "cfg")
	}
}
