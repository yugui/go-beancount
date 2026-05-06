package inventory

import (
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
	return a.Equal(*b)
}

// Reduce consumes |units.Number| of the given commodity, filtered by
// matcher, under booking method m, and returns one [ReductionStep] per
// lot consumed. Reduce mutates i in place, decrementing or removing
// the consumed positions.
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
	// BookingAverage is not yet supported.
	if m == ast.BookingAverage {
		return nil, Error{
			Code:    CodeInvalidBookingMethod,
			Message: "booking method AVERAGE is not supported",
		}
	}
	// BookingNone should never reach Reduce: classify routes NONE
	// postings along a separate path. Defend the invariant here.
	if m == ast.BookingNone {
		return nil, Error{
			Code:    CodeInternalError,
			Message: "Reduce called with BookingNone; classify should have routed this posting elsewhere",
		}
	}

	// Compute the absolute magnitude to consume. Reduce accepts units
	// with either sign — the caller typically passes a negative amount
	// for a sale — so we work with the absolute value throughout.
	var remaining apd.Decimal
	if _, err := apd.BaseContext.Abs(&remaining, &units.Number); err != nil {
		return nil, Error{
			Code:    CodeInternalError,
			Message: "inventory reduce: abs units: " + err.Error(),
		}
	}
	if remaining.Sign() == 0 {
		// A zero-magnitude reduction is a no-op. Return no steps
		// rather than scanning for candidates: there is nothing to
		// consume and no booking decision to make.
		return nil, nil
	}

	commodity := units.Currency

	// Collect the indices of candidate positions: same commodity and
	// matcher-approved. We keep the indices (not copies) so we can
	// mutate i.positions in place later.
	type candidate struct {
		idx int // index into i.positions
		// ord is the original insertion order; used as a stable
		// tie-breaker when Cost.Date is identical across lots.
		ord int
	}
	var candidates []candidate
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
		candidates = append(candidates, candidate{idx: idx, ord: idx})
	}

	if len(candidates) == 0 {
		return nil, Error{
			Code:    CodeNoMatchingLot,
			Message: "no lot in inventory matches the reducing posting",
		}
	}

	// Sum the magnitudes of all candidate units. Used both as an
	// availability bound for the overdraft check below and as the
	// "total match" predicate for the STRICT/DEFAULT ambiguity rule:
	// when the requested magnitude equals this sum, the booking is
	// unambiguous because every matching lot is consumed in full.
	var totalAvailable apd.Decimal
	for _, c := range candidates {
		var abs apd.Decimal
		if _, err := apd.BaseContext.Abs(&abs, &i.positions[c.idx].Units.Number); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Message: "inventory reduce: abs lot units: " + err.Error(),
			}
		}
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &totalAvailable, &abs); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Message: "inventory reduce: accumulate available: " + err.Error(),
			}
		}
		totalAvailable.Set(&sum)
	}

	// STRICT (and DEFAULT, which behaves like STRICT for the purposes
	// of lot selection) requires the match to be unambiguous. Multiple
	// candidates are still unambiguous when the requested magnitude
	// equals the sum of all candidate magnitudes — the only possible
	// booking is to consume every matching lot in full (upstream
	// beancount calls this a "total match"). The strictly-less-than
	// comparison is deliberate: when remaining > totalAvailable the
	// reduction is over-drafted rather than ambiguous, and falls
	// through to the CodeReductionExceedsInventory check below so
	// users see the more specific diagnostic.
	if (m == ast.BookingStrict || m == ast.BookingDefault) && len(candidates) > 1 {
		if remaining.Cmp(&totalAvailable) < 0 {
			return nil, Error{
				Code:    CodeAmbiguousLotMatch,
				Message: "reducing posting matches more than one lot under STRICT booking and does not consume them all",
			}
		}
	}

	// Order the candidates per booking method. FIFO consumes oldest
	// first, LIFO consumes newest first. The single-candidate
	// STRICT/DEFAULT case and the multi-candidate total-match case are
	// both order-insensitive at the consumption level (the latter
	// always exhausts every candidate); they fall through the same
	// ordered consumption path so the emitted ReductionStep sequence
	// is deterministic. STRICT/DEFAULT shares the FIFO branch by
	// default; LIFO is only selected when explicitly requested.
	switch m {
	case ast.BookingLIFO:
		slices.SortStableFunc(candidates, func(a, b candidate) int {
			// Newest first: reverse date order. Tie-break by reverse
			// insertion order so that two lots on the same day
			// consume the most recently added one first.
			da := lotDate(&i.positions[a.idx])
			db := lotDate(&i.positions[b.idx])
			if da.After(db) {
				return -1
			}
			if da.Before(db) {
				return 1
			}
			// Same date: prefer the higher original index.
			if a.ord > b.ord {
				return -1
			}
			if a.ord < b.ord {
				return 1
			}
			return 0
		})
	default:
		// BookingFIFO and the unambiguous STRICT/DEFAULT case.
		slices.SortStableFunc(candidates, func(a, b candidate) int {
			da := lotDate(&i.positions[a.idx])
			db := lotDate(&i.positions[b.idx])
			if da.Before(db) {
				return -1
			}
			if da.After(db) {
				return 1
			}
			// Same date: preserve original insertion order.
			if a.ord < b.ord {
				return -1
			}
			if a.ord > b.ord {
				return 1
			}
			return 0
		})
	}

	// Pre-check: do the candidates contain enough units to cover the
	// reduction? totalAvailable was computed above for the total-match
	// predicate; reuse it here to avoid partially mutating the
	// inventory and then erroring out.
	// Cash candidates have no lot identity (currency units are fungible),
	// so an overdraft is the balance assertion's concern, not booking's.
	// See package doc "# Lot identity" for the full rationale.
	allCash := true
	for _, c := range candidates {
		if i.positions[c.idx].Cost != nil {
			allCash = false
			break
		}
	}
	if !allCash && remaining.Cmp(&totalAvailable) > 0 {
		return nil, Error{
			Code:    CodeReductionExceedsInventory,
			Message: "reducing posting requests more units than the matched lots contain",
		}
	}

	// Walk the candidates in order and emit one step per consumed
	// lot. We track the indices we need to remove after the walk to
	// avoid invalidating the candidate indices mid-iteration.
	steps := make([]ReductionStep, 0, len(candidates))
	for _, c := range candidates {
		if remaining.Sign() == 0 {
			break
		}
		p := &i.positions[c.idx]

		var available apd.Decimal
		if _, err := apd.BaseContext.Abs(&available, &p.Units.Number); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Message: "inventory reduce: abs lot units: " + err.Error(),
			}
		}

		// Consume min(remaining, available) from this lot.
		var take apd.Decimal
		if remaining.Cmp(&available) >= 0 {
			take.Set(&available)
		} else {
			take.Set(&remaining)
		}

		// Decrement the live position's Units.Number by the taken
		// magnitude. The lot's sign (always positive for a normal
		// long position) is preserved.
		var newNum apd.Decimal
		if p.Units.Number.Sign() >= 0 {
			if _, err := apd.BaseContext.Sub(&newNum, &p.Units.Number, &take); err != nil {
				return nil, Error{
					Code:    CodeInternalError,
					Message: "inventory reduce: sub lot units: " + err.Error(),
				}
			}
		} else {
			// A short position (negative units) reduces toward zero
			// by addition. This path is defensive; normal augmentation
			// never produces short positions in this package.
			if _, err := apd.BaseContext.Add(&newNum, &p.Units.Number, &take); err != nil {
				return nil, Error{
					Code:    CodeInternalError,
					Message: "inventory reduce: add lot units: " + err.Error(),
				}
			}
		}
		p.Units.Number.Set(&newNum)

		// Update remaining = remaining - take.
		var newRemaining apd.Decimal
		if _, err := apd.BaseContext.Sub(&newRemaining, &remaining, &take); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Message: "inventory reduce: sub remaining: " + err.Error(),
			}
		}
		remaining.Set(&newRemaining)

		// Build the step. Clone the taken magnitude so later edits to
		// the inventory do not alias the step's Units decimal.
		step := ReductionStep{}
		if p.Cost != nil {
			// Deep-copy the lot so the step does not share the
			// position's coefficient buffer.
			step.Lot = *p.Cost.Clone()
		}
		step.Units.Set(&take)
		steps = append(steps, step)
	}

	// Remove positions that have been fully consumed. Walk the
	// original slice in reverse so index deletions do not shift
	// earlier indices.
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
