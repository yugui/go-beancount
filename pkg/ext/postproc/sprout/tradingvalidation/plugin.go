package tradingvalidation

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/inventory"
)

const (
	codeTradingNotBalanced          = "trading-not-balanced"
	codeTradingCommodityNotBalanced = "trading-commodity-not-balanced"

	defaultPrefix = "Equity:Trading"

	defaultToleranceMultiplier = "0.5"
)

func init() {
	postproc.Register("beansprout.plugins.trading_validation", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/tradingvalidation", api.PluginFunc(apply))
}

func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	prefix := strings.TrimSpace(in.Config)
	if prefix == "" {
		prefix = defaultPrefix
	}

	if in.Directives == nil {
		return api.Result{}, nil
	}

	tolMult := toleranceMultiplier(in.Options)

	disabledCommodities := map[string]struct{}{}
	var transactions []*ast.Transaction

	for _, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Commodity:
			if v, ok := x.Meta.Props["trading-account"]; ok && v.Kind == ast.MetaString && v.String == "disabled" {
				disabledCommodities[x.Currency] = struct{}{}
			}
		case *ast.Transaction:
			transactions = append(transactions, x)
		}
	}

	var diags []ast.Diagnostic
	for _, tx := range transactions {
		if !hasTradingPosting(tx, prefix) {
			continue
		}
		diags = append(diags, checkTransaction(tx, prefix, disabledCommodities, tolMult, in.Directive)...)
	}

	if len(diags) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Diagnostics: diags}, nil
}

func hasTradingPosting(tx *ast.Transaction, prefix string) bool {
	for i := range tx.Postings {
		if isTrading(tx.Postings[i].Account, prefix) {
			return true
		}
	}
	return false
}

func isTrading(acct ast.Account, prefix string) bool {
	s := string(acct)
	return s == prefix || strings.HasPrefix(s, prefix+":")
}

// checkTransaction validates the three balance rules for tx and returns
// any diagnostics.
func checkTransaction(
	tx *ast.Transaction,
	prefix string,
	disabled map[string]struct{},
	tolMult *apd.Decimal,
	trigger *ast.Plugin,
) []ast.Diagnostic {
	span := txSpan(tx, trigger)
	tol := inferTolerances(tx.Postings, tolMult)

	var diags []ast.Diagnostic

	// Rule 1: the weighted sum of trading-account postings must be zero.
	tradingWeights := map[string]*apd.Decimal{}
	for i := range tx.Postings {
		p := &tx.Postings[i]
		if !isTrading(p.Account, prefix) {
			continue
		}
		w, err := inventory.PostingWeight(p)
		if err != nil || w == nil {
			continue
		}
		addDecimal(tradingWeights, w.Currency, &w.Number)
	}
	for cur, sum := range tradingWeights {
		if balanceErr(sum, tol[cur]) {
			diags = append(diags, ast.Diagnostic{
				Code:     codeTradingNotBalanced,
				Span:     span,
				Message:  fmt.Sprintf("trading accounts do not balance for %s: %s", cur, sum.Text('f')),
				Severity: ast.Error,
			})
		}
	}

	// Rule 2: the weighted sum of non-trading-account postings must be zero.
	nonTradingWeights := map[string]*apd.Decimal{}
	for i := range tx.Postings {
		p := &tx.Postings[i]
		if isTrading(p.Account, prefix) {
			continue
		}
		w, err := inventory.PostingWeight(p)
		if err != nil || w == nil {
			continue
		}
		addDecimal(nonTradingWeights, w.Currency, &w.Number)
	}
	for cur, sum := range nonTradingWeights {
		if balanceErr(sum, tol[cur]) {
			diags = append(diags, ast.Diagnostic{
				Code:     codeTradingNotBalanced,
				Span:     span,
				Message:  fmt.Sprintf("non-trading accounts do not balance for %s: %s", cur, sum.Text('f')),
				Severity: ast.Error,
			})
		}
	}

	// Rule 3: per-effective-commodity balance.
	commodities := effectiveCommodities(tx, disabled)
	for commodity := range commodities {
		bals := map[string]*apd.Decimal{}
		for i := range tx.Postings {
			p := &tx.Postings[i]
			if p.Amount == nil {
				continue
			}
			unitsCur := p.Amount.Currency
			if _, dis := disabled[unitsCur]; dis {
				// disabled: grouped by price currency; contribute via weight
				// (units × price) only when the price currency matches.
				if p.Price == nil || p.Price.Amount.Currency != commodity {
					continue
				}
				w, err := inventory.PostingWeight(p)
				if err != nil || w == nil {
					continue
				}
				addDecimal(bals, w.Currency, &w.Number)
			} else {
				// normal: grouped by units currency; use raw units only.
				if unitsCur != commodity {
					continue
				}
				addDecimal(bals, unitsCur, &p.Amount.Number)
			}
		}
		for cur, sum := range bals {
			if balanceErr(sum, tol[cur]) {
				diags = append(diags, ast.Diagnostic{
					Code:     codeTradingCommodityNotBalanced,
					Span:     span,
					Message:  fmt.Sprintf("commodity %s does not balance: %s %s", commodity, sum.Text('f'), cur),
					Severity: ast.Error,
				})
			}
		}
	}

	return diags
}

// effectiveCommodities returns the set of commodity keys used for rule 3.
func effectiveCommodities(tx *ast.Transaction, disabled map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for i := range tx.Postings {
		p := &tx.Postings[i]
		if p.Amount == nil {
			continue
		}
		cur := p.Amount.Currency
		if _, dis := disabled[cur]; dis {
			if p.Price != nil {
				out[p.Price.Amount.Currency] = struct{}{}
			}
		} else {
			out[cur] = struct{}{}
		}
	}
	return out
}

// balanceErr reports whether sum is outside tolerance.
func balanceErr(sum, tolCur *apd.Decimal) bool {
	abs := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(abs, sum); err != nil {
		return false
	}
	if tolCur == nil {
		tolCur = new(apd.Decimal)
	}
	return abs.Cmp(tolCur) > 0
}

// inferTolerances returns the per-currency tolerance map. The tolerance for
// a currency is tolMult × 10^minExp, where minExp is the smallest (most
// precise) exponent observed in unit amounts in that currency.
func inferTolerances(postings []ast.Posting, tolMult *apd.Decimal) map[string]*apd.Decimal {
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
	out := make(map[string]*apd.Decimal, len(minExp))
	for cur, e := range minExp {
		base := new(apd.Decimal)
		base.Set(tolMult)
		base.Exponent += e
		out[cur] = base
	}
	return out
}

// toleranceMultiplier reads the tolerance_multiplier option, falling back
// to the beancount default of 0.5.
func toleranceMultiplier(opts *ast.OptionValues) *apd.Decimal {
	if opts != nil {
		m := opts.Decimal("tolerance_multiplier")
		if m != nil {
			return m
		}
	}
	d, _, _ := apd.NewFromString(defaultToleranceMultiplier)
	return d
}

// addDecimal adds delta into m[cur], allocating a fresh entry when absent.
func addDecimal(m map[string]*apd.Decimal, cur string, delta *apd.Decimal) {
	cell, ok := m[cur]
	if !ok {
		cell = new(apd.Decimal)
		m[cur] = cell
	}
	_, _ = apd.BaseContext.Add(cell, cell, delta)
}

// txSpan returns the most specific available span: transaction → plugin directive.
func txSpan(tx *ast.Transaction, plug *ast.Plugin) ast.Span {
	var zero ast.Span
	if tx.Span != zero {
		return tx.Span
	}
	if plug != nil {
		return plug.Span
	}
	return zero
}
