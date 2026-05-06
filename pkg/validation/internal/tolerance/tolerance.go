package tolerance

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
)

// forExponent returns the inferred tolerance for a value whose
// least-significant digit sits at exponent e. The result is
// `inferred_tolerance_multiplier × 10^e`; at the default multiplier 0.5
// this yields 0.005 for e=-2 and 0.5 for e=0.
func forExponent(opts *options.Values, e int32) *apd.Decimal {
	mult := opts.Decimal("inferred_tolerance_multiplier")
	out := new(apd.Decimal)
	out.Set(mult)
	// Shift the exponent directly; no rounding or clamping is needed.
	out.Exponent += e
	return out
}

// ForAmount returns the default Beancount tolerance for an amount based
// on the precision of its least-significant digit and the ledger's
// configured inferred_tolerance_multiplier.
func ForAmount(opts *options.Values, amount ast.Amount) *apd.Decimal {
	return forExponent(opts, amount.Number.Exponent)
}

// ForBalanceAssertion returns the inferred tolerance for a Balance
// directive's asserted amount, mirroring upstream beancount's
// get_balance_tolerance (beancount/ops/balance.py). The tolerance is
// 2 × inferred_tolerance_multiplier × 10^expo where expo is the
// exponent of the assertion amount's least-significant digit.
// Upstream applies the doubled factor specifically to balance
// assertions because users hand-write the asserted amount and
// rounding noise can exceed transaction-internal precision;
// transaction-balancing tolerance computed from the same amount
// remains the un-doubled inferred_tolerance_multiplier × 10^expo via
// ForAmount.
func ForBalanceAssertion(opts *options.Values, amount ast.Amount) *apd.Decimal {
	out := ForAmount(opts, amount)
	// apd.Decimal stores sign separately and Coeff is a non-negative
	// big.Int, so left-shifting the coefficient by 1 multiplies the
	// magnitude by 2 without an arithmetic context round-trip.
	out.Coeff.Lsh(&out.Coeff, 1)
	return out
}

// Infer returns a per-currency residual tolerance map keyed by the
// entries in residualCurrencies. For each such currency it computes
// the units-based tolerance from posting precision scaled by the
// inferred_tolerance_multiplier option. When the
// infer_tolerance_from_cost option is enabled, cost-based
// contributions are also included. See the package doc for the full
// derivation.
func Infer(postings []ast.Posting, opts *options.Values, residualCurrencies []string) (map[string]*apd.Decimal, error) {
	// Units-based tolerance per currency: multiplier × 10^maxExp where
	// maxExp is the *largest* (least negative) exponent observed among
	// posting amounts in that currency. This matches upstream beancount's
	// infer_tolerances called with mode="max" — the mode it uses for
	// transaction balance verification (see beancount/parser/booking_full.py).
	// Upstream's docstring on the choice: "the tolerance has a dual purpose:
	// it's used to infer the resolution for interpolation (where we might
	// want the min()) and also for balance checks (where we favor the
	// looser/larger tolerance)."
	//
	// Concretely, a transaction mixing a coarsely-written posting (e.g.
	// 100.1111 JPY, exp=-4) with a high-precision interpolation residue
	// (e.g. 200.22222222222222 JPY, exp=-14) gets a JPY tolerance of
	// 0.5×10⁻⁴, dominated by the coarse posting. The high-precision
	// posting's exponent does NOT tighten the tolerance, since balance
	// checks should not penalize the user for arithmetic precision they
	// did not author.
	maxExpPerCurrency := make(map[string]int32)
	for i := range postings {
		p := &postings[i]
		// tolerance is an internal helper; the public validators that call it
		// have already emitted CodeAutoPostingUnresolved for any nil-Amount
		// posting. Silent skip avoids duplicating the diagnostic.
		if p.Amount == nil {
			continue
		}
		cur := p.Amount.Currency
		e := p.Amount.Number.Exponent
		if existing, ok := maxExpPerCurrency[cur]; !ok || e > existing {
			maxExpPerCurrency[cur] = e
		}
	}

	unitsTol := make(map[string]*apd.Decimal, len(residualCurrencies))
	for _, cur := range residualCurrencies {
		if e, ok := maxExpPerCurrency[cur]; ok {
			unitsTol[cur] = forExponent(opts, e)
		} else {
			unitsTol[cur] = new(apd.Decimal)
		}
	}

	if !opts.Bool("infer_tolerance_from_cost") {
		return unitsTol, nil
	}

	// Cost-tolerance accumulation: this branch deliberately diverges from
	// upstream beancount. Both designs bound a real source of imprecision,
	// but they bound *different* ones:
	//
	//   Upstream's model (beancount/core/interpolate.py infer_tolerances):
	//     contribution = (multiplier × 10^unitsExp) × cost_number
	//     accumulation = sum across postings within a cost currency
	//   This bounds units-number imprecision propagated through cost.
	//   Concretely, "1 GOOG {2.00 USD}" treats the integer units as
	//   "1 ± 0.5" and hence ±(0.5 × 2.00) = ±1 USD of cost-side slop.
	//   The sum then bounds the worst-case adversarial alignment of those
	//   per-posting slops. This is permissive for integer unit counts
	//   against high-magnitude cost numbers, which is one reason upstream
	//   gates the whole branch behind infer_tolerance_from_cost=false by
	//   default.
	//
	//   Our model (below):
	//     contribution = |units| × (multiplier × 10^costExp)
	//     accumulation = max across postings within a cost currency
	//   This bounds cost-number imprecision propagated through units.
	//   Concretely, "10 STOCK {2.00 USD}" treats 2.00 as "2 ± 0.005"
	//   per unit and hence ±(10 × 0.005) = ±0.05 USD of cost-side slop.
	//   The max keeps the bound tied to the single most-impactful posting
	//   rather than summing speculative slops.
	//
	// Both are partial: a complete bound would include both terms (they
	// are independent error sources that can co-occur). We keep our own
	// model intentionally — it tracks the imprecision actually authored
	// by the user (the digits they wrote in the cost number) and tends
	// to produce more proportionate tolerances for typical "integer
	// units, fractional cost" ledger styles. The known weakness is that
	// for the {{ total CUR }} form the |units| factor over-inflates the
	// bound (the imprecision in a *total* cost should not be multiplied
	// by units count); revisit if a real ledger encounters that regime.
	costTol := make(map[string]*apd.Decimal)
	for i := range postings {
		p := &postings[i]
		if p.Amount == nil || p.Cost == nil {
			continue
		}
		// Pick the cost component(s) present. For the combined
		// "{per # total CUR}" form, the residual can pick up imprecision
		// from either component, so we use the more precise (more
		// negative) exponent. The lowerer guarantees both components
		// share a currency in the combined case.
		var costCur string
		var costExp int32
		switch {
		case p.Cost.PerUnit != nil && p.Cost.Total != nil:
			costCur = p.Cost.PerUnit.Currency
			costExp = p.Cost.PerUnit.Number.Exponent
			if te := p.Cost.Total.Number.Exponent; te < costExp {
				costExp = te
			}
		case p.Cost.PerUnit != nil:
			costCur = p.Cost.PerUnit.Currency
			costExp = p.Cost.PerUnit.Number.Exponent
		case p.Cost.Total != nil:
			costCur = p.Cost.Total.Currency
			costExp = p.Cost.Total.Number.Exponent
		default:
			continue
		}
		perUnitCostTol := forExponent(opts, costExp)

		absUnits := new(apd.Decimal)
		unitsNum := p.Amount.Number
		if _, err := apd.BaseContext.Abs(absUnits, &unitsNum); err != nil {
			return nil, fmt.Errorf("abs units: %w", err)
		}

		contribution := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(contribution, absUnits, perUnitCostTol); err != nil {
			return nil, fmt.Errorf("mul cost tolerance: %w", err)
		}

		if existing, ok := costTol[costCur]; !ok || contribution.Cmp(existing) > 0 {
			costTol[costCur] = contribution
		}
	}

	out := make(map[string]*apd.Decimal, len(residualCurrencies))
	for _, cur := range residualCurrencies {
		out[cur] = maxDecimal(unitsTol[cur], costTol[cur])
		if out[cur] == nil {
			out[cur] = new(apd.Decimal)
		}
	}
	return out, nil
}

// Within reports whether |diff| <= tol. The bool answers the tolerance
// question; the error is non-nil only when apd.BaseContext.Abs fails on
// pathological input (e.g. an overflowing exponent), not when the
// tolerance is exceeded.
func Within(diff, tol *apd.Decimal) (bool, error) {
	abs := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(abs, diff); err != nil {
		return false, err
	}
	return abs.Cmp(tol) <= 0, nil
}

// maxDecimal returns the larger of a and b. Both are assumed to be
// non-negative. A nil value is treated as zero.
func maxDecimal(a, b *apd.Decimal) *apd.Decimal {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}
