package classify

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
)

// DiagNoRule is emitted as a Warning diagnostic when a single-posting
// Transaction matches no configured rule. Severity: ast.Warning.
const DiagNoRule = "classify-no-rule"

// Hook is the classify hook. Registered as a process-global singleton; see
// the package documentation for the concurrency contract.
type Hook struct {
	rules []rule
}

// Name returns "classify".
func (h *Hook) Name() string { return "classify" }

// Apply replaces each single-posting *ast.Transaction with its two-leg form
// using the first matching rule's account and currency. Single-posting
// transactions with no matching rule emit a [DiagNoRule] Warning and pass
// through unchanged.
func (h *Hook) Apply(ctx context.Context, in hook.HookInput) (hook.HookResult, error) {
	if err := ctx.Err(); err != nil {
		return hook.HookResult{}, err
	}

	rules := h.rules

	hasSingleLeg := false
	for _, d := range in.Directives {
		if isSingleLeg(d) {
			hasSingleLeg = true
			break
		}
	}
	if !hasSingleLeg {
		return hook.HookResult{Directives: in.Directives}, nil
	}

	out := make([]ast.Directive, len(in.Directives))
	var diags []ast.Diagnostic

	for i, d := range in.Directives {
		if i > 0 && i%64 == 0 { // amortize ctx.Err cost
			if err := ctx.Err(); err != nil {
				return hook.HookResult{
					Directives:  out[:i],
					Diagnostics: diags,
				}, err
			}
		}

		if !isSingleLeg(d) {
			out[i] = d
			continue
		}
		tx := d.(*ast.Transaction)
		result, matched := applyRules(tx, rules)
		if !matched {
			out[i] = d
			diags = append(diags, ast.Diagnostic{
				Code:     DiagNoRule,
				Span:     tx.Span,
				Message:  fmt.Sprintf("no classify rule matched (payee=%q narration=%q)", tx.Payee, tx.Narration),
				Severity: ast.Warning,
			})
			continue
		}
		out[i] = result
	}

	return hook.HookResult{Directives: out, Diagnostics: diags}, nil
}

func isSingleLeg(d ast.Directive) bool {
	tx, ok := d.(*ast.Transaction)
	return ok && len(tx.Postings) == 1
}

// applyRules walks rules in declaration order. When a rule's selectors all
// match tx (AND semantics when both payeeRegex and narrationRegex are set),
// result is the BalanceWith clone with the counterpart posting and matched
// is true. When no rule matches, result is nil and matched is false.
func applyRules(tx *ast.Transaction, rules []rule) (result ast.Directive, matched bool) {
	for _, r := range rules {
		if r.payeeRegex != nil && !r.payeeRegex.MatchString(tx.Payee) {
			continue
		}
		if r.narrationRegex != nil && !r.narrationRegex.MatchString(tx.Narration) {
			continue
		}
		return importerutil.BalanceWith(tx, r.account, r.currency), true
	}
	return nil, false
}
