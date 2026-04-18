package inventory

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// weightFromTotal converts a total-amount annotation (e.g. `@@` or `{{}}`)
// into a signed weight whose sign matches the units posting, so the two
// sides of the transaction cancel out. It returns a freshly allocated
// apd.Decimal that the caller may mutate.
func weightFromTotal(units, total *apd.Decimal) (*apd.Decimal, error) {
	out := new(apd.Decimal)
	abs := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(abs, total); err != nil {
		return nil, err
	}
	// apd.Decimal exposes sign via Negative; a zero value has Negative=false.
	if units.Negative {
		if _, err := apd.BaseContext.Neg(out, abs); err != nil {
			return nil, err
		}
	} else {
		out.Set(abs)
	}
	return out, nil
}

// PostingWeight computes the signed weight contributed by a posting to the
// transaction's currency sums, along with the currency in which that weight
// is denominated.
//
// An auto-posting (p.Amount == nil) returns (nil, "", nil); callers that need
// to infer the missing amount should perform their own balancing. When p.Price
// is set, the weight is converted into the price currency. When p.Cost is set
// (per-unit, total, or the combined "{per # total CUR}" form), the weight is
// converted into the cost currency.
//
// The returned *apd.Decimal is freshly allocated and does not alias any
// AST field, so callers may mutate it in place (e.g. to negate it when
// computing an auto-posting residual) without corrupting the source AST.
func PostingWeight(p *ast.Posting) (*apd.Decimal, string, error) {
	if p.Amount == nil {
		return nil, "", nil
	}
	num := p.Amount.Number
	switch {
	case p.Price != nil:
		// Per-unit price: weight = units * price.
		// Total price (@@): weight = sign(units) * price.
		priceNum := p.Price.Amount.Number
		if p.Price.IsTotal {
			out, err := weightFromTotal(&num, &priceNum)
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
	case p.Cost != nil && p.Cost.PerUnit != nil && p.Cost.Total != nil:
		// Combined form `{per # total CUR}`: weight = units*per +
		// sign(units)*total, in the shared cost currency. The lowerer
		// guarantees PerUnit.Currency == Total.Currency; we defensively
		// verify and surface a descriptive error if it is ever violated.
		// This case must come BEFORE the PerUnit-only and Total-only
		// cases below, because those use `!= nil` without excluding the
		// other field and would otherwise shadow this branch.
		if p.Cost.PerUnit.Currency != p.Cost.Total.Currency {
			return nil, "", fmt.Errorf("combined cost currencies differ: %q vs %q", p.Cost.PerUnit.Currency, p.Cost.Total.Currency)
		}
		perNum := p.Cost.PerUnit.Number
		totalNum := p.Cost.Total.Number
		perPart := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(perPart, &num, &perNum); err != nil {
			return nil, "", err
		}
		totalPart, err := weightFromTotal(&num, &totalNum)
		if err != nil {
			return nil, "", err
		}
		out := new(apd.Decimal)
		if _, err := apd.BaseContext.Add(out, perPart, totalPart); err != nil {
			return nil, "", err
		}
		return out, p.Cost.PerUnit.Currency, nil
	case p.Cost != nil && p.Cost.PerUnit != nil:
		// Cost spec with an explicit per-unit cost amount: weight = units * cost.
		costNum := p.Cost.PerUnit.Number
		out := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(out, &num, &costNum); err != nil {
			return nil, "", err
		}
		return out, p.Cost.PerUnit.Currency, nil
	case p.Cost != nil && p.Cost.Total != nil:
		// Cost spec with a total cost amount ({{...}}): weight =
		// sign(units) * total. Mirrors the total-price (@@) weight calculation.
		costNum := p.Cost.Total.Number
		out, err := weightFromTotal(&num, &costNum)
		if err != nil {
			return nil, "", err
		}
		return out, p.Cost.Total.Currency, nil
	default:
		// Plain amount: return a fresh copy of the posting's units so the
		// caller (or the balance accumulator) can safely add or negate in
		// place without mutating the AST.
		out := new(apd.Decimal)
		out.Set(&num)
		return out, p.Amount.Currency, nil
	}
}
