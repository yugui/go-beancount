package importer

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Dispatch walks reg.Names() in sorted order and calls Identify on each
// registered importer. It returns the first importer whose Identify returns
// true. Between calls it checks for ctx cancellation; on cancellation it
// returns (nil, false, nil) and the caller is responsible for converting
// ctx.Err() into an error.
//
// When no importer matches, Dispatch returns (nil, false, diags) where diags
// contains a single Error diagnostic with Code [DiagImporterNone] and
// Span.Start.Filename set to in.Path.
func Dispatch(ctx context.Context, reg Registry, in Input) (Importer, bool, []ast.Diagnostic) {
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

// Apply returns the directives and diagnostics produced by the first registered
// importer that identifies in. When no importer matches, it returns an Output
// whose Diagnostics contains a single DiagImporterNone error diagnostic and a
// nil error — the absence of a matching importer is a ledger-content problem,
// not a framework error. On a successful match, Output.Diagnostics is the
// concatenation of dispatch diagnostics followed by Extract diagnostics; it is
// nil when both sides produce none. On Extract error, Apply returns an Output
// with nil Directives and the diagnostics composed so far, plus the non-nil
// error. On ctx cancellation Apply returns an empty Output and ctx.Err().
//
// Apply always uses the buffered Extract path in ABI v1, even when the
// importer satisfies [Streaming]. Apply does NOT call [Configurable.Configure];
// configuration is the caller's responsibility before Apply is invoked.
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
