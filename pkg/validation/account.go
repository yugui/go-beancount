package validation

import (
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// accountState tracks the open/close lifecycle of a single account.
type accountState struct {
	openSpan   ast.Span
	openDate   time.Time
	closed     bool
	closeDate  time.Time
	closeSpan  ast.Span
	currencies []string
	booking    ast.BookingMethod
}

// allowsCurrency reports whether the given currency is permitted by this
// account's open directive. An empty currencies list means "any currency".
func (s *accountState) allowsCurrency(currency string) bool {
	if len(s.currencies) == 0 {
		return true
	}
	for _, c := range s.currencies {
		if c == currency {
			return true
		}
	}
	return false
}

// visitOpen records a new account open, or emits a duplicate-open error if
// the account has already been opened.
func (c *checker) visitOpen(d *ast.Open) {
	if _, ok := c.accounts[d.Account]; ok {
		c.emit(Error{
			Code:    CodeDuplicateOpen,
			Span:    d.Span,
			Message: fmt.Sprintf("account %q already opened", d.Account),
		})
		return
	}
	c.accounts[d.Account] = &accountState{
		openSpan:   d.Span,
		openDate:   d.Date,
		currencies: d.Currencies,
		booking:    d.Booking,
	}
}

// visitClose records the closure of an account, or emits errors if the
// account was never opened or has already been closed.
func (c *checker) visitClose(d *ast.Close) {
	st, ok := c.accounts[d.Account]
	if !ok {
		c.emit(Error{
			Code:    CodeAccountNotOpen,
			Span:    d.Span,
			Message: fmt.Sprintf("cannot close account %q: not open", d.Account),
		})
		return
	}
	if st.closed {
		c.emit(Error{
			Code:    CodeAccountClosed,
			Span:    d.Span,
			Message: fmt.Sprintf("account %q already closed", d.Account),
		})
		return
	}
	st.closed = true
	st.closeDate = d.Date
	st.closeSpan = d.Span
}

// requireOpen verifies that an account is open at the given date and, if a
// currency is supplied, that the currency is allowed by the account. It
// emits an error for each violation found.
func (c *checker) requireOpen(account ast.Account, at time.Time, span ast.Span, currency string) {
	st, ok := c.accounts[account]
	if !ok {
		c.emit(Error{
			Code:    CodeAccountNotOpen,
			Span:    span,
			Message: fmt.Sprintf("account %q is not open", account),
		})
		return
	}
	if at.Before(st.openDate) {
		c.emit(Error{
			Code:    CodeAccountNotYetOpen,
			Span:    span,
			Message: fmt.Sprintf("account %q is not open on %s", account, at.Format("2006-01-02")),
		})
		// date before open implies not-yet-open; no further checks needed
		return
	}
	if st.closed && at.After(st.closeDate) {
		c.emit(Error{
			Code:    CodeAccountClosed,
			Span:    span,
			Message: fmt.Sprintf("account %q is closed on %s", account, at.Format("2006-01-02")),
		})
		return
	}
	if currency != "" && !st.allowsCurrency(currency) {
		c.emit(Error{
			Code:    CodeCurrencyNotAllowed,
			Span:    span,
			Message: fmt.Sprintf("currency %q not allowed for account %q", currency, account),
		})
	}
}
