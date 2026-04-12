package validation

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// State is a read-only view into the checker's running state, exposed to
// CustomAssertion implementations. It intentionally offers only inspection
// helpers; assertions must not mutate validator state.
type State struct {
	c *checker
}

// Balance returns a copy of the running balance for (account, currency), or
// nil if the bucket has never been touched. The returned pointer is safe for
// the caller to read and modify without affecting validator state.
func (s *State) Balance(account, currency string) *apd.Decimal {
	cur, ok := s.c.balances[balanceKey{Account: account, Currency: currency}]
	if !ok || cur == nil {
		return nil
	}
	out := new(apd.Decimal)
	out.Set(cur)
	return out
}

// InferTolerance returns the inferred tolerance for an amount, honouring
// the ledger's inferred_tolerance_multiplier option. The returned decimal
// is freshly allocated and safe for the caller to mutate.
func (s *State) InferTolerance(a ast.Amount) *apd.Decimal {
	return s.c.inferTolerance(a)
}

// AccountOpen reports whether the named account exists and is not closed.
func (s *State) AccountOpen(account string) bool {
	st, ok := s.c.accounts[account]
	if !ok {
		return false
	}
	return !st.closed
}

// CustomAssertion implements the logic for a single custom directive type.
// Name returns the TypeName string (the first quoted argument of the custom
// directive) the assertion handles. Evaluate runs the check and returns any
// validation errors to surface.
//
// CustomAssertion is the public extension point for plugging additional
// `custom` directive handlers into the validator. Implementations must be
// registered with RegisterCustomAssertion at package init() time so that the
// registry is fully populated before any validation runs; later registration
// is not safe for concurrent use.
type CustomAssertion interface {
	Name() string
	Evaluate(state *State, dir *ast.Custom) []Error
}

// customAssertions is the package-private registry of custom assertion
// handlers keyed by their Name(). The registry is not safe for concurrent
// registration; callers should register from init() only.
var customAssertions = map[string]CustomAssertion{}

// RegisterCustomAssertion adds a CustomAssertion to the global registry. It
// panics if an assertion with the same Name() has already been registered.
// This is intended to be called from an init() function.
func RegisterCustomAssertion(a CustomAssertion) {
	name := a.Name()
	if _, exists := customAssertions[name]; exists {
		panic(fmt.Sprintf("validation: custom assertion %q already registered", name))
	}
	customAssertions[name] = a
}

// assertHandler is the built-in handler for `custom "assert" Account Amount`.
// It compares the running balance of the given account/currency to the
// provided amount using the same tolerance rule as balance assertions.
type assertHandler struct{}

// Name returns the custom directive type handled by this assertion.
func (assertHandler) Name() string { return "assert" }

// Evaluate checks that the running balance for the referenced account and
// currency matches the provided amount within the inferred tolerance.
func (assertHandler) Evaluate(state *State, d *ast.Custom) []Error {
	if len(d.Values) != 2 {
		return []Error{{
			Code: CodeCustomAssertionFailed,
			Span: d.Span,
			Message: fmt.Sprintf(
				`custom "assert" expects 2 values (account, amount), got %d`,
				len(d.Values),
			),
		}}
	}
	accountVal := d.Values[0]
	amountVal := d.Values[1]
	if accountVal.Kind != ast.MetaAccount {
		return []Error{{
			Code:    CodeCustomAssertionFailed,
			Span:    d.Span,
			Message: `custom "assert" expects the first value to be an account`,
		}}
	}
	if amountVal.Kind != ast.MetaAmount {
		return []Error{{
			Code:    CodeCustomAssertionFailed,
			Span:    d.Span,
			Message: `custom "assert" expects the second value to be an amount`,
		}}
	}
	account := accountVal.String
	amount := amountVal.Amount

	if !state.AccountOpen(account) {
		return []Error{{
			Code: CodeCustomAssertionFailed,
			Span: d.Span,
			Message: fmt.Sprintf(
				`custom "assert": account %q is not open`,
				account,
			),
		}}
	}

	actual := state.Balance(account, amount.Currency)
	if actual == nil {
		actual = new(apd.Decimal)
	}
	expCopy := amount.Number
	expected := &expCopy

	diff := new(apd.Decimal)
	if _, err := apd.BaseContext.Sub(diff, expected, actual); err != nil {
		return []Error{{
			Code:    CodeCustomAssertionFailed,
			Span:    d.Span,
			Message: fmt.Sprintf("failed to compute balance difference: %v", err),
		}}
	}
	tolerance := state.InferTolerance(amount)
	ok, err := withinTolerance(diff, tolerance)
	if err != nil {
		return []Error{{
			Code:    CodeCustomAssertionFailed,
			Span:    d.Span,
			Message: fmt.Sprintf("failed to evaluate assertion tolerance: %v", err),
		}}
	}
	if ok {
		return nil
	}
	return []Error{{
		Code: CodeCustomAssertionFailed,
		Span: d.Span,
		Message: fmt.Sprintf(
			`custom "assert" failed: account %q: expected %s %s, got %s %s`,
			account,
			expected.Text('f'),
			amount.Currency,
			actual.Text('f'),
			amount.Currency,
		),
	}}
}

func init() {
	RegisterCustomAssertion(assertHandler{})
}
