package inventory

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// PostingWeight computes the signed weight contributed by a posting to the
// transaction's currency sums, along with the currency in which that weight
// is denominated.
//
// An auto-posting (p.Amount == nil) returns (nil, "", nil); callers that need
// to infer the missing amount should perform their own balancing.
//
// The dispatch is:
//
//   - p.Price != nil: the price annotation defines the weight (per-unit
//     `@P` -> units * P, total `@@P` -> sign(units) * |P|). A price
//     wins over any cost spec, mirroring upstream beancount: a sale's
//     weight is the cash side, not the lot's cost basis.
//   - otherwise (*Posting).TotalCost handles the cost cases uniformly,
//     covering `{X CUR}`, `{{T CUR}}`, and `{X # T CUR}` forms. When
//     TotalCost returns nil (no Cost spec or empty spec), the weight
//     falls through to the posting's units in their commodity currency.
//
// The returned *apd.Decimal is freshly allocated and does not alias any
// AST field, so callers may mutate it in place (e.g. to negate it when
// computing an auto-posting residual) without corrupting the source AST.
func PostingWeight(p *ast.Posting) (*apd.Decimal, string, error) {
	if p.Amount == nil {
		return nil, "", nil
	}
	if p.Price != nil {
		num := p.Amount.Number
		priceNum := p.Price.Amount.Number
		if p.Price.IsTotal {
			out, err := signedAbs(&num, &priceNum)
			if err != nil {
				return nil, "", err
			}
			return out, p.Price.Amount.Currency, nil
		}
		out := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(out, &num, &priceNum); err != nil {
			return nil, "", err
		}
		return out, p.Price.Amount.Currency, nil
	}
	cost, err := p.TotalCost()
	if err != nil {
		return nil, "", err
	}
	if cost != nil {
		out := new(apd.Decimal)
		out.Set(&cost.Number)
		return out, cost.Currency, nil
	}
	// Plain amount: copy the posting's units so the caller may mutate
	// without disturbing the AST.
	out := new(apd.Decimal)
	out.Set(&p.Amount.Number)
	return out, p.Amount.Currency, nil
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
