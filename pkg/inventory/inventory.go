package inventory

import (
	"cmp"
	"iter"
	"slices"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// ReductionStep records the consumption of a single lot during a call
// to [Inventory.Reduce]. Reduce populates only Lot and Units; the
// SalePricePer / RealizedGain / GainCurrency fields are filled by the
// booking layer (see booking.go) when the reducing posting carries a
// price annotation.
//
// A cash reduction (the source Position had Cost == nil) records a
// zero-value Lot.
type ReductionStep struct {
	// Lot is the cost of the lot that was reduced; zero value for a
	// cash reduction.
	Lot Cost
	// Units is the positive magnitude consumed from this lot.
	Units apd.Decimal
	// SalePricePer is the per-unit sale price from the reducing
	// posting's price annotation; nil until the booking layer fills it.
	SalePricePer *apd.Decimal
	// RealizedGain is the realized gain or loss for this step; nil
	// until the booking layer fills it.
	RealizedGain *apd.Decimal
	// GainCurrency is the currency of RealizedGain; "" until the
	// booking layer fills it.
	GainCurrency string
}

// Inventory holds a set of [Position]s in insertion order. Positions
// of the same commodity and equal [Cost] merge on [Inventory.Add] per
// Beancount's augmentation rule. An Inventory is conceptually a value
// type but is exposed via pointer so callers can mutate it in place.
//
// The zero value of Inventory is valid and empty. Use [NewInventory]
// for an explicit constructor, or declare `var inv Inventory` and call
// methods on `&inv`.
type Inventory struct {
	positions []Position
}

// NewInventory returns a pointer to an empty [Inventory].
func NewInventory() *Inventory {
	return &Inventory{}
}

// Add merges a position into i. A position with the same commodity
// and an equal [Cost] (per [Cost.Equal]) — including the cash case
// where both Costs are nil — has its Units number added to the
// existing one in place; otherwise p is cloned and appended at the
// tail. A merge whose sum is zero drops the slot so the inventory
// does not accumulate empty placeholders.
func (i *Inventory) Add(p Position) error {
	commodity := p.Units.Currency
	for idx := range i.positions {
		existing := &i.positions[idx]
		if existing.Units.Currency != commodity {
			continue
		}
		if !costsEqualForMerge(existing.Cost, p.Cost) {
			continue
		}
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &existing.Units.Number, &p.Units.Number); err != nil {
			return Error{
				Code:    CodeInternalError,
				Message: "inventory add: " + err.Error(),
			}
		}
		existing.Units.Number.Set(&sum)
		if existing.Units.Number.Sign() == 0 {
			i.positions = slices.Delete(i.positions, idx, idx+1)
		}
		return nil
	}
	i.positions = append(i.positions, p.Clone())
	return nil
}

// costsEqualForMerge reports whether two *Cost values should merge:
// two nils merge (cash), nil-vs-non-nil never, otherwise [Cost.Equal].
func costsEqualForMerge(a, b *Cost) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(b)
}

// Reduce consumes |units.Number| of the given commodity, filtered by
// matcher, under booking method m, and returns one [ReductionStep] per
// lot consumed. Reduce mutates i in place, decrementing or removing
// the consumed positions. The sign of units does not matter; the
// magnitude is what is consumed.
//
// The booking method governs lot selection: FIFO consumes oldest
// first, LIFO newest; STRICT and DEFAULT consume oldest-first but
// only when the match is unambiguous. Consumption is greedy — each
// lot is taken in full until the requested magnitude is met, so the
// last lot may be reduced only partially.
//
// Reduce is lot-selection-only: SalePricePer / RealizedGain /
// GainCurrency on each step are left zero for the booking layer to
// fill (see booking.go).
//
// Errors:
//
//   - [CodeNoMatchingLot] — zero candidates remain after filtering by
//     commodity and matcher.
//   - [CodeAmbiguousLotMatch] — under BookingStrict or BookingDefault
//     more than one candidate remains AND the requested magnitude is
//     strictly less than the sum of candidate magnitudes (the equal
//     case is an unambiguous "total match" and proceeds normally;
//     greater-than falls through to CodeReductionExceedsInventory or,
//     for cash candidates, is left to balance assertions).
//   - [CodeReductionExceedsInventory] — magnitude exceeds the total
//     available across non-cash candidates.
//   - [CodeInvalidBookingMethod] — BookingAverage (unsupported).
//   - [CodeInternalError] — BookingNone reached here (classify should
//     have routed elsewhere) or an arithmetic error from the decimal
//     context.
func (i *Inventory) Reduce(
	units ast.Amount,
	matcher CostMatcher,
	m ast.BookingMethod,
) ([]ReductionStep, error) {
	if m == ast.BookingAverage {
		return nil, Error{
			Code:    CodeInvalidBookingMethod,
			Message: "booking method AVERAGE is not supported",
		}
	}
	if m == ast.BookingNone {
		return nil, Error{
			Code:    CodeInternalError,
			Message: "booking method NONE reached Reduce; classify should have routed this posting elsewhere",
		}
	}

	var remaining apd.Decimal
	if err := absDecimal(&remaining, &units.Number, "inventory reduce: abs units"); err != nil {
		return nil, err
	}
	if remaining.Sign() == 0 {
		return nil, nil
	}

	commodity := units.Currency

	type candidate struct {
		pos *Position
		ord int // sort tie-breaker
	}
	var candidates []candidate
	var totalAvailable apd.Decimal
	allCash := true
	for idx := range i.positions {
		p := &i.positions[idx]
		if p.Units.Currency != commodity {
			continue
		}
		var lot Cost
		if p.Cost != nil {
			lot = *p.Cost
		}
		if !matcher.Matches(lot) {
			continue
		}
		candidates = append(candidates, candidate{pos: p, ord: idx})
		if p.Cost != nil {
			allCash = false
		}
		var mag apd.Decimal
		if err := absDecimal(&mag, &p.Units.Number, "inventory reduce: abs lot units"); err != nil {
			return nil, err
		}
		if err := addDecimal(&totalAvailable, &totalAvailable, &mag, "inventory reduce: accumulate available"); err != nil {
			return nil, err
		}
	}

	if len(candidates) == 0 {
		return nil, Error{
			Code:    CodeNoMatchingLot,
			Message: "no lot in inventory matches the reducing posting",
		}
	}

	// cash overdraft → balance assertion's concern.
	if !allCash && remaining.Cmp(&totalAvailable) > 0 {
		return nil, Error{
			Code:    CodeReductionExceedsInventory,
			Message: "reducing posting requests more units than the matched lots contain",
		}
	}

	// strict/default: ambiguous iff strictly less than total available.
	if (m == ast.BookingStrict || m == ast.BookingDefault) && len(candidates) > 1 {
		if remaining.Cmp(&totalAvailable) < 0 {
			return nil, Error{
				Code:    CodeAmbiguousLotMatch,
				Message: "reducing posting matches more than one lot under STRICT booking and does not consume them all",
			}
		}
	}

	switch m {
	case ast.BookingLIFO:
		slices.SortStableFunc(candidates, func(a, b candidate) int {
			if d := lotDate(a.pos).Compare(lotDate(b.pos)); d != 0 {
				return -d
			}
			return cmp.Compare(b.ord, a.ord)
		})
	default:
		slices.SortStableFunc(candidates, func(a, b candidate) int {
			if d := lotDate(a.pos).Compare(lotDate(b.pos)); d != 0 {
				return d
			}
			return cmp.Compare(a.ord, b.ord)
		})
	}

	steps := make([]ReductionStep, 0, len(candidates))
	for _, c := range candidates {
		if remaining.Sign() == 0 {
			break
		}
		p := c.pos

		var available apd.Decimal
		if err := absDecimal(&available, &p.Units.Number, "inventory reduce: abs lot units"); err != nil {
			return nil, err
		}
		var take apd.Decimal
		if remaining.Cmp(&available) >= 0 {
			take.Set(&available)
		} else {
			take.Set(&remaining)
		}

		// |p.Units| -= take, sign-aware.
		if p.Units.Number.Sign() >= 0 {
			// long lot
			if err := subDecimal(&p.Units.Number, &p.Units.Number, &take, "inventory reduce: sub lot units"); err != nil {
				return nil, err
			}
		} else {
			// short lot
			if err := addDecimal(&p.Units.Number, &p.Units.Number, &take, "inventory reduce: add lot units"); err != nil {
				return nil, err
			}
		}
		if err := subDecimal(&remaining, &remaining, &take, "inventory reduce: sub remaining"); err != nil {
			return nil, err
		}

		var step ReductionStep
		if p.Cost != nil {
			// avoid aliasing
			step.Lot = *p.Cost.Clone()
		}
		step.Units.Set(&take)
		steps = append(steps, step)
	}

	// in reverse: deletions do not shift unvisited indices.
	for idx := len(i.positions) - 1; idx >= 0; idx-- {
		if i.positions[idx].Units.Number.Sign() == 0 {
			i.positions = slices.Delete(i.positions, idx, idx+1)
		}
	}

	return steps, nil
}

// lotDate returns the Cost.Date of p, or the zero time for a cash
// position. Used as the FIFO/LIFO sort key.
func lotDate(p *Position) time.Time {
	if p.Cost == nil {
		return time.Time{}
	}
	return p.Cost.Date
}

// Get returns a slice of positions matching the given commodity in
// their original insertion order. The returned slice is a fresh copy;
// callers may not mutate the inventory through it.
func (i *Inventory) Get(currency string) []Position {
	var out []Position
	for _, p := range i.positions {
		if p.Units.Currency == currency {
			out = append(out, p.Clone())
		}
	}
	return out
}

// Len returns the number of positions currently held.
func (i *Inventory) Len() int {
	return len(i.positions)
}

// IsEmpty reports whether the inventory holds no positions.
func (i *Inventory) IsEmpty() bool {
	return len(i.positions) == 0
}

// All returns an iterator over positions in insertion order. The
// yielded positions are clones, so consumers may retain or mutate them
// without affecting the inventory.
func (i *Inventory) All() iter.Seq[Position] {
	return func(yield func(Position) bool) {
		for _, p := range i.positions {
			if !yield(p.Clone()) {
				return
			}
		}
	}
}

// Clone returns a deep copy of i. Mutating the clone — including
// decimal coefficients and cost lots — does not affect the receiver.
func (i *Inventory) Clone() *Inventory {
	out := &Inventory{}
	if len(i.positions) == 0 {
		return out
	}
	out.positions = make([]Position, len(i.positions))
	for idx, p := range i.positions {
		out.positions[idx] = p.Clone()
	}
	return out
}

// Equal reports whether two inventories are position-for-position
// equal in insertion order. Two positions are equal when their
// commodity, units number (by value), and cost lot all match; a cash
// position and a non-cash position are never equal. A nil inventory is
// treated as empty: it equals any other empty (or nil) inventory and is
// never equal to a non-empty one.
func (i *Inventory) Equal(o *Inventory) bool {
	var ip, op []Position
	if i != nil {
		ip = i.positions
	}
	if o != nil {
		op = o.positions
	}
	if len(ip) != len(op) {
		return false
	}
	for idx := range ip {
		a := ip[idx]
		b := op[idx]
		if a.Units.Currency != b.Units.Currency {
			return false
		}
		if a.Units.Number.Cmp(&b.Units.Number) != 0 {
			return false
		}
		if !costsEqualForMerge(a.Cost, b.Cost) {
			return false
		}
	}
	return true
}
