package coherentcost

import (
	"context"
	"fmt"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeIncoherentCost is the diagnostic code emitted when an
// (Account, Commodity) pair is held both with and without a cost
// annotation. Upstream's CoherentCostError namedtuple has no
// machine-readable category; the kebab-case code lets downstream
// tooling (lsp, log filters) match without parsing the human-readable
// message.
const codeIncoherentCost = "incoherent-cost"

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.coherent_cost", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/coherentcost", api.PluginFunc(apply))
}

// acctCur is the per-(account, commodity) observation key.
type acctCur struct {
	Account   ast.Account
	Commodity string
}

// apply emits one diagnostic per (Account, Commodity) pair held both
// with and without a cost annotation. It is diagnostic-only and
// returns nil Result.Directives. See the package godoc for the full
// behavior, the chosen anchor convention, and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// withCost / withoutCost record, per (account, commodity), whether
	// any posting was observed with a non-nil Cost annotation and
	// whether any was observed with a nil Cost annotation. A key
	// present in both maps is the offender we report.
	withCost := map[acctCur]struct{}{}
	withoutCost := map[acctCur]struct{}{}

	// opens records the first Open directive seen per account so each
	// diagnostic can anchor at the actionable-fix location.
	opens := map[ast.Account]*ast.Open{}

	for _, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Open:
			if _, exists := opens[x.Account]; !exists {
				opens[x.Account] = x
			}
		case *ast.Transaction:
			for i := range x.Postings {
				p := &x.Postings[i]
				if p.Amount == nil {
					// Auto-balanced posting carries no currency; it
					// contributes nothing to either set.
					continue
				}
				k := acctCur{Account: p.Account, Commodity: p.Amount.Currency}
				if p.Cost == nil {
					withoutCost[k] = struct{}{}
				} else {
					withCost[k] = struct{}{}
				}
			}
		}
	}

	// Collect offenders: keys present in both maps.
	var offenders []acctCur
	for k := range withCost {
		if _, also := withoutCost[k]; also {
			offenders = append(offenders, k)
		}
	}
	if len(offenders) == 0 {
		return api.Result{}, nil
	}

	// Sort by (Account, Commodity) for stable output across runs;
	// Go's map iteration order is randomized.
	sort.Slice(offenders, func(i, j int) bool {
		if offenders[i].Account != offenders[j].Account {
			return offenders[i].Account < offenders[j].Account
		}
		return offenders[i].Commodity < offenders[j].Commodity
	})

	diags := make([]ast.Diagnostic, 0, len(offenders))
	for _, k := range offenders {
		diags = append(diags, ast.Diagnostic{
			Code:     codeIncoherentCost,
			Span:     diagSpan(opens[k.Account], in.Directive),
			Message:  fmt.Sprintf("Account '%s' holds '%s' both with and without a cost", k.Account, k.Commodity),
			Severity: ast.Error,
		})
	}
	return api.Result{Diagnostics: diags}, nil
}

// diagSpan picks the most actionable span for a diagnostic. The Open
// directive is the place a user typically fixes a booking issue (by
// adjusting the booking method or splitting the account), so we
// prefer it when available. The triggering plugin directive's Span is
// the fallback, matching the convention used by sibling onecommodity.
func diagSpan(o *ast.Open, trigger *ast.Plugin) ast.Span {
	if o != nil {
		var zero ast.Span
		if o.Span != zero {
			return o.Span
		}
	}
	return spanOf(trigger)
}

// spanOf returns the span of the triggering plugin directive, or the
// zero span when no trigger was supplied.
func spanOf(p *ast.Plugin) ast.Span {
	if p == nil {
		return ast.Span{}
	}
	return p.Span
}
