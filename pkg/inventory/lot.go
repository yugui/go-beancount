package inventory

import (
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Lot identifies an inventory lot by its booked per-unit cost,
// currency, acquisition date, and optional label. Unlike [ast.Cost],
// Lot carries no presentation provenance, so inventory-tier code
// cannot propagate surcharge-form literals into a reducing posting's
// weight.
type Lot struct {
	Number   apd.Decimal
	Currency string
	Date     time.Time
	Label    string
}

// Equal reports whether two lots identify the same position: same
// Number (by value), Currency, Date (via [time.Time.Equal]), and
// Label. Nil-safe at both arguments; two nils compare equal.
func (l *Lot) Equal(o *Lot) bool {
	if l == nil || o == nil {
		return l == o
	}
	if l.Currency != o.Currency || l.Label != o.Label {
		return false
	}
	if !l.Date.Equal(o.Date) {
		return false
	}
	return l.Number.Cmp(&o.Number) == 0
}

// Clone returns a deep copy of l, or nil if l is nil. The clone owns
// its Number coefficient buffer.
func (l *Lot) Clone() *Lot {
	if l == nil {
		return nil
	}
	return &Lot{
		Number:   *ast.CloneDecimal(&l.Number),
		Currency: l.Currency,
		Date:     l.Date,
		Label:    l.Label,
	}
}

// LotFromCost extracts the provenance-free identity from an AST cost,
// discarding PerUnit and Total. Returns nil for a nil input. The
// returned Lot owns its Number coefficient buffer.
func LotFromCost(c *ast.Cost) *Lot {
	if c == nil {
		return nil
	}
	return &Lot{
		Number:   *ast.CloneDecimal(&c.Number),
		Currency: c.Currency,
		Date:     c.Date,
		Label:    c.Label,
	}
}

// ToCost rebuilds a booked [ast.Cost] from a Lot's identity. PerUnit
// and Total on the returned value are always nil. The returned value
// owns its Number coefficient buffer. Returns nil for a nil receiver.
func (l *Lot) ToCost() *ast.Cost {
	if l == nil {
		return nil
	}
	return &ast.Cost{
		Number:   *ast.CloneDecimal(&l.Number),
		Currency: l.Currency,
		Date:     l.Date,
		Label:    l.Label,
	}
}
