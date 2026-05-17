package inventory

import (
	"errors"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// PostingWeight computes the signed weight a posting contributes to
// the transaction's currency sums. An auto-posting (p.Amount == nil)
// returns (nil, nil).
//
// Precedence (matching upstream Beancount's Balancing Postings rule):
//
//   - p.Cost with a number: weight is [(*ast.Posting).TotalCost] in
//     the cost currency, covering `{X CUR}`, `{{T CUR}}`, and the
//     combined `{X # T CUR}` forms. When a price annotation is also
//     present the cost still defines the weight; the price is only
//     consulted by downstream consumers (prices database, realized-
//     gain bookkeeping).
//   - p.Cost present but unresolved (unbooked spec with a currency
//     and no number): returns an error — the reducer must resolve
//     the cost before weighing.
//   - p.Price (and no cost): per-unit `@P` → units * P, total `@@P`
//     → sign(units) * |P|, in the price currency.
//   - otherwise: plain units in their commodity currency.
//
// The returned *ast.Amount is freshly allocated and its Number does
// not alias any AST field, so callers may mutate it in place (e.g.
// to negate it when computing an auto-posting residual).
func PostingWeight(p *ast.Posting) (*ast.Amount, error) {
	if p.Amount == nil {
		return nil, nil
	}
	cost, err := p.TotalCost()
	if err != nil {
		return nil, err
	}
	if cost != nil {
		return &ast.Amount{Number: *ast.CloneDecimal(&cost.Number), Currency: cost.Currency}, nil
	}
	if p.Cost != nil && !p.Cost.IsBooked() && p.Cost.GetCurrency() != "" {
		return nil, errors.New("posting weight: cost spec has currency but no number; reducer must resolve before weighing")
	}
	if p.Price != nil {
		num := p.Amount.Number
		priceNum := p.Price.Amount.Number
		if p.Price.IsTotal {
			out, err := signedAbs(&num, &priceNum)
			if err != nil {
				return nil, err
			}
			return &ast.Amount{Number: *out, Currency: p.Price.Amount.Currency}, nil
		}
		out := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(out, &num, &priceNum); err != nil {
			return nil, err
		}
		return &ast.Amount{Number: *out, Currency: p.Price.Amount.Currency}, nil
	}
	return &ast.Amount{Number: *ast.CloneDecimal(&p.Amount.Number), Currency: p.Amount.Currency}, nil
}

// signedAbs returns sign(units) * |val| as a freshly allocated
// decimal, used for the total-price (`@@`) branch of PostingWeight.
func signedAbs(units, val *apd.Decimal) (*apd.Decimal, error) {
	abs := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(abs, val); err != nil {
		return nil, err
	}
	if !units.Negative {
		return abs, nil
	}
	out := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(out, abs); err != nil {
		return nil, err
	}
	return out, nil
}
