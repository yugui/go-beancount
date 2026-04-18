package validation

import (
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// currencySum accumulates signed per-currency totals used for transaction
// balance checking. A nil currencySum panics on write; always initialise
// with make(currencySum).
type currencySum map[string]*apd.Decimal

// add adds n to the running total for the given currency.
func (s currencySum) add(currency string, n *apd.Decimal) error {
	d, ok := s[currency]
	if !ok {
		d = new(apd.Decimal)
		s[currency] = d
	}
	_, err := apd.BaseContext.Add(d, d, n)
	return err
}

// nonZeroCurrencies returns the currencies whose running total is not
// exactly zero, sorted for deterministic reporting.
func (s currencySum) nonZeroCurrencies() []string {
	out := make([]string, 0, len(s))
	for cur, d := range s {
		if !d.IsZero() {
			out = append(out, cur)
		}
	}
	sort.Strings(out)
	return out
}

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
	// This function is used by pkg/inventory to compute auto-posting
	// residuals; keeping it as the single source of truth avoids
	// duplicating price/cost weight-calculation logic.
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

// txnTolerance derives per-currency residual tolerances for a transaction
// from the maximum precision among non-auto postings contributing to each
// currency. For each residual currency, the tolerance is half the
// least-significant digit of any posting that contributes to that currency.
// If no postings contribute to a currency (e.g. it arose from a price
// conversion), the tolerance for that currency is zero.
//
// When the ledger option `infer_tolerance_from_cost` is enabled, postings
// with an explicit cost spec additionally contribute a tolerance to their
// cost currency equal to |units| * (multiplier * 10^costExp). Per-currency
// the largest such contribution is combined with the units-based tolerance
// via maxTolerance.
func (c *checker) txnTolerance(d *ast.Transaction, residualCurrencies []string) (map[string]*apd.Decimal, error) {
	// Per-currency max precision means the smallest (most negative)
	// exponent among posting amounts in that currency. We track the
	// minimum exponent observed.
	minExpPerCurrency := make(map[string]int32)
	for i := range d.Postings {
		p := &d.Postings[i]
		if p.Amount == nil {
			continue
		}
		cur := p.Amount.Currency
		e := p.Amount.Number.Exponent
		if existing, ok := minExpPerCurrency[cur]; !ok || e < existing {
			minExpPerCurrency[cur] = e
		}
	}

	unitsTol := make(map[string]*apd.Decimal, len(residualCurrencies))
	for _, cur := range residualCurrencies {
		if e, ok := minExpPerCurrency[cur]; ok {
			unitsTol[cur] = c.toleranceForExponent(e)
		} else {
			unitsTol[cur] = new(apd.Decimal)
		}
	}

	if !c.options.Bool("infer_tolerance_from_cost") {
		return unitsTol, nil
	}

	// Second scan: per-posting cost-based contributions.
	costTol := make(map[string]*apd.Decimal)
	for i := range d.Postings {
		p := &d.Postings[i]
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
		perUnitCostTol := c.toleranceForExponent(costExp)

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
		out[cur] = maxTolerance(unitsTol[cur], costTol[cur])
		if out[cur] == nil {
			out[cur] = new(apd.Decimal)
		}
	}
	return out, nil
}

// checkBalance verifies that the postings of the transaction sum to zero per
// currency (within the derived tolerance), tolerating at most one
// auto-computed posting. On success, it also feeds per-posting weights into
// the running balance map so later balance assertions can consult them.
func (c *checker) checkBalance(d *ast.Transaction) {
	sums := make(currencySum)
	weights := make([]*apd.Decimal, len(d.Postings))
	currencies := make([]string, len(d.Postings))
	autoCount := 0
	autoIdx := -1

	for i := range d.Postings {
		p := &d.Postings[i]
		span := p.Span
		if span == (ast.Span{}) {
			span = d.Span
		}
		currency := ""
		if p.Amount != nil {
			currency = p.Amount.Currency
		}
		c.requireOpen(p.Account, d.Date, span, currency)

		if p.Amount == nil {
			autoCount++
			autoIdx = i
			continue
		}
		w, cur, err := PostingWeight(p)
		if err != nil {
			c.emit(Error{
				Code:    CodeUnbalancedTransaction,
				Span:    d.Span,
				Message: fmt.Sprintf("failed to compute posting weight: %v", err),
			})
			return
		}
		weights[i] = w
		currencies[i] = cur
		if err := sums.add(cur, w); err != nil {
			c.emit(Error{
				Code:    CodeUnbalancedTransaction,
				Span:    d.Span,
				Message: fmt.Sprintf("failed to accumulate posting weight: %v", err),
			})
			return
		}
	}

	if autoCount > 1 {
		c.emit(Error{
			Code:    CodeMultipleAutoPostings,
			Span:    d.Span,
			Message: fmt.Sprintf("transaction has %d auto-balanced postings; at most one is allowed", autoCount),
		})
		return
	}

	nonZero := sums.nonZeroCurrencies()
	tolerances, err := c.txnTolerance(d, nonZero)
	if err != nil {
		c.emit(Error{
			Code:    CodeInternalError,
			Span:    d.Span,
			Message: fmt.Sprintf("failed to derive transaction tolerance: %v", err),
		})
		return
	}

	// Filter nonZero down to currencies whose residual exceeds the tolerance.
	residual := make([]string, 0, len(nonZero))
	for _, cur := range nonZero {
		within, err := withinTolerance(sums[cur], tolerances[cur])
		if err != nil {
			c.emit(Error{
				Code:    CodeUnbalancedTransaction,
				Span:    d.Span,
				Message: fmt.Sprintf("failed to evaluate balance tolerance: %v", err),
			})
			return
		}
		if !within {
			residual = append(residual, cur)
		}
	}

	if autoCount == 1 {
		// The auto-posting must absorb at most one residual currency. We
		// ignore within-tolerance residuals in other currencies (they are
		// considered zero for balancing purposes).
		switch len(residual) {
		case 0:
			// The auto-posting is implicitly zero; nothing to infer.
			// Mark it as skipped and apply explicit posting weights.
			weights[autoIdx] = nil
			c.applyPostingWeights(d, weights, currencies)
		case 1:
			// Exactly one residual currency: the auto-posting absorbs the
			// negation of it.
			cur := residual[0]
			inferred := new(apd.Decimal)
			if _, err := apd.BaseContext.Neg(inferred, sums[cur]); err != nil {
				c.emit(Error{
					Code:    CodeUnbalancedTransaction,
					Span:    d.Span,
					Message: fmt.Sprintf("failed to infer auto-posting amount: %v", err),
				})
				return
			}
			weights[autoIdx] = inferred
			currencies[autoIdx] = cur
			c.applyPostingWeights(d, weights, currencies)
		default:
			c.emit(Error{
				Code:    CodeUnbalancedTransaction,
				Span:    d.Span,
				Message: fmt.Sprintf("cannot infer auto-posting amount: residual has %d non-zero currencies (%v)", len(residual), residual),
			})
		}
		return
	}

	// No auto-postings: residual must be within tolerance.
	if len(residual) > 0 {
		c.emit(Error{
			Code:    CodeUnbalancedTransaction,
			Span:    d.Span,
			Message: fmt.Sprintf("transaction does not balance: non-zero residual in %v", residual),
		})
		return
	}
	c.applyPostingWeights(d, weights, currencies)
}

// applyPostingWeights feeds the computed per-posting weights into the
// running balance map. Entries with a nil weight are skipped; callers use
// this to exclude an auto-posting whose inferred amount is zero.
func (c *checker) applyPostingWeights(d *ast.Transaction, weights []*apd.Decimal, currencies []string) {
	for i := range d.Postings {
		if weights[i] == nil {
			continue
		}
		// For the running balance we want the posting's effect on its own
		// account expressed in its own currency, not the weight converted
		// via a price/cost annotation. So when the posting has a literal
		// Amount, we record that amount. For auto-postings whose amount was
		// inferred, we use the inferred weight directly.
		p := &d.Postings[i]
		if p.Amount != nil {
			num := new(apd.Decimal)
			num.Set(&p.Amount.Number)
			if err := c.apply(p.Account, p.Amount.Currency, num); err != nil {
				c.emit(Error{
					Code:    CodeInternalError,
					Span:    d.Span,
					Message: fmt.Sprintf("failed to update running balance: %v", err),
				})
				return
			}
		} else {
			// Inferred auto-posting: weight and currency are the residual.
			if err := c.apply(p.Account, currencies[i], weights[i]); err != nil {
				c.emit(Error{
					Code:    CodeInternalError,
					Span:    d.Span,
					Message: fmt.Sprintf("failed to update running balance: %v", err),
				})
				return
			}
		}
	}
}
