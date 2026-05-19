package importerutil

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// BalanceWith adds a counterpart Posting to a single-posting *ast.Transaction
// so the transaction balances. account is assigned unvalidated to the
// counterpart's Account field; a malformed name will fail downstream
// validation, not here. currency overrides the counterpart's currency when
// non-empty; when empty the source posting's currency is used.
//
// The returned directive is a deep clone with the counterpart appended at
// index 1. The input is never mutated.
//
// No-op cases — the input directive is returned aliased, without allocation:
//   - d is not *ast.Transaction.
//   - d is *ast.Transaction with zero or two-or-more postings.
//   - d is *ast.Transaction with one posting whose Amount is nil.
//   - d is nil.
func BalanceWith(d ast.Directive, account string, currency string) ast.Directive {
	t, ok := d.(*ast.Transaction)
	if !ok {
		return d
	}
	if len(t.Postings) != 1 || t.Postings[0].Amount == nil {
		return d
	}
	out := t.Clone()
	out.Postings = append(out.Postings, negatedCounterpart(t.Postings[0], account, currency))
	return out
}

// negatedCounterpart builds a Posting whose Amount is the arithmetic negation
// of src.Amount. Currency falls back to src.Amount.Currency when the override
// is empty; Span is copied from src so the counterpart inherits its source
// location. Cost, Price, Flag, and Meta are intentionally left zero.
func negatedCounterpart(src ast.Posting, account, currency string) ast.Posting {
	neg := new(apd.Decimal)
	// Neg on a finite decimal is exact
	_, _ = apd.BaseContext.Neg(neg, &src.Amount.Number)

	cur := src.Amount.Currency
	if currency != "" {
		cur = currency
	}

	return ast.Posting{
		Span:    src.Span,
		Account: ast.Account(account),
		Amount:  &ast.Amount{Number: *neg, Currency: cur},
	}
}
