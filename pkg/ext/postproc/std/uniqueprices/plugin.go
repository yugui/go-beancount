package uniqueprices

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeDuplicatePrice is the diagnostic code emitted for a Price
// directive whose (date, base, quote) triple disagrees with an earlier
// Price directive on the same triple. Upstream's UniquePricesError
// namedtuple has no machine-readable category; we pick a stable
// kebab-case code so downstream tooling can match on it without
// parsing the human-readable message.
const codeDuplicatePrice = "duplicate-price"

// Dual registration: upstream's Python module path (with underscore)
// and this package's Go import path (no underscore, since Go package
// identifiers cannot contain underscores). See doc.go for the
// rationale.
func init() {
	postproc.Register("beancount.plugins.unique_prices", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/uniqueprices", api.PluginFunc(apply))
}

// apply emits one diagnostic per Price directive whose (date, base,
// quote) triple has an earlier Price directive on the same triple
// declaring a different value. It is diagnostic-only and returns nil
// Result.Directives. See the package godoc for the full behavior and
// upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// firsts records, per (date, base, quote) triple, the first Price
	// directive seen with that key. Subsequent Price directives on the
	// same triple are compared against this first one: equal values are
	// silently accepted as duplicates, unequal values yield a
	// diagnostic. This matches upstream's grouping by `(date, currency,
	// amount.currency)` while preserving deterministic
	// source-encounter ordering for the diagnostics.
	type key struct {
		date  string
		base  string
		quote string
	}
	firsts := map[key]*ast.Price{}

	var diags []ast.Diagnostic
	for _, d := range in.Directives {
		p, ok := d.(*ast.Price)
		if !ok {
			continue
		}
		k := key{
			date:  p.Date.Format("2006-01-02"),
			base:  p.Commodity,
			quote: p.Amount.Currency,
		}
		first, seen := firsts[k]
		if !seen {
			firsts[k] = p
			continue
		}
		// apd.Decimal.Cmp returns 0 when the numeric values are equal,
		// regardless of internal representation (e.g. 1.5 vs 1.50).
		// Equal values are duplicates, not conflicts.
		if first.Amount.Number.Cmp(&p.Amount.Number) == 0 {
			continue
		}
		diags = append(diags, ast.Diagnostic{
			Code:     codeDuplicatePrice,
			Span:     diagSpan(p, in.Directive),
			Message:  fmt.Sprintf("Disagreeing price for %s/%s on %s: %s vs %s", p.Commodity, p.Amount.Currency, k.date, &first.Amount.Number, &p.Amount.Number),
			Severity: ast.Error,
		})
	}

	if len(diags) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Diagnostics: diags}, nil
}

// diagSpan picks the most actionable span for a diagnostic. The
// offending Price directive is where the user adds, removes, or
// corrects a price assertion, so we prefer it when its Span is
// non-zero. The triggering plugin directive's Span is the fallback,
// matching the convention used by sibling ports.
func diagSpan(p *ast.Price, trigger *ast.Plugin) ast.Span {
	if p != nil {
		var zero ast.Span
		if p.Span != zero {
			return p.Span
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
