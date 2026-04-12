package validation

import (
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// pendingPad records a pad directive that has not yet been resolved by a
// subsequent balance assertion on its target account. The pad amount is
// computed lazily when the matching balance directive is evaluated, using
// the actual running balance at that moment.
type pendingPad struct {
	dir *ast.Pad
}

// visitPad records a pending pad for the target account. If a pending pad
// already exists for the same account, it is reported as unresolved and
// replaced by the new one, matching Beancount's behavior where consecutive
// pads without an intervening balance assertion drop the earlier pad.
func (c *checker) visitPad(d *ast.Pad) {
	c.requireOpen(d.Account, d.Date, d.Span, "")
	c.requireOpen(d.PadAccount, d.Date, d.Span, "")

	if prev, ok := c.pendingPads[d.Account]; ok {
		c.emit(Error{
			Code:    CodePadUnresolved,
			Span:    prev.dir.Span,
			Message: fmt.Sprintf("pad directive for %s from %s was not resolved before another pad", prev.dir.Account, prev.dir.PadAccount),
		})
	}
	c.pendingPads[d.Account] = &pendingPad{dir: d}
}

// resolvePendingPad applies the filler amount for a pending pad on account
// at the moment of a balance assertion for the given currency and expected
// value. It mutates the running balances so that the target account's actual
// balance for currency equals expected, and offsets the source account
// accordingly. Returns whether a pad was resolved and any internal arithmetic
// error encountered.
func (c *checker) resolvePendingPad(account, currency string, expected *apd.Decimal) (bool, error) {
	pp, ok := c.pendingPads[account]
	if !ok {
		return false, nil
	}

	key := balanceKey{Account: account, Currency: currency}
	actual := c.balances[key]
	if actual == nil {
		actual = new(apd.Decimal)
	}

	delta := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(delta, expected, actual); err != nil {
		return false, fmt.Errorf("pad %s/%s: compute delta: %w", account, currency, err)
	}
	neg := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(neg, delta); err != nil {
		return false, fmt.Errorf("pad %s/%s: negate delta: %w", account, currency, err)
	}
	if err := c.apply(pp.dir.Account, currency, delta); err != nil {
		return false, fmt.Errorf("pad %s/%s: apply to target: %w", account, currency, err)
	}
	if err := c.apply(pp.dir.PadAccount, currency, neg); err != nil {
		return false, fmt.Errorf("pad %s/%s: apply to source: %w", account, currency, err)
	}
	delete(c.pendingPads, account)
	return true, nil
}

// reportUnresolvedPads emits an error for each pad directive that was never
// matched by a subsequent balance assertion on its target account. Errors
// are emitted in a deterministic order sorted by account name.
func (c *checker) reportUnresolvedPads() {
	if len(c.pendingPads) == 0 {
		return
	}
	accounts := make([]string, 0, len(c.pendingPads))
	for a := range c.pendingPads {
		accounts = append(accounts, a)
	}
	sort.Strings(accounts)
	for _, a := range accounts {
		pp := c.pendingPads[a]
		c.emit(Error{
			Code:    CodePadUnresolved,
			Span:    pp.dir.Span,
			Message: fmt.Sprintf("pad directive for %s from %s was not followed by a matching balance assertion", pp.dir.Account, pp.dir.PadAccount),
		})
	}
	c.pendingPads = nil
}
