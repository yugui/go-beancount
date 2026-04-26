package validations

import (
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// activeAccounts enforces that every directive referencing an account
// does so on a date within the account's open window. It mirrors
// upstream beancount's require-open dispatch: references to an account
// that was never opened emit CodeAccountNotOpen, references dated
// before the account's Open directive emit CodeAccountNotYetOpen, and
// references dated strictly after the account's Close directive emit
// CodeAccountClosed.
//
// Currency-constraint enforcement is intentionally not performed here;
// that is the job of a separate validator.
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
// matches upstream beancount's require-open coverage: Transaction
// postings, Balance, Pad (both Account and PadAccount), Note, and
// Document.
func (v *activeAccounts) ProcessEntry(d ast.Directive) []ast.Diagnostic {
	switch d := d.(type) {
	case *ast.Transaction:
		return v.checkTransaction(d)
	case *ast.Balance:
		if diag, ok := v.check(d.Account, d.Date, d.Span); ok {
			return []ast.Diagnostic{diag}
		}
	case *ast.Pad:
		var diags []ast.Diagnostic
		if diag, ok := v.check(d.Account, d.Date, d.Span); ok {
			diags = append(diags, diag)
		}
		if diag, ok := v.check(d.PadAccount, d.Date, d.Span); ok {
			diags = append(diags, diag)
		}
		return diags
	case *ast.Note:
		if diag, ok := v.check(d.Account, d.Date, d.Span); ok {
			return []ast.Diagnostic{diag}
		}
	case *ast.Document:
		if diag, ok := v.check(d.Account, d.Date, d.Span); ok {
			return []ast.Diagnostic{diag}
		}
	}
	return nil
}

// Finish has no deferred diagnostics: all checks here are per-directive.
func (*activeAccounts) Finish() []ast.Diagnostic { return nil }

// checkTransaction walks a transaction's postings, emitting one error
// per posting whose account is not open at the transaction's date. The
// span falls back to the transaction's span when the posting itself has
// no recorded span, matching upstream beancount's posting-visit
// behavior.
func (v *activeAccounts) checkTransaction(d *ast.Transaction) []ast.Diagnostic {
	var diags []ast.Diagnostic
	for i := range d.Postings {
		p := &d.Postings[i]
		span := p.Span
		if span == (ast.Span{}) {
			span = d.Span
		}
		if diag, ok := v.check(p.Account, d.Date, span); ok {
			diags = append(diags, diag)
		}
	}
	return diags
}

// check verifies a single (account, date) reference against the
// per-account lifecycle map. The second return is false when no error
// is produced. Message text matches upstream beancount's require-open
// wording verbatim for byte-for-byte parity.
func (v *activeAccounts) check(account ast.Account, at time.Time, span ast.Span) (ast.Diagnostic, bool) {
	st, ok := v.state[account]
	if !ok {
		return ast.Diagnostic{
			Code:    string(validation.CodeAccountNotOpen),
			Span:    span,
			Message: fmt.Sprintf("account %q is not open", account),
		}, true
	}
	if at.Before(st.OpenDate) {
		return ast.Diagnostic{
			Code:    string(validation.CodeAccountNotYetOpen),
			Span:    span,
			Message: fmt.Sprintf("account %q is not open on %s", account, at.Format("2006-01-02")),
		}, true
	}
	if st.Closed && at.After(st.CloseDate) {
		return ast.Diagnostic{
			Code:    string(validation.CodeAccountClosed),
			Span:    span,
			Message: fmt.Sprintf("account %q is closed on %s", account, at.Format("2006-01-02")),
		}, true
	}
	return ast.Diagnostic{}, false
}
