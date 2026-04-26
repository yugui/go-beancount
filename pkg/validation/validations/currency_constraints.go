package validations

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// currencyConstraints enforces the allowed-currency list declared by an
// account's open directive. For each posting whose account has a
// non-empty Currencies list, the validator emits
// CodeCurrencyNotAllowed when the posting's currency is not in that
// list. An empty Currencies list means the account accepts any
// currency, mirroring accountstate.State.AllowsCurrency.
//
// The validator only inspects *ast.Transaction directives; no other
// directive carries a currency-bearing posting in beancount's model.
// When a posting's account is absent from the lifecycle map, this
// validator emits nothing and defers to activeAccounts, which surfaces
// the missing-open as CodeAccountNotOpen. This matches upstream
// beancount's require-open ordering, where the currency check is only
// reached once the account is confirmed to exist.
type currencyConstraints struct {
	state map[ast.Account]*accountstate.State
}

// newCurrencyConstraints constructs a currencyConstraints validator
// against the given per-account lifecycle map. The map is read-only from
// the validator's perspective.
func newCurrencyConstraints(state map[ast.Account]*accountstate.State) *currencyConstraints {
	return &currencyConstraints{state: state}
}

// Name identifies this validator for diagnostic and debugging purposes.
func (*currencyConstraints) Name() string { return "currency_constraints" }

// ProcessEntry inspects each posting of a transaction and emits one
// CodeCurrencyNotAllowed diagnostic per posting whose currency violates
// the account's open-directive allowlist. Auto-postings (Amount == nil)
// carry no currency and are skipped.
func (v *currencyConstraints) ProcessEntry(d ast.Directive) []ast.Diagnostic {
	txn, ok := d.(*ast.Transaction)
	if !ok {
		return nil
	}
	var diags []ast.Diagnostic
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			continue
		}
		st, ok := v.state[p.Account]
		if !ok {
			// Missing-open is the activeAccounts validator's job.
			continue
		}
		if st.AllowsCurrency(p.Amount.Currency) {
			continue
		}
		span := p.Span
		if span == (ast.Span{}) {
			span = txn.Span
		}
		diags = append(diags, ast.Diagnostic{
			Code:    string(validation.CodeCurrencyNotAllowed),
			Span:    span,
			Message: fmt.Sprintf("currency %q not allowed for account %q", p.Amount.Currency, p.Account),
		})
	}
	return diags
}

// Finish has no deferred diagnostics: all checks are per-directive.
func (*currencyConstraints) Finish() []ast.Diagnostic { return nil }
