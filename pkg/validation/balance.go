package validation

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// balanceKey identifies a running balance bucket by (account, currency).
type balanceKey struct {
	Account  string
	Currency string
}

// apply adds delta to the running balance for (account, currency). A nil or
// zero delta is treated as a no-op. The returned error surfaces arithmetic
// failures from the underlying apd context.
func (c *checker) apply(account, currency string, delta *apd.Decimal) error {
	if delta == nil {
		return nil
	}
	key := balanceKey{Account: account, Currency: currency}
	cur, ok := c.balances[key]
	if !ok {
		cur = new(apd.Decimal)
		c.balances[key] = cur
	}
	_, err := apd.BaseContext.Add(cur, cur, delta)
	return err
}

// toleranceForExponent returns half the unit at the given decimal exponent,
// i.e. a decimal with coefficient 5 and exponent e-1. For e=-2 this yields
// 0.005; for e=0 it yields 0.5.
func toleranceForExponent(e int32) *apd.Decimal {
	tol := new(apd.Decimal)
	tol.Coeff.SetInt64(5)
	tol.Exponent = e - 1
	return tol
}

// inferTolerance returns the default Beancount tolerance for an amount: half
// the precision of the amount's least-significant digit. For example, an
// amount written as "100.00" (exponent -2) yields 0.005, while an integer
// amount "100" (exponent 0) yields 0.5.
func inferTolerance(amount ast.Amount) *apd.Decimal {
	return toleranceForExponent(amount.Number.Exponent)
}

// maxTolerance returns the larger of a and b. Both are assumed to be
// non-negative. A nil value is treated as zero.
func maxTolerance(a, b *apd.Decimal) *apd.Decimal {
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

// absDecimal returns |x| as a freshly allocated decimal.
func absDecimal(x *apd.Decimal) (*apd.Decimal, error) {
	out := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(out, x); err != nil {
		return nil, err
	}
	return out, nil
}

// withinTolerance reports whether |diff| <= tolerance.
func withinTolerance(diff, tolerance *apd.Decimal) (bool, error) {
	abs, err := absDecimal(diff)
	if err != nil {
		return false, err
	}
	return abs.Cmp(tolerance) <= 0, nil
}

// visitBalance verifies the asserted account is open on the balance date and
// that the running balance for (account, currency) matches the assertion
// within the applicable tolerance.
func (c *checker) visitBalance(d *ast.Balance) {
	c.requireOpen(d.Account, d.Date, d.Span, d.Amount.Currency)

	expCopy := d.Amount.Number
	expected := &expCopy
	if _, err := c.resolvePendingPad(d.Account, d.Amount.Currency, expected); err != nil {
		c.emit(Error{
			Code:    CodeInternalError,
			Span:    d.Span,
			Message: fmt.Sprintf("failed to resolve pad for %q: %v", d.Account, err),
		})
		return
	}

	key := balanceKey{Account: d.Account, Currency: d.Amount.Currency}
	actual := c.balances[key]
	if actual == nil {
		actual = new(apd.Decimal)
	}

	diff := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(diff, expected, actual); err != nil {
		c.emit(Error{
			Code:    CodeBalanceMismatch,
			Span:    d.Span,
			Message: fmt.Sprintf("failed to compute balance difference: %v", err),
		})
		return
	}

	var tolerance *apd.Decimal
	if d.Tolerance != nil {
		tolerance = new(apd.Decimal)
		if _, err := apd.BaseContext.Abs(tolerance, &d.Tolerance.Number); err != nil {
			c.emit(Error{
				Code:    CodeBalanceMismatch,
				Span:    d.Span,
				Message: fmt.Sprintf("failed to normalize tolerance: %v", err),
			})
			return
		}
	} else {
		tolerance = inferTolerance(d.Amount)
	}

	ok, err := withinTolerance(diff, tolerance)
	if err != nil {
		c.emit(Error{
			Code:    CodeBalanceMismatch,
			Span:    d.Span,
			Message: fmt.Sprintf("failed to evaluate balance tolerance: %v", err),
		})
		return
	}
	if !ok {
		c.emit(Error{
			Code: CodeBalanceMismatch,
			Span: d.Span,
			Message: fmt.Sprintf(
				"balance assertion failed: account %s: expected %s %s, got %s %s",
				d.Account,
				expected.Text('f'),
				d.Amount.Currency,
				actual.Text('f'),
				d.Amount.Currency,
			),
		})
	}
}
