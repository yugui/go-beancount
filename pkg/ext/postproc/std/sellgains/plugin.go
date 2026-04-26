package sellgains

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeInvalidSellGains is the diagnostic code emitted when the
// price-implied proceeds and the sum of non-Income posting weights of
// a sale transaction disagree by more than the inferred tolerance.
// Upstream's SellGainsError namedtuple has no machine-readable
// category; the kebab-case code lets downstream tooling match without
// parsing the human-readable message.
const codeInvalidSellGains = "invalid-sell-gains"

// extraToleranceMultiplier mirrors upstream's
// EXTRA_TOLERANCE_MULTIPLIER. The factor of 2 is applied on top of the
// per-currency inferred tolerance to give a small extra margin to
// users satisfying both a price-side and a proceeds-side rounding
// constraint at once.
const extraToleranceMultiplier = 2

// defaultToleranceMultiplier is the Beancount-default value for the
// `inferred_tolerance_multiplier` option. This port uses the default
// directly because the plugin Input does not yet carry option
// directives (see doc.go for the deviation note).
const defaultToleranceMultiplier = "0.5"

// proceedsRoots is the set of account roots whose postings contribute
// to the proceeds side of the equation. The Income root is excluded
// because the plugin's whole purpose is to derive its amount from the
// price; including it would make the check tautological.
var proceedsRoots = map[ast.Account]struct{}{
	ast.Assets:      {},
	ast.Liabilities: {},
	ast.Equity:      {},
	ast.Expenses:    {},
}

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.sellgains", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/sellgains", api.PluginFunc(apply))
}

// apply emits one diagnostic per Transaction whose price-implied
// proceeds disagree with the sum of its non-Income posting weights.
// It is diagnostic-only and returns nil Result.Directives. See the
// package godoc for the full behavior, the chosen anchor convention,
// and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	var errs []api.Error
	for _, d := range in.Directives {
		t, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		diag, ok := checkTransaction(t, in.Directive)
		if ok {
			errs = append(errs, diag)
		}
	}

	if len(errs) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Errors: errs}, nil
}

// checkTransaction returns a single diagnostic for t when the
// price-implied proceeds side and the non-Income proceeds side
// disagree by more than the inferred tolerance, currency by currency.
// The second return value indicates whether a diagnostic was produced.
func checkTransaction(t *ast.Transaction, trigger *ast.Plugin) (api.Error, bool) {
	// Find the postings held at cost. If there are none, this is not a
	// sale-with-cost-basis transaction — skip silently.
	var atCost []*ast.Posting
	for i := range t.Postings {
		p := &t.Postings[i]
		if p.Cost != nil {
			atCost = append(atCost, p)
		}
	}
	if len(atCost) == 0 {
		return api.Error{}, false
	}
	// The check requires a price annotation on every cost-bearing
	// posting. Without one, we have no independent quantity to verify
	// against, so the transaction is skipped — matching upstream.
	for _, p := range atCost {
		if p.Price == nil {
			return api.Error{}, false
		}
	}

	// totalPrice accumulates -(units × price) for each cost-bearing
	// posting. The sign mirrors upstream: a typical sale posting has
	// negative units, so totalPrice ends up positive in the price
	// currency (it represents the proceeds the price says we should
	// have received).
	totalPrice := map[string]*apd.Decimal{}
	// totalProceeds accumulates the weight of every non-cost-bearing
	// posting whose account root is in proceedsRoots. The weights
	// retain their natural signs: cash inflows are positive on the
	// Asset side; fees are positive on the Expense side. For a sale,
	// totalPrice and totalProceeds end up of comparable magnitude in
	// the same currency, and the diagnostic fires when their
	// difference exceeds tolerance.
	totalProceeds := map[string]*apd.Decimal{}

	for _, p := range atCost {
		// price × -units, in price currency.
		neg := new(apd.Decimal)
		if _, err := apd.BaseContext.Neg(neg, &p.Amount.Number); err != nil {
			return api.Error{}, false
		}
		var contrib apd.Decimal
		var cur string
		if p.Price.IsTotal {
			// "@@ X CUR" — the Price.Amount is already the total. Its
			// sign is taken from the units sign for the proceeds-side
			// convention, which means we flip when units flip.
			//
			// Upstream's amount.mul(price, -units) for a total-price
			// annotation isn't strictly correct in beancount semantics,
			// since the total form is independent of unit count.
			// However, for sellgains in particular, upstream applies
			// `amount.mul(posting.price, -posting.units.number)` even
			// when the price is the total form, so we mirror its
			// behavior. The same multiplication produces a value with
			// a magnitude that no longer equals the user-entered total,
			// but matches whatever upstream produced. Tests pin this.
			if _, err := apd.BaseContext.Mul(&contrib, &p.Price.Amount.Number, neg); err != nil {
				return api.Error{}, false
			}
			cur = p.Price.Amount.Currency
		} else {
			// "@ X CUR" — per-unit price.
			if _, err := apd.BaseContext.Mul(&contrib, &p.Price.Amount.Number, neg); err != nil {
				return api.Error{}, false
			}
			cur = p.Price.Amount.Currency
		}
		addInto(totalPrice, cur, &contrib)
	}

	// Walk every posting that does NOT carry a Cost annotation. Cost
	// postings are the price side; their weight already lives in the
	// totalPrice map under upstream's accounting. Non-cost postings
	// whose account root sits in proceedsRoots contribute their weight
	// to totalProceeds.
	for i := range t.Postings {
		p := &t.Postings[i]
		if p.Cost != nil {
			continue
		}
		if _, ok := proceedsRoots[p.Account.Root()]; !ok {
			continue
		}
		w, wcur, ok := postingWeight(p)
		if !ok {
			continue
		}
		addInto(totalProceeds, wcur, w)
	}

	// Compare currency by currency. Allowed disagreement per currency
	// is `multiplier × 10^minExp × extraToleranceMultiplier`, where
	// minExp is the smallest exponent (most precise number) seen among
	// posting amounts in that currency.
	tol := inferTolerances(t.Postings)

	// Collect currencies seen on either side, then iterate in sorted
	// order so the diagnostic message is stable.
	currencies := map[string]struct{}{}
	for c := range totalPrice {
		currencies[c] = struct{}{}
	}
	for c := range totalProceeds {
		currencies[c] = struct{}{}
	}
	curList := make([]string, 0, len(currencies))
	for c := range currencies {
		curList = append(curList, c)
	}
	sort.Strings(curList)

	var disagree []string
	for _, cur := range curList {
		price := totalPrice[cur]
		if price == nil {
			price = new(apd.Decimal)
		}
		proc := totalProceeds[cur]
		if proc == nil {
			proc = new(apd.Decimal)
		}
		// diff = price - proceeds. On a balanced sale, the price-side
		// total and the non-Income proceeds total agree, so |diff| is
		// near zero.
		diff := new(apd.Decimal)
		if _, err := apd.BaseContext.Sub(diff, price, proc); err != nil {
			return api.Error{}, false
		}
		abs := new(apd.Decimal)
		if _, err := apd.BaseContext.Abs(abs, diff); err != nil {
			return api.Error{}, false
		}
		curTol := tol[cur]
		if curTol == nil {
			curTol = new(apd.Decimal)
		}
		if abs.Cmp(curTol) > 0 {
			disagree = append(disagree, fmt.Sprintf("%s: price=%s proceeds=%s diff=%s tol=%s", cur, price.Text('f'), proc.Text('f'), diff.Text('f'), curTol.Text('f')))
		}
	}
	if len(disagree) == 0 {
		return api.Error{}, false
	}

	return api.Error{
		Code:    codeInvalidSellGains,
		Span:    diagSpan(t, trigger),
		Message: fmt.Sprintf("Invalid price vs. proceeds for %s: %s", t.Date.Format("2006-01-02"), strings.Join(disagree, "; ")),
	}, true
}

// postingWeight returns the posting's weight for proceeds-summing
// purposes: units × cost-per-unit when a Cost is present, otherwise
// units × price-per-unit when a Price is present, otherwise the plain
// Amount. The boolean is false when the posting has no Amount and no
// derived weight can be computed.
//
// In the sellgains check this helper is invoked only on postings
// without a Cost (cost-bearing postings live on the price side of the
// equation), so the cost branch is dead code in practice; it is kept
// here for parity with `convert.get_weight` in case a future caller
// needs the full mapping.
func postingWeight(p *ast.Posting) (*apd.Decimal, string, bool) {
	if p.Amount == nil {
		return nil, "", false
	}
	if p.Cost != nil {
		switch {
		case p.Cost.PerUnit != nil:
			out := new(apd.Decimal)
			if _, err := apd.BaseContext.Mul(out, &p.Amount.Number, &p.Cost.PerUnit.Number); err != nil {
				return nil, "", false
			}
			return out, p.Cost.PerUnit.Currency, true
		case p.Cost.Total != nil:
			// Total cost: the contribution is the total in the cost
			// currency, signed by the units sign.
			out := new(apd.Decimal)
			out.Set(&p.Cost.Total.Number)
			if p.Amount.Number.Negative {
				if _, err := apd.BaseContext.Neg(out, out); err != nil {
					return nil, "", false
				}
			}
			return out, p.Cost.Total.Currency, true
		}
		// Empty cost spec: fall through to plain amount.
	}
	if p.Price != nil {
		out := new(apd.Decimal)
		if p.Price.IsTotal {
			out.Set(&p.Price.Amount.Number)
			if p.Amount.Number.Negative {
				if _, err := apd.BaseContext.Neg(out, out); err != nil {
					return nil, "", false
				}
			}
		} else {
			if _, err := apd.BaseContext.Mul(out, &p.Amount.Number, &p.Price.Amount.Number); err != nil {
				return nil, "", false
			}
		}
		return out, p.Price.Amount.Currency, true
	}
	out := new(apd.Decimal)
	out.Set(&p.Amount.Number)
	return out, p.Amount.Currency, true
}

// inferTolerances returns the per-currency tolerance map for the
// transaction's postings. The tolerance for a currency is
// `defaultToleranceMultiplier × 10^minExp × extraToleranceMultiplier`,
// where minExp is the smallest (most precise) exponent observed among
// posting Amount fields (the units side) in that currency. Price and
// cost numbers are intentionally ignored, matching upstream's
// `interpolate.infer_tolerances` which only consumes posting `units`
// unless the `infer_tolerance_from_cost` option is enabled (this port
// does not yet thread that option; see doc.go).
//
// Currencies absent from any unit-side posting receive a zero
// tolerance, which produces a strict equality check — a deliberate
// fallback mirroring upstream's behavior for currencies missing from
// the inferred map.
func inferTolerances(postings []ast.Posting) map[string]*apd.Decimal {
	minExp := map[string]int32{}
	for i := range postings {
		p := &postings[i]
		if p.Amount == nil {
			continue
		}
		cur := p.Amount.Currency
		exp := p.Amount.Number.Exponent
		if e, ok := minExp[cur]; !ok || exp < e {
			minExp[cur] = exp
		}
	}
	mult := mustDecimal(defaultToleranceMultiplier)
	out := make(map[string]*apd.Decimal, len(minExp))
	for cur, e := range minExp {
		base := new(apd.Decimal)
		base.Set(mult)
		base.Exponent += e
		// extra := base × extraToleranceMultiplier.
		extra := apd.New(extraToleranceMultiplier, 0)
		final := new(apd.Decimal)
		// Multiplying base (a small bounded magnitude derived from a
		// posting exponent) by the constant extraToleranceMultiplier
		// cannot overflow under apd.BaseContext. If a condition
		// unexpectedly fires we conservatively fall back to the
		// un-multiplied base, which yields a tighter (stricter)
		// tolerance — safer for a diagnostic-only check.
		if _, err := apd.BaseContext.Mul(final, base, extra); err != nil {
			final = base
		}
		out[cur] = final
	}
	return out
}

// addInto adds delta to the named-currency entry of m, allocating a
// fresh apd.Decimal when the entry is missing. Callers retain
// ownership of delta; the value is copied via Add into the map's
// independent storage.
func addInto(m map[string]*apd.Decimal, cur string, delta *apd.Decimal) {
	cur = strings.TrimSpace(cur)
	if cur == "" {
		return
	}
	cell, ok := m[cur]
	if !ok {
		cell = new(apd.Decimal)
		m[cur] = cell
	}
	// apd.BaseContext.Add cannot fail for bounded monetary values; the
	// runtime error path is reserved for overflow and invalid-operation
	// conditions, neither of which can occur within ledger-realistic
	// magnitudes accumulated here.
	_, _ = apd.BaseContext.Add(cell, cell, delta)
}

// mustDecimal parses a decimal literal that is statically known to be
// valid; it panics on parse error. Used only for package-level
// constants like the default tolerance multiplier.
func mustDecimal(s string) *apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(fmt.Errorf("sellgains: cannot parse decimal %q: %w", s, err))
	}
	return d
}

// diagSpan picks the most actionable span for a diagnostic. The
// offending Transaction is where the user fixes the imbalance (correct
// the price, the units, or the proceeds postings), so we prefer it
// when its Span is non-zero. The triggering plugin directive's Span
// is the fallback, matching the convention used by sibling ports.
func diagSpan(t *ast.Transaction, trigger *ast.Plugin) ast.Span {
	if t != nil {
		var zero ast.Span
		if t.Span != zero {
			return t.Span
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
