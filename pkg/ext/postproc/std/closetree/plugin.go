package closetree

import (
	"context"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// Dual registration: upstream's Python module path (with underscore)
// and this package's Go import path (no underscore, since Go package
// identifiers cannot contain underscores). See doc.go for the
// rationale.
func init() {
	postproc.Register("beancount.plugins.close_tree", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/closetree", api.PluginFunc(apply))
}

// apply walks every directive once, copying it through to the output,
// and synthesizes one Close directive per still-open descendant of any
// closed account. See the package godoc for the full behavior, ordering
// policy, and deviations from upstream.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// openedBy tracks every account that has been Open'ed, with the
	// Open directive itself retained so we can consult its Date. A
	// later Open for the same account does not overwrite an earlier
	// one; reopens are not modelled by this plugin (or by upstream).
	openedBy := make(map[ast.Account]*ast.Open)

	// closed tracks accounts whose Close has been observed in source
	// order (either explicit, or already synthesized in this pass). It
	// is the dedup key for "do not double-close".
	closed := make(map[ast.Account]struct{})

	// Materialize once so the second pass can stream output in source
	// order without re-running the iterator (api.Input.Directives is
	// re-runnable, but materializing is cheaper for a two-pass walk).
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
	}
	if len(all) == 0 {
		return api.Result{}, nil
	}

	// Pre-grow: at most one synthesized Close per Open in the worst
	// case (every account closed by a single root Close).
	out := make([]ast.Directive, 0, len(all))
	synthesized := false
	for _, d := range all {
		// Poll cancellation per directive so large ledgers observe
		// context cancellation promptly rather than only at entry.
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}

		switch x := d.(type) {
		case *ast.Open:
			// Record the Open so subsequent Closes can find their
			// descendants. A later duplicate Open for the same account
			// is ignored (first one wins).
			if _, ok := openedBy[x.Account]; !ok {
				openedBy[x.Account] = x
			}
			out = append(out, d)
		case *ast.Close:
			// The original Close is preserved unconditionally — see
			// doc.go's "original Close is preserved unconditionally"
			// deviation.
			out = append(out, d)
			closed[x.Account] = struct{}{}

			subs := descendants(x, openedBy, closed)
			for _, sub := range subs {
				out = append(out, &ast.Close{
					Span:    x.Span,
					Date:    x.Date,
					Account: sub,
				})
				closed[sub] = struct{}{}
				synthesized = true
			}
		default:
			out = append(out, d)
		}
	}

	if !synthesized {
		// Nothing was synthesized: signal "no change" per the
		// Result.Directives contract used elsewhere in this package
		// family (implicitprices, autoaccounts, …). Returning the
		// already-built `out` slice is also legal but would force the
		// runner to walk it; nil is the cheaper signal.
		return api.Result{}, nil
	}
	return api.Result{Directives: out}, nil
}

// descendants returns the accounts that should have a Close synthesized
// for the parent Close c, in alphabetical order. An account A is a
// descendant when:
//
//   - A is a strict component-child of c.Account (see [isDescendant]);
//   - A has been Open'ed in source order before c (entry in openedBy)
//     and the Open's Date is on or before c.Date;
//   - A has not been Closed in source order before c, and has not
//     already been synthesized in this pass (entry not in closed).
//
// Alphabetical ordering keeps test expectations and user-visible diffs
// stable; upstream sorts by no key (set iteration), so this is a
// deliberate strengthening rather than an upstream-matching choice.
func descendants(c *ast.Close, openedBy map[ast.Account]*ast.Open, closed map[ast.Account]struct{}) []ast.Account {
	var subs []ast.Account
	for acct, op := range openedBy {
		if _, alreadyClosed := closed[acct]; alreadyClosed {
			continue
		}
		if !isDescendant(acct, c.Account) {
			continue
		}
		// Skip Opens whose date is strictly after the parent Close's
		// date — synthesizing a Close that predates the Open would
		// produce an invalid ledger. See doc.go's "date-aware Open
		// visibility" deviation.
		if op.Date.After(c.Date) {
			continue
		}
		subs = append(subs, acct)
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i] < subs[j] })
	return subs
}

// isDescendant reports whether child is a strict component-aware
// descendant of parent. The walk is via [ast.Account.Parent], so
// `Assets:CashFlow` is correctly NOT a descendant of `Assets:Cash`
// (they are siblings under `Assets`). See doc.go's "component-aware
// ancestry" deviation.
func isDescendant(child, parent ast.Account) bool {
	if child == "" || parent == "" {
		return false
	}
	if child == parent {
		return false
	}
	for a := child.Parent(); a != ""; a = a.Parent() {
		if a == parent {
			return true
		}
	}
	return false
}
