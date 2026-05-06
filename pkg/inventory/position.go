package inventory

import (
	"github.com/yugui/go-beancount/pkg/ast"
)

// Position is a single holding in an inventory: a signed unit amount
// paired with an optional lot [Cost]. A nil Cost represents cash or a
// fungible commodity that does not require lot bookkeeping; a non-nil
// Cost binds the position to a specific acquisition lot.
type Position struct {
	Units ast.Amount
	Cost  *Cost
}

// Clone returns a deep copy of p. The Units amount is deep-copied via
// [(*ast.Amount).Clone] and the Cost via [(*Cost).Clone]; both are
// nil-safe.
func (p Position) Clone() Position {
	return Position{
		Units: *p.Units.Clone(),
		Cost:  p.Cost.Clone(),
	}
}

// Commodity returns the currency of the position's units. It is a
// convenience alias for p.Units.Currency.
func (p Position) Commodity() string {
	return p.Units.Currency
}
