package tradingvalidation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/validation/tolerance"
)

const (
	codeTradingNotBalanced          = "trading-not-balanced"
	codeTradingCommodityNotBalanced = "trading-commodity-not-balanced"

	defaultPrefix = "Equity:Trading"
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
		more, err := checkTransaction(tx, prefix, disabledCommodities, in.Options, in.Directive)
		if err != nil {
			return api.Result{}, err
		}
		diags = append(diags, more...)
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

// checkTransaction evaluates the three balance rules against tx and
// returns one diagnostic per per-currency residual that exceeds its
// rule-scoped tolerance. The error return is reserved for tolerance
// inference or decimal arithmetic failures.
func checkTransaction(
	tx *ast.Transaction,
	prefix string,
	disabled map[string]struct{},
	opts *ast.OptionValues,
	trigger *ast.Plugin,
) ([]ast.Diagnostic, error) {
	span := txSpan(tx, trigger)
	var diags []ast.Diagnostic

	// Rule 1: weighted sum of trading-account postings must balance.
	trading := selectPostings(tx.Postings, func(p *ast.Posting) bool {
		return isTrading(p.Account, prefix)
	})
	rule1, err := residuals(trading, opts)
	if err != nil {
		return nil, err
	}
	for _, r := range rule1 {
		diags = append(diags, ast.Diagnostic{
			Code:     codeTradingNotBalanced,
			Span:     span,
			Message:  fmt.Sprintf("trading accounts do not balance for %s: %s", r.Currency, r.Sum.Text('f')),
			Severity: ast.Error,
		})
	}

	// Rule 2: weighted sum of non-trading-account postings must balance.
	nonTrading := selectPostings(tx.Postings, func(p *ast.Posting) bool {
		return !isTrading(p.Account, prefix)
	})
	rule2, err := residuals(nonTrading, opts)
	if err != nil {
		return nil, err
	}
	for _, r := range rule2 {
		diags = append(diags, ast.Diagnostic{
			Code:     codeTradingNotBalanced,
			Span:     span,
			Message:  fmt.Sprintf("non-trading accounts do not balance for %s: %s", r.Currency, r.Sum.Text('f')),
			Severity: ast.Error,
		})
	}

	// Rule 3: per-effective-commodity balance.
	for _, commodity := range sortedKeys(effectiveCommodities(tx, disabled)) {
		scoped := postingsForCommodity(tx, commodity, disabled)
		rule3, err := residuals(scoped, opts)
		if err != nil {
			return nil, err
		}
		for _, r := range rule3 {
			diags = append(diags, ast.Diagnostic{
				Code:     codeTradingCommodityNotBalanced,
				Span:     span,
				Message:  fmt.Sprintf("commodity %s does not balance: %s %s", commodity, r.Sum.Text('f'), r.Currency),
				Severity: ast.Error,
			})
		}
	}

	return diags, nil
}

// residual reports a single per-currency balance-rule failure.
type residual struct {
	Currency string
	Sum      *apd.Decimal
}

// residuals returns one residual per currency whose weighted sum of
// postings exceeds the tolerance inferred from those same postings.
// Returns nil for an empty input or a fully balanced subset.
func residuals(postings []ast.Posting, opts *ast.OptionValues) ([]residual, error) {
	if len(postings) == 0 {
		return nil, nil
	}
	sums := map[string]*apd.Decimal{}
	for i := range postings {
		w, err := inventory.PostingWeight(&postings[i])
		if err != nil || w == nil {
			continue
		}
		cell, ok := sums[w.Currency]
		if !ok {
			cell = new(apd.Decimal)
			sums[w.Currency] = cell
		}
		if _, err := apd.BaseContext.Add(cell, cell, &w.Number); err != nil {
			return nil, fmt.Errorf("accumulate posting weight: %w", err)
		}
	}

	nonZero := nonZeroSortedKeys(sums)
	if len(nonZero) == 0 {
		return nil, nil
	}
	tol, err := tolerance.Infer(postings, opts, nonZero)
	if err != nil {
		return nil, fmt.Errorf("infer tolerance: %w", err)
	}
	var out []residual
	for _, cur := range nonZero {
		within, err := tolerance.Within(sums[cur], tol[cur])
		if err != nil {
			return nil, fmt.Errorf("check tolerance: %w", err)
		}
		if !within {
			out = append(out, residual{Currency: cur, Sum: sums[cur]})
		}
	}
	return out, nil
}

// selectPostings returns a fresh slice containing shallow copies of
// the postings for which keep reports true. Callers may mutate
// returned entries (e.g. nil out Cost/Price) without affecting tx.
func selectPostings(postings []ast.Posting, keep func(*ast.Posting) bool) []ast.Posting {
	out := make([]ast.Posting, 0, len(postings))
	for i := range postings {
		if keep(&postings[i]) {
			out = append(out, postings[i])
		}
	}
	return out
}

// postingsForCommodity returns the subset of tx.Postings that
// contribute to commodity for rule 3. Disabled-commodity postings are
// kept whole so PostingWeight evaluates them in their price currency;
// normal-commodity postings are returned with Cost and Price nil-ed
// so PostingWeight degrades to raw units in the commodity's own
// currency.
func postingsForCommodity(tx *ast.Transaction, commodity string, disabled map[string]struct{}) []ast.Posting {
	out := make([]ast.Posting, 0, len(tx.Postings))
	for i := range tx.Postings {
		p := tx.Postings[i]
		if p.Amount == nil {
			continue
		}
		cur := p.Amount.Currency
		if _, dis := disabled[cur]; dis {
			if p.Price == nil || p.Price.Amount.Currency != commodity {
				continue
			}
			out = append(out, p)
		} else {
			if cur != commodity {
				continue
			}
			p.Cost = nil
			p.Price = nil
			out = append(out, p)
		}
	}
	return out
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

func nonZeroSortedKeys(m map[string]*apd.Decimal) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v != nil && !v.IsZero() {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
