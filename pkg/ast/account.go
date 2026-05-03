package ast

import (
	"fmt"
	"strings"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// Account is a beancount account name, such as "Assets:Cash:JPY".
//
// A valid Account is a root component (one of Assets, Liabilities, Equity,
// Income, Expenses) followed by zero or more sub-components separated by
// ':'. Each sub-component must satisfy the beancount component grammar
// (see syntax.IsAccountComponentStart / IsAccountComponentCont).
//
// Account is defined as a named string so that literal composite-literal
// forms such as `Account: "Assets:Cash"` continue to compile. This means
// the type cannot enforce its invariant at construction time. Callers
// producing Account values outside the parser (e.g. plugins, the BQL
// processor, test fixtures whose validity is not obvious) should build
// values through the Assets/Liabilities/Equity/Income/Expenses constants
// and MustSub/Sub, which validate components against the same rules the
// scanner applies. Untyped string conversions remain legal but are
// appropriate only where the literal's validity is obvious by inspection.
type Account string

// Root account constants. These are the only permitted root components
// per the beancount grammar and are the recommended entry points for
// constructing Account values outside the parser.
const (
	Assets      Account = "Assets"
	Liabilities Account = "Liabilities"
	Equity      Account = "Equity"
	Income      Account = "Income"
	Expenses    Account = "Expenses"
)

// Sub returns a new Account with the given sub-components appended to a.
// Each component is validated against the beancount account-component
// grammar; Sub returns the empty Account and a descriptive error on the
// first invalid component. The receiver itself is not re-validated, so
// Sub may be called on an already-invalid Account to extend it; callers
// needing full-value validation should use IsValid.
//
// A call with zero components returns the receiver unchanged.
func (a Account) Sub(components ...string) (Account, error) {
	if len(components) == 0 {
		return a, nil
	}
	for _, c := range components {
		if err := validateComponent(c); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	b.Grow(len(a) + sumLen(components) + len(components))
	b.WriteString(string(a))
	for _, c := range components {
		b.WriteByte(':')
		b.WriteString(c)
	}
	return Account(b.String()), nil
}

// MustSub is like Sub but panics on an invalid component. It is intended
// for package-level constants and test fixtures where component validity
// is statically known.
func (a Account) MustSub(components ...string) Account {
	out, err := a.Sub(components...)
	if err != nil {
		panic(err)
	}
	return out
}

// Parent returns a with its last component removed. If a has only a root
// component (or is the empty Account), Parent returns the empty Account.
func (a Account) Parent() Account {
	if a == "" {
		return ""
	}
	i := strings.LastIndexByte(string(a), ':')
	if i < 0 {
		return ""
	}
	return a[:i]
}

// Root returns the root component of a. For a valid Account this is one
// of the five root constants. For an Account that already consists of
// only a root component (e.g. Assets), Root returns the receiver
// unchanged. For an empty Account, Root returns the empty Account.
func (a Account) Root() Account {
	if a == "" {
		return ""
	}
	i := strings.IndexByte(string(a), ':')
	if i < 0 {
		return a
	}
	return a[:i]
}

// Parts returns the colon-separated components of a. An empty Account
// yields a nil slice.
func (a Account) Parts() []string {
	if a == "" {
		return nil
	}
	return strings.Split(string(a), ":")
}

// IsAncestorOf reports whether a is a strict ancestor of descendant in
// the account hierarchy: descendant lies in the subtree rooted at a,
// with descendant != a. Returns false when either operand is empty.
// The check is component-aware, so "Assets:A".IsAncestorOf("Assets:Apple")
// is false even though one is a string prefix of the other.
func (a Account) IsAncestorOf(descendant Account) bool {
	if a == "" || descendant == "" {
		return false
	}
	if len(descendant) <= len(a) {
		return false
	}
	if Account(descendant[:len(a)]) != a {
		return false
	}
	return descendant[len(a)] == ':'
}

// Covers reports whether other equals a or lies in the subtree rooted
// at a. It is the natural predicate when an operation on a parent
// account should also apply to every descendant (e.g. summing balances
// over a subtree). Returns false when either account is empty.
func (a Account) Covers(other Account) bool {
	if a == "" || other == "" {
		return false
	}
	return a == other || a.IsAncestorOf(other)
}

// IsValid reports whether a satisfies the beancount account grammar:
// a valid root component followed by zero or more valid sub-components
// separated by ':'. Use IsValid at trust boundaries where an Account
// may have originated from an untyped string conversion.
func (a Account) IsValid() bool {
	if a == "" {
		return false
	}
	parts := strings.Split(string(a), ":")
	if !syntax.IsAccountRoot(parts[0]) {
		return false
	}
	for _, p := range parts[1:] {
		if !isValidComponent(p) {
			return false
		}
	}
	return true
}

// isValidComponent reports whether s is a valid account sub-component.
func isValidComponent(s string) bool {
	if s == "" {
		return false
	}
	first := true
	for _, r := range s {
		if first {
			if !syntax.IsAccountComponentStart(r) {
				return false
			}
			first = false
			continue
		}
		if !syntax.IsAccountComponentCont(r) {
			return false
		}
	}
	return true
}

// validateComponent returns a descriptive error when s is not a valid
// sub-component.
func validateComponent(s string) error {
	if s == "" {
		return fmt.Errorf("ast: empty account component")
	}
	for i, r := range s {
		if i == 0 {
			if !syntax.IsAccountComponentStart(r) {
				return fmt.Errorf("ast: invalid account component %q: illegal start rune %q", s, r)
			}
			continue
		}
		if !syntax.IsAccountComponentCont(r) {
			return fmt.Errorf("ast: invalid account component %q: illegal rune %q at offset %d", s, r, i)
		}
	}
	return nil
}

func sumLen(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}
