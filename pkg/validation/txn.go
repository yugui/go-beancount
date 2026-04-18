package validation

import (
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
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
		w, cur, err := inventory.PostingWeight(p)
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
