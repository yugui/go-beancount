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

// postingWeight computes the signed weight contributed by a posting to the
// transaction's currency sums. It returns (nil, "", nil) for auto-postings
// (Amount == nil). Price or cost annotations convert the weight into the
// quote currency; in that case the returned decimal is a freshly allocated
// value, so callers may freely mutate it.
func postingWeight(p *ast.Posting) (*apd.Decimal, string, error) {
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
	case p.Cost != nil && p.Cost.Amount != nil:
		// Cost spec with an explicit per-unit (or total) cost amount.
		// Mirrors the price handling above.
		costNum := p.Cost.Amount.Number
		if p.Cost.IsTotal {
			out, err := weightFromTotal(&num, &costNum)
			if err != nil {
				return nil, "", err
			}
			return out, p.Cost.Amount.Currency, nil
		}
		out := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(out, &num, &costNum); err != nil {
			return nil, "", err
		}
		return out, p.Cost.Amount.Currency, nil
	default:
		// Plain amount: the caller must not mutate the returned pointer
		// because it aliases the AST. Copy to a fresh value so the
		// accumulator can safely add in place.
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
func txnTolerance(d *ast.Transaction, residualCurrencies []string) map[string]*apd.Decimal {
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

	out := make(map[string]*apd.Decimal, len(residualCurrencies))
	for _, cur := range residualCurrencies {
		if e, ok := minExpPerCurrency[cur]; ok {
			out[cur] = toleranceForExponent(e)
		} else {
			out[cur] = new(apd.Decimal)
		}
	}
	return out
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
		w, cur, err := postingWeight(p)
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
	tolerances := txnTolerance(d, nonZero)

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
