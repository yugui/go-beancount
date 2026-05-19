package importer

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Dispatch walks reg.Names() in the registry's declared order and
// returns the first Importer whose Identify returns true. Between
// calls it checks ctx.Err(); on cancellation it returns
// (nil, false, nil) and the caller converts ctx.Err() into an error.
//
// When no instance matches, Dispatch returns (nil, false, diags) where
// diags carries a single Error diagnostic with Code [DiagImporterNone]
// and Span.Start.Filename = in.Path.
func Dispatch(ctx context.Context, reg Registry, in Input) (Importer, bool, []ast.Diagnostic) {
	if ctx.Err() != nil {
		return nil, false, nil
	}
	for _, name := range reg.Names() {
		if ctx.Err() != nil {
			return nil, false, nil
		}
		imp, ok := reg.Lookup(name)
		if !ok {
			continue
		}
		if imp.Identify(ctx, in) {
			return imp, true, nil
		}
	}
	return nil, false, []ast.Diagnostic{{
		Code:     DiagImporterNone,
		Span:     ast.Span{Start: ast.Position{Filename: in.Path}},
		Message:  fmt.Sprintf("no importer identified %q", in.Path),
		Severity: ast.Error,
	}}
}

// Apply dispatches in against reg and runs Extract on the chosen
// instance. Diagnostics from Dispatch and Extract are concatenated in
// that order; if both sides produce none, Output.Diagnostics is nil.
// When no instance matches, Apply returns an Output whose Diagnostics
// contains the [DiagImporterNone] diagnostic and a nil error — the
// absence of a matching importer is a ledger-content problem, not a
// framework error. On Extract error, Directives is nil regardless of
// what Extract returned; Diagnostics reflects any partial output.
// On ctx cancellation Apply returns (Output{}, ctx.Err()).
//
// Apply always uses the buffered Extract path in ABI v1, even when the
// importer satisfies [Streaming].
func Apply(ctx context.Context, reg Registry, in Input) (Output, error) {
	imp, ok, dispatchDiags := Dispatch(ctx, reg, in)
	if ctx.Err() != nil {
		return Output{}, ctx.Err()
	}
	if !ok {
		return Output{Diagnostics: dispatchDiags}, nil
	}

	out, err := imp.Extract(ctx, in)
	composed := make([]ast.Diagnostic, 0, len(dispatchDiags)+len(out.Diagnostics))
	composed = append(composed, dispatchDiags...)
	composed = append(composed, out.Diagnostics...)
	if len(composed) == 0 {
		composed = nil
	}
	if err != nil {
		return Output{Diagnostics: composed}, err
	}
	return Output{Directives: out.Directives, Diagnostics: composed}, nil
}
