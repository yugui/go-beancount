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

// isZero reports whether every accumulated currency is exactly zero.
func (s currencySum) isZero() bool {
	for _, d := range s {
		if !d.IsZero() {
			return false
		}
	}
	return true
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

// checkBalance verifies that the postings of the transaction sum to zero per
// currency, tolerating at most one auto-computed posting. It also performs
// the per-posting account lifecycle checks required of every transaction.
func (c *checker) checkBalance(d *ast.Transaction) {
	sums := make(currencySum)
	autoCount := 0

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
			continue
		}
		w, cur, err := postingWeight(p)
		if err != nil {
			// Arithmetic failure is unexpected for well-formed decimals;
			// surface it as an unbalanced error rather than panicking.
			c.emit(Error{
				Code:    CodeUnbalancedTransaction,
				Span:    d.Span,
				Message: fmt.Sprintf("failed to compute posting weight: %v", err),
			})
			return
		}
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

	if autoCount == 1 {
		switch len(nonZero) {
		case 0:
			// The auto-posting is implicitly zero; nothing to infer.
		case 1:
			// Exactly one residual currency: the auto-posting is the
			// negation of it. Step 5 will compute the inferred amount from
			// the residual when it consumes postings for running balances.
		default:
			c.emit(Error{
				Code:    CodeUnbalancedTransaction,
				Span:    d.Span,
				Message: fmt.Sprintf("cannot infer auto-posting amount: residual has %d non-zero currencies (%v)", len(nonZero), nonZero),
			})
		}
		return
	}

	// No auto-postings: residual must be exactly zero.
	if len(nonZero) > 0 {
		c.emit(Error{
			Code:    CodeUnbalancedTransaction,
			Span:    d.Span,
			Message: fmt.Sprintf("transaction does not balance: non-zero residual in %v", nonZero),
		})
	}
}
