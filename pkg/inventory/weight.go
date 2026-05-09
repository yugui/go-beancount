package inventory

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// PostingWeight computes the signed weight contributed by a posting to the
// transaction's currency sums.
//
// An auto-posting (p.Amount == nil) returns (nil, nil); callers that
// need to infer the missing amount should perform their own balancing.
//
// Precedence (matching upstream Beancount's Balancing Postings rule):
//
//   - p.Cost != nil: the cost spec defines the weight via
//     [(*ast.Posting).TotalCost], handling `{X CUR}`, `{{T CUR}}`, and
//     the combined `{X # T CUR}` forms uniformly. When both a cost and
//     a price annotation are present (e.g.
//     `-10 IVV {183.07 USD} @ 197.90 USD`), upstream's documented
//     contract is that **the cost defines the balancing weight** and
//     the price is used only to insert an entry into the prices
//     database; the realized gain or loss must be recorded explicitly
//     by another posting in the same transaction.
//   - p.Price != nil (and no cost): the price annotation defines the
//     weight (per-unit `@P` -> units * P, total `@@P` -> sign(units) *
//     |P|), in the price currency. This is the FX-style conversion
//     case where the cost basis is not tracked at lot granularity.
//   - otherwise: the posting carries plain units in their commodity
//     currency.
//
// Booking has already filled the cost spec with a concrete number (or
// synthesized one — see Reducer's deferred-cost and reduction-Total
// passes) by the time PostingWeight runs as part of validation, so the
// "Cost present but TotalCost returns nil" branch is unreachable in
// practice. As a defensive measure the implementation falls through to
// Price (and then to plain units) if it ever does happen.
//
// The returned *ast.Amount is freshly allocated and its Number does
// not alias any AST field, so callers may mutate it in place (e.g. to
// negate it when computing an auto-posting residual) without
// corrupting the source AST.
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
	// Plain amount: copy the posting's units so the caller may mutate
	// without disturbing the AST.
	return &ast.Amount{Number: *ast.CloneDecimal(&p.Amount.Number), Currency: p.Amount.Currency}, nil
}

// signedAbs returns sign(units) * |val| as a freshly allocated decimal,
// used for the total-price (`@@`) branch of PostingWeight. The cost-side
// equivalent lives in package ast as part of (*Posting).TotalCost.
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
