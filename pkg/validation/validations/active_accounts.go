package validations

import (
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// activeAccounts enforces that every directive referencing an account
// does so on a date within the account's open window. It mirrors the
// legacy checker's requireOpen dispatch: references to an account that
// was never opened emit CodeAccountNotOpen, references dated before the
// account's Open directive emit CodeAccountNotYetOpen, and references
// dated strictly after the account's Close directive emit
// CodeAccountClosed.
//
// Currency-constraint enforcement is intentionally not performed here;
// that is the job of a separate validator, matching the legacy
// requireOpen's currency check which is out of scope for this validator.
type activeAccounts struct {
	state map[ast.Account]*accountstate.State
}

// newActiveAccounts constructs an activeAccounts validator that checks
// directives against the given per-account lifecycle map. The map is
// read-only from the validator's perspective; Apply owns its lifetime.
func newActiveAccounts(state map[ast.Account]*accountstate.State) *activeAccounts {
	return &activeAccounts{state: state}
}

// Name identifies this validator for diagnostic and debugging purposes.
func (*activeAccounts) Name() string { return "active_accounts" }

// ProcessEntry dispatches by directive type, invoking check for each
// account reference in the directive. The set of directive types
// matches the legacy checker's requireOpen coverage: Transaction
// postings, Balance, Pad (both Account and PadAccount), Note, and
// Document.
func (v *activeAccounts) ProcessEntry(d ast.Directive) []api.Error {
	switch d := d.(type) {
	case *ast.Transaction:
		return v.checkTransaction(d)
	case *ast.Balance:
		if err, ok := v.check(d.Account, d.Date, d.Span); ok {
			return []api.Error{err}
		}
	case *ast.Pad:
		var errs []api.Error
		if err, ok := v.check(d.Account, d.Date, d.Span); ok {
			errs = append(errs, err)
		}
		if err, ok := v.check(d.PadAccount, d.Date, d.Span); ok {
			errs = append(errs, err)
		}
		return errs
	case *ast.Note:
		if err, ok := v.check(d.Account, d.Date, d.Span); ok {
			return []api.Error{err}
		}
	case *ast.Document:
		if err, ok := v.check(d.Account, d.Date, d.Span); ok {
			return []api.Error{err}
		}
	}
	return nil
}

// Finish has no deferred diagnostics: all checks here are per-directive.
func (*activeAccounts) Finish() []api.Error { return nil }

// checkTransaction walks a transaction's postings, emitting one error
// per posting whose account is not open at the transaction's date. The
// span falls back to the transaction's span when the posting itself has
// no recorded span, matching the legacy checkBalance behavior.
func (v *activeAccounts) checkTransaction(d *ast.Transaction) []api.Error {
	var errs []api.Error
	for i := range d.Postings {
		p := &d.Postings[i]
		span := p.Span
		if span == (ast.Span{}) {
			span = d.Span
		}
		if err, ok := v.check(p.Account, d.Date, span); ok {
			errs = append(errs, err)
		}
	}
	return errs
}

// check verifies a single (account, date) reference against the
// per-account lifecycle map. The second return is false when no error
// is produced. Message text matches legacy requireOpen verbatim for
// byte-for-byte parity.
func (v *activeAccounts) check(account ast.Account, at time.Time, span ast.Span) (api.Error, bool) {
	st, ok := v.state[account]
	if !ok {
		return api.Error{
			Code:    string(validation.CodeAccountNotOpen),
			Span:    span,
			Message: fmt.Sprintf("account %q is not open", account),
		}, true
	}
	if at.Before(st.OpenDate) {
		return api.Error{
			Code:    string(validation.CodeAccountNotYetOpen),
			Span:    span,
			Message: fmt.Sprintf("account %q is not open on %s", account, at.Format("2006-01-02")),
		}, true
	}
	if st.Closed && at.After(st.CloseDate) {
		return api.Error{
			Code:    string(validation.CodeAccountClosed),
			Span:    span,
			Message: fmt.Sprintf("account %q is closed on %s", account, at.Format("2006-01-02")),
		}, true
	}
	return api.Error{}, false
}
