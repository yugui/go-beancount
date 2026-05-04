package inventory

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Reducer streams through an [ast.Ledger], maintaining per-account
// Inventory state and emitting [BookedPosting] records via a caller-
// supplied visitor. The primary entry point is [Reducer.Walk]; see
// [Reducer.Run] for a batch convenience that retains only the final
// per-account state, and [Reducer.Inspect] for an on-demand single-
// transaction view.
//
// A Reducer is not safe for concurrent use. It is, however, reusable:
// calling [Reducer.Walk] twice on the same Reducer produces identical
// results because Walk resets internal state at entry.
type Reducer struct {
	ledger *ast.Ledger
	// booking tracks the per-account booking method discovered from an
	// Open directive. Accounts that have not yet been opened (or whose
	// Open omitted a booking keyword) resolve to BookingDefault via the
	// zero value of the map.
	booking map[ast.Account]ast.BookingMethod
	// state holds the mutable Inventory snapshot for each account that
	// has been touched by at least one booked posting.
	state map[ast.Account]*Inventory
	// errs collects diagnostics emitted during Walk.
	errs []Error
}

// NewReducer returns a Reducer ready to walk the given ledger. The
// ledger is retained by reference; the caller must not mutate it while
// a Walk is in progress.
func NewReducer(ledger *ast.Ledger) *Reducer {
	return &Reducer{ledger: ledger}
}

// VisitFunc is called once per [ast.Transaction] during [Reducer.Walk].
// The before and after maps contain only the accounts touched by the
// transaction. An account that was never seen before the transaction
// maps to a nil *Inventory in before; after always holds a non-nil
// (possibly empty) deep-copied snapshot. Both maps' *Inventory values
// are fresh clones that the callback may retain beyond the invocation
// without risk of later mutation by Walk. Returning false terminates
// iteration early.
type VisitFunc func(
	txn *ast.Transaction,
	before map[ast.Account]*Inventory,
	after map[ast.Account]*Inventory,
	booked []BookedPosting,
) bool

// Walk iterates the ledger in canonical order, applying per-transaction
// booking to the internal per-account Inventory state and invoking
// visit for each transaction that touched at least one account.
//
// Directives other than Open, Close, and Transaction are ignored by
// this layer. Balance, Pad, and Price checks are already enforced by
// [pkg/validation] during its own pass and are not re-evaluated here.
//
// Walk is the primary API and is safe to call at most once per Reducer
// in typical use. Calling Walk repeatedly on the same Reducer is
// permitted — state is reset to empty at the start of each call — but
// each call pays the full O(N) cost of re-streaming the ledger.
//
// Errors collected during the walk are returned as a fresh slice the
// caller may retain. An error does not stop iteration unless the
// visitor returns false; subsequent transactions still run even after
// errors are recorded.
func (r *Reducer) Walk(visit VisitFunc) []Error {
	// Reset state so repeat calls are idempotent.
	r.state = map[ast.Account]*Inventory{}
	r.booking = map[ast.Account]ast.BookingMethod{}
	r.errs = nil

	for _, d := range r.ledger.All() {
		switch d := d.(type) {
		case *ast.Open:
			// d.Booking is already a typed BookingMethod (invalid
			// keywords are rejected by the lowerer), so record it
			// directly.
			r.booking[d.Account] = d.Booking
			// Leave r.state[d.Account] unset; a later augmentation
			// will create the inventory lazily on first touch.
		case *ast.Close:
			// No-op for inventory. pkg/validation already rejects
			// directives targeting a closed account, so the inventory
			// state can remain frozen at its final value for later
			// inspection.
		case *ast.Transaction:
			before, after, booked, stop := r.visitTxn(d)
			if len(booked) == 0 && len(before) == 0 {
				// Transaction had no bookable postings (e.g., a purely
				// defensive placeholder). Skip the visitor to keep
				// signal-to-noise high for common cases.
				continue
			}
			if visit != nil {
				if !visit(d, before, after, booked) {
					stop = true
				}
			}
			if stop {
				return append([]Error(nil), r.errs...)
			}
		}
	}

	return append([]Error(nil), r.errs...)
}

// visitTxn performs the per-transaction booking pass, mutating the
// reducer's per-account state in place and returning the before/after
// snapshots plus the booked postings. The stop return value is reserved
// for future use (e.g. fatal structural errors); today it is always
// false when the function returns normally.
func (r *Reducer) visitTxn(txn *ast.Transaction) (
	before map[ast.Account]*Inventory,
	after map[ast.Account]*Inventory,
	booked []BookedPosting,
	stop bool,
) {
	touched := map[ast.Account]bool{}
	before = map[ast.Account]*Inventory{}

	// Pre-pass: scan for auto-posting structural violations. An
	// auto-balanced posting (nil Amount) must not carry a Cost or
	// Price spec, and a transaction must not contain more than one
	// auto-balanced posting. We reject these before mutating any
	// account state so a rejected transaction leaves inventory
	// untouched.
	autoIdx := -1
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount != nil {
			continue
		}
		if p.Cost != nil || p.Price != nil {
			r.errs = append(r.errs, Error{
				Code:    CodeInvalidAutoPosting,
				Span:    p.Span,
				Account: p.Account,
				Message: "auto-balanced posting must not carry cost or price",
			})
			return before, nil, nil, false
		}
		if autoIdx != -1 {
			r.errs = append(r.errs, Error{
				Code:    CodeMultipleAutoPostings,
				Span:    p.Span,
				Account: p.Account,
				Message: "transaction has more than one auto-balanced posting",
			})
			return before, nil, nil, false
		}
		autoIdx = i
	}

	// Pass 1: book explicit postings. A posting that fails with
	// CodeAugmentationRequiresCost AND has a cost spec without a
	// number (the costNumberMissing predicate) is set aside as a
	// deferred unknown; Pass 2 will try to interpolate the cost from
	// the transaction's residual. Every other booking error is
	// surfaced immediately.
	var deferred []int
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			continue
		}

		// Capture the before-state lazily, once per touched account.
		captureBefore(r.state, before, touched, p.Account)

		// Book the posting.
		inv := r.ensureInventory(p.Account)
		method := r.booking[p.Account] // zero value = BookingDefault
		bp, errs := bookOne(inv, p, method, txn.Date, false)
		if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
			// Hold this posting back; Pass 2 may be able to fill in
			// the missing per-unit number from the residual.
			deferred = append(deferred, i)
			continue
		}
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			booked = append(booked, bp)
		}
	}

	// Pass 2: unified interpolation. Treats the auto-posting and any
	// deferred cost-spec postings as a uniform set of "unknowns" the
	// transaction's residual must resolve to a single concrete value.
	if autoIdx >= 0 || len(deferred) > 0 {
		extra := r.interpolate(txn, booked, deferred, autoIdx, touched, before)
		booked = append(booked, extra...)
	}

	// Build the after snapshot. Every touched account is included, even
	// if its inventory is empty — the contract is that after[acct] is
	// always non-nil for touched accounts.
	after = map[ast.Account]*Inventory{}
	for acct := range touched {
		if inv := r.state[acct]; inv != nil {
			after[acct] = inv.Clone()
		} else {
			after[acct] = NewInventory()
		}
	}

	return before, after, booked, false
}

// captureBefore records the account's current inventory state in the
// before map the first time the account is touched within a single
// transaction. Snapshots are deep-copied via [Inventory.Clone] so the
// visitor may retain them safely after Walk resumes mutating state.
// Accounts not yet present in state map to a nil *Inventory snapshot;
// a nil before-value is the signal that the account had never been
// touched prior to this transaction.
func captureBefore(
	state map[ast.Account]*Inventory,
	before map[ast.Account]*Inventory,
	touched map[ast.Account]bool,
	acct ast.Account,
) {
	if touched[acct] {
		return
	}
	touched[acct] = true
	if inv, ok := state[acct]; ok && inv != nil {
		before[acct] = inv.Clone()
	} else {
		before[acct] = nil
	}
}

// ensureInventory returns the inventory for acct, lazily creating and
// installing one in r.state on first touch.
func (r *Reducer) ensureInventory(acct ast.Account) *Inventory {
	if inv, ok := r.state[acct]; ok && inv != nil {
		return inv
	}
	inv := NewInventory()
	r.state[acct] = inv
	return inv
}

// interpolate runs the unified Pass 2: the transaction's residual
// (computed from the postings already booked in Pass 1) is used to
// fill in exactly one unknown — either an auto-posting Amount or a
// deferred cost-spec's per-unit number — and the resolved posting is
// then booked. When the count of unknowns is anything other than one,
// or the residual cannot be expressed as a single non-zero currency,
// a [CodeUnresolvableInterpolation] is emitted on every unknown so the
// caller sees a clear pointer back to each ambiguous posting.
//
// touched and before are threaded through so the lazy capture-before
// dance stays consistent: an auto-posting account that was untouched
// in Pass 1 still gets a nil before-snapshot recorded the moment Pass
// 2 books it.
func (r *Reducer) interpolate(
	txn *ast.Transaction,
	booked []BookedPosting,
	deferred []int,
	autoIdx int,
	touched map[ast.Account]bool,
	before map[ast.Account]*Inventory,
) []BookedPosting {
	unknownCount := len(deferred)
	if autoIdx >= 0 {
		unknownCount++
	}
	if unknownCount == 0 {
		return nil
	}

	if unknownCount > 1 {
		// Ambiguous: too many unknowns for a single residual to pin
		// down. Emit one diagnostic per unknown so a user fixing the
		// ledger sees both sites.
		for _, idx := range deferred {
			p := &txn.Postings[idx]
			r.errs = append(r.errs, Error{
				Code:    CodeUnresolvableInterpolation,
				Span:    p.Span,
				Account: p.Account,
				Message: fmt.Sprintf("cannot interpolate cost: transaction has %d unknown posting values, expected 1", unknownCount),
			})
		}
		if autoIdx >= 0 {
			p := &txn.Postings[autoIdx]
			r.errs = append(r.errs, Error{
				Code:    CodeUnresolvableInterpolation,
				Span:    p.Span,
				Account: p.Account,
				Message: fmt.Sprintf("cannot interpolate amount: transaction has %d unknown posting values, expected 1", unknownCount),
			})
		}
		return nil
	}

	// Compute the residual from the already-booked postings. Each
	// BookedPosting reflects the resolved cost (e.g. a reduction's
	// matched lot cost), which is what makes this loop correct in the
	// presence of partial cost specs that the AST has not yet been
	// written back for.
	sums := map[string]*apd.Decimal{}
	var order []string
	for i := range booked {
		bp := booked[i]
		w, cur, err := bookedPostingWeight(bp)
		if err != nil {
			r.errs = append(r.errs, Error{
				Code:    CodeInternalError,
				Span:    bp.Source.Span,
				Account: bp.Account,
				Message: "interpolate: posting weight: " + err.Error(),
			})
			return nil
		}
		if w == nil {
			continue
		}
		if existing, ok := sums[cur]; ok {
			if _, err := apd.BaseContext.Add(existing, existing, w); err != nil {
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    bp.Source.Span,
					Account: bp.Account,
					Message: "interpolate: accumulate weight: " + err.Error(),
				})
				return nil
			}
		} else {
			sums[cur] = w
			order = append(order, cur)
		}
	}

	nonZero := make([]string, 0, len(order))
	for _, cur := range order {
		if !sums[cur].IsZero() {
			nonZero = append(nonZero, cur)
		}
	}

	// Locate the unknown's posting and span. Exactly one of the two
	// branches fires by the unknownCount==1 contract above.
	var unknownIdx int
	if autoIdx >= 0 {
		unknownIdx = autoIdx
	} else {
		unknownIdx = deferred[0]
	}
	unknownP := &txn.Postings[unknownIdx]

	if len(nonZero) != 1 {
		var msg string
		if len(nonZero) == 0 {
			if autoIdx >= 0 {
				msg = "auto-balanced posting has no residual to absorb; every currency already balances"
			} else {
				msg = "deferred cost cannot be interpolated: every currency already balances"
			}
		} else {
			msg = fmt.Sprintf("residual spans %d currencies %v but a single unknown can only absorb one", len(nonZero), nonZero)
		}
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: msg,
		})
		return nil
	}

	cur := nonZero[0]
	residual := sums[cur]
	// Negate in place: bookedPostingWeight always returns a fresh
	// allocation, so this does not disturb any AST or BookedPosting
	// field.
	if _, err := apd.BaseContext.Neg(residual, residual); err != nil {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "interpolate: negate residual: " + err.Error(),
		})
		return nil
	}

	if autoIdx >= 0 {
		// Fill the auto-posting Amount with -residual in cur and book.
		amt := &ast.Amount{Currency: cur}
		amt.Number.Set(residual)
		unknownP.Amount = amt
		captureBefore(r.state, before, touched, unknownP.Account)
		inv := r.ensureInventory(unknownP.Account)
		method := r.booking[unknownP.Account]
		bp, errs := bookOne(inv, unknownP, method, txn.Date, true)
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			return []BookedPosting{bp}
		}
		return nil
	}

	// Deferred cost-spec interpolation: solve for cost_per_unit such
	// that units * cost_per_unit cancels the residual. units is the
	// posting's unit count; a zero unit count is unrecoverable.
	if unknownP.Amount.Number.Sign() == 0 {
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "deferred cost cannot be interpolated: posting has zero units",
		})
		return nil
	}
	costPerUnit := new(apd.Decimal)
	unitsNum := unknownP.Amount.Number
	if _, err := quoContext.Quo(costPerUnit, residual, &unitsNum); err != nil {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "interpolate: divide residual by units: " + err.Error(),
		})
		return nil
	}
	// Write the interpolated per-unit number back into the AST so
	// ResolveCost can complete the lot, and so the loader-level
	// writeAugmentationCost step sees the same number when it folds
	// the resolved Cost back into the spec.
	unknownP.Cost.PerUnit = &ast.Amount{
		Number:   *costPerUnit,
		Currency: cur,
	}
	bp, errs := bookOne(r.ensureInventory(unknownP.Account), unknownP, r.booking[unknownP.Account], txn.Date, false)
	r.errs = append(r.errs, errs...)
	if len(errs) == 0 {
		return []BookedPosting{bp}
	}
	return nil
}

// Run walks the ledger without a visitor, retaining only the final
// per-account inventory state and collected errors. It is equivalent to
// calling Walk with a visitor that always returns true and ignores its
// snapshot arguments. Run is O(N) in the number of directives and O(A)
// in memory, where A is the number of accounts.
func (r *Reducer) Run() []Error {
	return r.Walk(nil)
}

// Final returns the final inventory for the given account after the
// most recent [Reducer.Run] or [Reducer.Walk], or nil if the account
// was never touched. The returned *Inventory aliases the reducer's
// internal state and must not be mutated; callers that need a mutable
// copy should call [Inventory.Clone].
func (r *Reducer) Final(account ast.Account) *Inventory {
	return r.state[account]
}

// Errors returns the errors collected by the most recent [Reducer.Run]
// or [Reducer.Walk]. The returned slice is a fresh copy; callers may
// retain it and mutate it without affecting the reducer.
func (r *Reducer) Errors() []Error {
	return append([]Error(nil), r.errs...)
}

// Inspection holds a single transaction's before/after/booked view as
// returned by [Reducer.Inspect]. The inventories inside Before and
// After are independent deep copies; Booked entries alias into the
// source [ast.Transaction] via their Source field.
type Inspection struct {
	Before map[ast.Account]*Inventory
	After  map[ast.Account]*Inventory
	Booked []BookedPosting
}

// Inspect reconstructs a single transaction's view by re-walking the
// ledger from the start until it reaches txn. It is intended for
// bean-doctor-style trouble-shooting; each call costs O(N) in the
// number of directives up to txn.
//
// The txn argument is matched by pointer identity against the
// transactions stored in the ledger. Callers MUST pass the exact
// *ast.Transaction pointer that appears in the ledger; a freshly
// constructed transaction with equivalent fields will not match.
//
// For repeated inspections over a large ledger, callers should prefer
// [Reducer.Walk] with a visitor that stops at the target transaction.
//
// Returns (nil, errors) if txn is not found in the ledger or if the
// walk ended before reaching it. The errors slice always contains the
// errors collected up to (and including) the point where the walk
// stopped.
//
// After Inspect returns, the reducer's internal state reflects the
// ledger position immediately after the target transaction, not the
// final state of the ledger. Callers that want to resume full-ledger
// processing afterwards should call [Reducer.Run] to restore the
// final state. Every subsequent [Reducer.Walk] or [Reducer.Run] call
// resets the reducer's internal state at entry, so invoking Run after
// Inspect fully restores the final ledger state rather than applying
// additional directives on top of the mid-walk state.
func (r *Reducer) Inspect(txn *ast.Transaction) (*Inspection, []Error) {
	if txn == nil {
		return nil, nil
	}
	var hit *Inspection
	errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got != txn {
			return true
		}
		hit = &Inspection{
			Before: cloneInventoryMap(before),
			After:  cloneInventoryMap(after),
			Booked: append([]BookedPosting(nil), booked...),
		}
		return false
	})
	if hit == nil {
		return nil, errs
	}
	return hit, errs
}

// cloneInventoryMap copies the (Account -> *Inventory) map used by
// Walk's before/after snapshots into a fresh map. [Reducer.Walk]
// already hands the visitor deep-cloned *Inventory values that the
// callback "may retain", so this function only needs to duplicate the
// map spine; the values remain safe to retain after Walk resumes.
// A nil value (Walk's signal that an account was not previously
// touched) is preserved as nil.
func cloneInventoryMap(src map[ast.Account]*Inventory) map[ast.Account]*Inventory {
	if src == nil {
		return nil
	}
	out := make(map[ast.Account]*Inventory, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
