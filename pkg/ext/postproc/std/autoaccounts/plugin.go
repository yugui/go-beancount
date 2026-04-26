package autoaccounts

import (
	"context"
	"sort"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.auto_accounts", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/autoaccounts", api.PluginFunc(apply))
}

// apply synthesizes Open directives for every account referenced in the
// ledger that lacks one. See the package godoc for the full behavior
// and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// opened tracks every account that already has an explicit Open
	// directive — these are excluded from synthesis.
	opened := make(map[ast.Account]struct{})
	// firstUse maps each referenced account to the earliest date on
	// which it appears.
	firstUse := make(map[ast.Account]time.Time)

	// Materialize the iterator into a slice while gathering first-use dates.
	var all []ast.Directive
	for _, d := range in.Directives {
		all = append(all, d)
		visit(d, opened, firstUse)
	}

	// Collect accounts that need a synthesized Open. Upstream iterates
	// `sorted(accounts_first.items())`; the matching sort happens below.
	var missing []ast.Account
	for acct := range firstUse {
		if _, ok := opened[acct]; ok {
			continue
		}
		missing = append(missing, acct)
	}
	if len(missing) == 0 {
		if len(all) == 0 {
			// Truly empty input: nothing to synthesize and no input
			// directives to preserve. Return nil Directives to signal
			// "no change" per the Result contract.
			return api.Result{}, nil
		}
		// No new directives: return the original input unchanged but
		// in a freshly allocated slice so the runner's Result.Directives
		// contract (non-nil = replace) is honored without aliasing the
		// caller's storage.
		out := make([]ast.Directive, len(all))
		copy(out, all)
		return api.Result{Directives: out}, nil
	}
	// Sort alphabetically so output is deterministic.
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })

	out := make([]ast.Directive, 0, len(all)+len(missing))
	for _, acct := range missing {
		out = append(out, &ast.Open{
			Date:    firstUse[acct],
			Account: acct,
		})
	}
	out = append(out, all...)
	return api.Result{Directives: out}, nil
}

// visit records the accounts referenced by d. opened receives accounts
// from explicit Open directives so the caller can exclude them from
// synthesis. firstUse retains the earliest date each account is
// referenced; if an account appears multiple times, the smaller date
// wins.
func visit(d ast.Directive, opened map[ast.Account]struct{}, firstUse map[ast.Account]time.Time) {
	switch x := d.(type) {
	case *ast.Open:
		opened[x.Account] = struct{}{}
		recordUse(firstUse, x.Account, x.Date)
	case *ast.Close:
		recordUse(firstUse, x.Account, x.Date)
	case *ast.Balance:
		recordUse(firstUse, x.Account, x.Date)
	case *ast.Pad:
		recordUse(firstUse, x.Account, x.Date)
		recordUse(firstUse, x.PadAccount, x.Date)
	case *ast.Note:
		recordUse(firstUse, x.Account, x.Date)
	case *ast.Document:
		recordUse(firstUse, x.Account, x.Date)
	case *ast.Transaction:
		for i := range x.Postings {
			recordUse(firstUse, x.Postings[i].Account, x.Date)
		}
	}
}

// recordUse stores date as the earliest first-use of acct, keeping the
// existing entry if it is already earlier or equal.
func recordUse(firstUse map[ast.Account]time.Time, acct ast.Account, date time.Time) {
	if acct == "" {
		return
	}
	if prev, ok := firstUse[acct]; ok && !date.Before(prev) {
		return
	}
	firstUse[acct] = date
}
