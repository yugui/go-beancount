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
// to [Inventory.Reduce]. Only Lot and Units are populated by Reduce:
// the lot that was consumed, and the positive magnitude consumed from
// it. SalePricePer, RealizedGain, and GainCurrency are deliberately
// left zero — they are populated by the booking layer in this package
// (see booking.go) when the reducing posting carries a price
// annotation and a realized gain must be computed. Keeping Reduce
// lot-selection-only avoids coupling the inventory layer to the
// per-transaction booking rules.
//
// Note on cash lots: an [Inventory] stores a cash position as a
// [Position] with a nil Cost pointer. A ReductionStep cannot carry a
// nil lot, so Lot is a value and cash reductions record a zero-value
// Cost (empty currency/label, zero date, zero number). Callers should
// interpret a zero Lot as "this step consumed from a cash position".
type ReductionStep struct {
	// Lot is the cost of the lot that was reduced. For cash positions
	// (originally stored with Cost == nil) Lot is the zero value.
	Lot Cost
	// Units is the positive magnitude consumed from this lot. Always
	// non-negative; the sign of the reducing posting's units does not
	// propagate here.
	Units apd.Decimal
	// SalePricePer is the per-unit sale price from the reducing
	// posting's price annotation, or nil if the step has not been
	// enriched by the booking layer. [Inventory.Reduce] always leaves
	// this nil.
	SalePricePer *apd.Decimal
	// RealizedGain is the realized gain or loss for this step, or nil
	// if unset. [Inventory.Reduce] always leaves this nil.
	RealizedGain *apd.Decimal
	// GainCurrency is the currency in which RealizedGain is expressed,
	// or "" if unset. [Inventory.Reduce] always leaves this empty.
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

// Add merges a position into i. If an existing position has the same
// commodity and an equal [Cost] (per [Cost.Equal]) — including the
// cash case where both have Cost == nil — the new Units number is
// added to the existing one. Otherwise p is appended at the end of
// the insertion order, after being cloned so the caller retains
// ownership of the source.
//
// Add preserves insertion order: merges happen in place at the existing
// slot, and new lots always append at the tail. The one exception is when
// merging produces a zero-units position; that slot is dropped so the
// inventory does not accumulate empty placeholders, and entries after it
// shift up by one.
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
		// Augmentation merge: sum the numbers in place.
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &existing.Units.Number, &p.Units.Number); err != nil {
			return Error{
				Code:    CodeInternalError,
				Message: "inventory add: " + err.Error(),
			}
		}
		existing.Units.Number.Set(&sum)
		// If the sum is zero, drop the merged position entirely so the
		// inventory does not accumulate empty slots.
		if existing.Units.Number.Sign() == 0 {
			i.positions = slices.Delete(i.positions, idx, idx+1)
		}
		return nil
	}
	// No merge target: append a clone.
	i.positions = append(i.positions, p.Clone())
	return nil
}

// costsEqualForMerge returns true if two *Cost values should merge.
// Two nil costs merge (cash case); a nil and a non-nil never merge; two
// non-nil costs merge iff Cost.Equal says so.
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
// the consumed positions.
//
// The booking method governs lot selection: FIFO consumes the oldest
// lots first, LIFO the newest; STRICT and DEFAULT consume oldest-first
// like FIFO but only when the match is unambiguous. Consumption is
// greedy — each lot is taken in full until the requested magnitude is
// met, so the last lot consumed may be reduced only partially.
//
// Reduce is lot-selection-only: it populates each step's Lot and Units
// but leaves SalePricePer/RealizedGain/GainCurrency as the zero
// value. The booking layer in this package (see booking.go) fills
// those in when a price annotation is present on the reducing
// posting.
//
// Errors:
//
//   - [CodeNoMatchingLot] — zero candidates remain after filtering by
//     commodity and matcher.
//   - [CodeAmbiguousLotMatch] — under BookingStrict or BookingDefault
//     more than one candidate remains AND the requested magnitude is
//     strictly less than the sum of magnitudes across all candidates.
//     When the two are equal the booking is unambiguous (a "total
//     match" in upstream beancount's terminology — every matching lot
//     is consumed in full) and Reduce proceeds normally. When the
//     requested magnitude exceeds that sum the diagnostic falls
//     through to [CodeReductionExceedsInventory] (or, for cash
//     candidates, the consumption proceeds and the overdraft is the
//     balance assertion's concern).
//   - [CodeReductionExceedsInventory] — the magnitude to consume
//     exceeds the total units available across all candidates.
//   - [CodeInvalidBookingMethod] — BookingAverage, which is not yet
//     supported.
//   - [CodeInternalError] — BookingNone (never routed here by classify)
//     or an arithmetic error from the decimal context.
func (i *Inventory) Reduce(
	units ast.Amount,
	matcher CostMatcher,
	m ast.BookingMethod,
) ([]ReductionStep, error) {
	// BookingAverage is unsupported; BookingNone must have been routed
	// elsewhere by classify. Defend both invariants.
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

	// Reduce accepts units of either sign — a sale is typically passed
	// as a negative amount — so work with the absolute magnitude. A
	// zero-magnitude reduction is a no-op.
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
		ord int // tie-breaker on sort
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

	// Overdraft check.
	// NOTE: Cash has no lot identity. So leaving their overdraft checks to
	// the balance assertions.
	if !allCash && remaining.Cmp(&totalAvailable) > 0 {
		return nil, Error{
			Code:    CodeReductionExceedsInventory,
			Message: "reducing posting requests more units than the matched lots contain",
		}
	}

	// STRICT/DEFAULT require an unambiguous match. Multiple candidates
	// are still unambiguous on a "total match" — remaining equals the
	// sum of all candidate magnitudes, so every lot is consumed in
	// full. Strictly-less-than catches the genuinely ambiguous case;
	// overdrafts were already rejected above (or, for cash candidates,
	// allowed to proceed).
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

	// Walk the ordered candidates, consuming min(remaining, lot) from
	// each and emitting one step per lot touched.
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

		// Decrement the live position toward zero: a normal long lot
		// subtracts the taken magnitude, a defensive short lot adds it.
		// The decimal helpers permit dst to alias an operand.
		if p.Units.Number.Sign() >= 0 {
			if err := subDecimal(&p.Units.Number, &p.Units.Number, &take, "inventory reduce: sub lot units"); err != nil {
				return nil, err
			}
		} else {
			if err := addDecimal(&p.Units.Number, &p.Units.Number, &take, "inventory reduce: add lot units"); err != nil {
				return nil, err
			}
		}
		if err := subDecimal(&remaining, &remaining, &take, "inventory reduce: sub remaining"); err != nil {
			return nil, err
		}

		var step ReductionStep
		if p.Cost != nil {
			// prevent aliasing the inventory's decimal
			step.Lot = *p.Cost.Clone()
		}
		step.Units.Set(&take)
		steps = append(steps, step)
	}

	// Drop fully-consumed positions. Walk in reverse so deletions do
	// not shift indices yet to be visited.
	for idx := len(i.positions) - 1; idx >= 0; idx-- {
		if i.positions[idx].Units.Number.Sign() == 0 {
			i.positions = slices.Delete(i.positions, idx, idx+1)
		}
	}

	return steps, nil
}

// lotDate returns the Cost.Date of a position, or the zero time if the
// position has no cost. Used as the FIFO/LIFO sort key.
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
// position and a non-cash position are never equal. Two nil inventories
// are equal; a nil inventory is never equal to a non-nil one.
func (i *Inventory) Equal(o *Inventory) bool {
	if i == nil {
		return o == nil
	}
	if o == nil {
		return false
	}
	if len(i.positions) != len(o.positions) {
		return false
	}
	for idx := range i.positions {
		a := i.positions[idx]
		b := o.positions[idx]
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
