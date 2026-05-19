package hook

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Chain runs the hooks named by names from reg, in caller-supplied order.
//
// Empty names returns HookResult{Directives: in.Directives, Diagnostics: nil}
// with zero allocations — the returned Directives shares the same backing
// array as in.Directives.
//
// For each rung, Chain checks ctx.Err() first; on cancellation it returns the
// composed-so-far HookResult together with ctx.Err(). If a name is not in the
// registry, Chain halts and returns the composed-so-far HookResult augmented
// with a DiagHookNotRegistered Error diagnostic and a nil error. If Apply
// returns a non-nil error, Chain halts: it returns the previous rung's
// Directives (the failing hook's Directives are discarded), the composed
// diagnostics (including any the failing hook emitted), and the error.
//
// Diagnostics from successive rungs concatenate in chain order. When no rung
// emits any diagnostic, the returned Diagnostics is nil (not an empty slice).
// Chain MUST NOT defensively copy Directives between rungs.
func Chain(ctx context.Context, reg Registry, names []string, in HookInput) (HookResult, error) {
	if len(names) == 0 {
		return HookResult{Directives: in.Directives}, nil
	}

	var diags []ast.Diagnostic
	current := in.Directives
	if current == nil {
		current = []ast.Directive{}
	}

	for _, name := range names {
		if ctx.Err() != nil {
			return HookResult{Directives: current, Diagnostics: diags}, ctx.Err()
		}

		h, ok := reg.Lookup(name)
		if !ok {
			diag := ast.Diagnostic{
				Code:     DiagHookNotRegistered,
				Message:  fmt.Sprintf("hook %q is not registered", name),
				Severity: ast.Error,
			}
			diags = append(diags, diag)
			return HookResult{Directives: current, Diagnostics: diags}, nil
		}

		result, err := h.Apply(ctx, HookInput{
			Directives: current,
			Hints:      in.Hints,
			Options:    in.Options,
		})
		diags = append(diags, result.Diagnostics...)
		if err != nil {
			return HookResult{Directives: current, Diagnostics: nilIfEmpty(diags)}, err
		}
		current = result.Directives
	}

	return HookResult{Directives: current, Diagnostics: nilIfEmpty(diags)}, nil
}

// nilIfEmpty returns nil for an empty slice, otherwise s.
func nilIfEmpty(s []ast.Diagnostic) []ast.Diagnostic {
	if len(s) == 0 {
		return nil
	}
	return s
}
