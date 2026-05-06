package ast

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// TotalCost computes the cost-currency contribution of this posting,
// resolving CostSpec.PerUnit and CostSpec.Total uniformly so callers do
// not need to branch on which field is populated.
//
// The result is signed: the sign of p.Amount.Number propagates so the
// returned weight cancels against the per-currency totals of a balanced
// transaction. The mapping is:
//
//   - p.Amount == nil (auto-posting): (nil, nil).
//   - p.Cost == nil, or p.Cost has neither PerUnit nor Total: (nil, nil).
//     Callers treat this as "no cost contribution" and fall back to
//     units in the posting's commodity currency.
//   - PerUnit only: units * PerUnit, in PerUnit.Currency.
//   - Total only: sign(units) * |Total|, in Total.Currency. Mirrors the
//     "{{T CUR}}" balance rule; the formulation is exact (no division)
//     so values like {{1 JPY}} on 3 STOCK round-trip without precision
//     loss.
//   - Both set ({X # T CUR}): units*PerUnit + sign(units)*|Total|, in
//     the shared cost currency. Returns an error if the PerUnit and
//     Total currencies disagree (lowering enforces equality; the check
//     is defensive).
//
// The returned Amount is freshly allocated; the caller may mutate its
// fields without affecting the receiver.
func (p *Posting) TotalCost() (*Amount, error) {
	if p == nil || p.Amount == nil || p.Cost == nil {
		return nil, nil
	}
	if p.Cost.PerUnit == nil && p.Cost.Total == nil {
		return nil, nil
	}
	units := &p.Amount.Number
	switch {
	case p.Cost.PerUnit != nil && p.Cost.Total != nil:
		if p.Cost.PerUnit.Currency != p.Cost.Total.Currency {
			return nil, fmt.Errorf(
				"combined cost currencies differ: %q vs %q",
				p.Cost.PerUnit.Currency, p.Cost.Total.Currency)
		}
		perPart := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(perPart, units, &p.Cost.PerUnit.Number); err != nil {
			return nil, err
		}
		totalPart, err := signedAbs(units, &p.Cost.Total.Number)
		if err != nil {
			return nil, err
		}
		out := Amount{Currency: p.Cost.PerUnit.Currency}
		if _, err := apd.BaseContext.Add(&out.Number, perPart, totalPart); err != nil {
			return nil, err
		}
		return &out, nil
	case p.Cost.PerUnit != nil:
		out := Amount{Currency: p.Cost.PerUnit.Currency}
		if _, err := apd.BaseContext.Mul(&out.Number, units, &p.Cost.PerUnit.Number); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		signed, err := signedAbs(units, &p.Cost.Total.Number)
		if err != nil {
			return nil, err
		}
		out := Amount{Currency: p.Cost.Total.Currency}
		out.Number.Set(signed)
		return &out, nil
	}
}

// signedAbs returns sign(units) * |val| as a freshly allocated
// decimal. Used by both the Total-only and combined branches of
// (*Posting).TotalCost so the same exact (division-free) formulation
// reaches both code paths. The same name and shape live in
// pkg/inventory for the price-side equivalent.
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
