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

// bookedPostingWeight computes the signed weight a [BookedPosting]
// contributes to the transaction's residual, along with the currency.
//
// Unlike [PostingWeight] which reads the AST's cost spec verbatim,
// this helper consults the booking outcome so partial cost specs
// (date-only, `{}`) get their resolved per-lot cost rather than a
// fallback unit weight. A `@` or `@@` price annotation on the source
// posting still wins, mirroring upstream beancount's rule that the
// price defines the weight when present (the cost basis is then a
// realized-gain concern, not a balance concern):
//
//   - p.Price != nil: defer to [PostingWeight], which handles the
//     per-unit and total-price branches.
//   - reduction with no price (len(bp.Reductions) > 0): return
//     sign(bp.Units.Number) * Σ step.Units * step.Lot.Number, in the
//     lot's cost currency. Correct even when the AST's cost spec is
//     partial because the matcher already resolved the lot cost.
//   - augmentation with a resolved lot (bp.Lot != nil) and no price:
//     return bp.Units.Number * bp.Lot.Number in the lot's cost
//     currency.
//   - everything else (cash augmentation, no price, no cost): defer
//     to [PostingWeight].
//
// The returned *apd.Decimal is freshly allocated and does not alias
// any AST or BookedPosting field.
func bookedPostingWeight(bp BookedPosting) (*apd.Decimal, string, error) {
	if bp.Source != nil && bp.Source.Price != nil {
		return PostingWeight(bp.Source)
	}
	switch {
	case len(bp.Reductions) > 0:
		// Sum |step.Units| * step.Lot.Number across steps; flip sign
		// to match the units' sign so the weight cancels correctly in
		// the residual.
		out := new(apd.Decimal)
		currency := bp.Reductions[0].Lot.Currency
		for i := range bp.Reductions {
			s := &bp.Reductions[i]
			if s.Lot.Currency != "" && s.Lot.Currency != currency {
				return nil, "", fmt.Errorf("booked reduction has mixed cost currencies: %q vs %q", currency, s.Lot.Currency)
			}
			part := new(apd.Decimal)
			lotNum := s.Lot.Number
			if _, err := apd.BaseContext.Mul(part, &s.Units, &lotNum); err != nil {
				return nil, "", err
			}
			if _, err := apd.BaseContext.Add(out, out, part); err != nil {
				return nil, "", err
			}
		}
		// step.Units carries a positive magnitude per the reducer
		// contract; flip the sign of the sum if the source units were
		// negative.
		if bp.Units.Number.Negative {
			if _, err := apd.BaseContext.Neg(out, out); err != nil {
				return nil, "", err
			}
		}
		// A cash reduction (Lot.Currency == "") has no cost weight;
		// fall back to the unit weight in the commodity currency.
		// Cash lots carry a zero Lot.Number, so out has accumulated
		// to zero by here and Set safely overwrites it.
		if currency == "" {
			out.Set(&bp.Units.Number)
			return out, bp.Units.Currency, nil
		}
		return out, currency, nil
	case bp.Lot != nil:
		out := new(apd.Decimal)
		lotNum := bp.Lot.Number
		if _, err := apd.BaseContext.Mul(out, &bp.Units.Number, &lotNum); err != nil {
			return nil, "", err
		}
		return out, bp.Lot.Currency, nil
	default:
		// Cash or price-annotated augmentation: the AST already carries
		// the right shape for PostingWeight to handle.
		return PostingWeight(bp.Source)
	}
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
