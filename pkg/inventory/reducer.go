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
// As a side effect of interpolation, Walk writes resolved values back
// into the ledger's posting AST: an auto-balanced posting (nil Amount)
// receives the inferred Amount, and a deferred cost-spec posting (Cost
// present, PerUnit nil) receives the inferred PerUnit. Callers that
// retain pointers into transaction postings must be aware those fields
// may transition from nil to populated during Walk.
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

	// Reject structurally-invalid transactions before mutating any
	// account state, so a rejected transaction leaves inventory
	// untouched.
	if !r.validateStructure(txn) {
		return before, nil, nil, false
	}

	// Pass 1: book explicit postings while collecting unknowns. A
	// posting that fails with CodeAugmentationRequiresCost AND has a
	// cost spec without a number is held back as a deferred unknown
	// (Pass 2 may infer its per-unit cost from the residual). The
	// auto-posting (Amount == nil) is also an unknown. If more than
	// one unknown is collected, the transaction is ambiguous and is
	// reported below.
	var unknowns []*ast.Posting
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			// Auto-posting (validated single & no cost/price by
			// validateStructure).
			unknowns = append(unknowns, p)
			continue
		}

		captureBefore(r.state, before, touched, p.Account)

		inv := r.ensureInventory(p.Account)
		method := r.booking[p.Account] // zero value = BookingDefault
		bp, errs := bookOne(inv, p, method, txn.Date, false)
		if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
			unknowns = append(unknowns, p)
			continue
		}
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			booked = append(booked, bp)
		}
	}

	// Pass 2: resolve the single unknown, if any.
	switch {
	case len(unknowns) > 1:
		// Ambiguous: too many unknowns for a single residual to pin
		// down. Emit one diagnostic per unknown so the user sees every
		// site they need to fix.
		r.flagAmbiguousUnknowns(unknowns)
	case len(unknowns) == 1:
		unknownP := unknowns[0]
		if residual, ok := r.solveResidual(booked, unknownP); ok {
			var bp BookedPosting
			var fillOk bool
			if unknownP.Amount == nil {
				bp, fillOk = r.fillAutoPosting(txn, unknownP, residual, touched, before)
			} else {
				bp, fillOk = r.fillDeferredCost(txn, unknownP, residual)
			}
			if fillOk {
				booked = append(booked, bp)
			}
		}
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

// validateStructure rejects transactions whose auto-posting structure
// is invalid: an auto-balanced posting (nil Amount) must not carry a
// Cost or Price spec, and at most one auto-balanced posting may appear.
// On any violation it appends a diagnostic to r.errs and returns false;
// the caller must abort before touching account state. Returning early
// here is what guarantees that a rejected transaction leaves inventory
// untouched.
func (r *Reducer) validateStructure(txn *ast.Transaction) bool {
	seenAuto := false
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
			return false
		}
		if seenAuto {
			r.errs = append(r.errs, Error{
				Code:    CodeMultipleAutoPostings,
				Span:    p.Span,
				Account: p.Account,
				Message: "transaction has more than one auto-balanced posting",
			})
			return false
		}
		seenAuto = true
	}
	return true
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

// flagAmbiguousUnknowns emits one CodeUnresolvableInterpolation per
// unknown when the transaction has more than one. The wording branches
// on whether the unknown is the auto-posting (no Amount, so the
// "amount" is unresolved) or a deferred cost-spec (Amount is set, only
// the per-unit "cost" is unresolved).
func (r *Reducer) flagAmbiguousUnknowns(unknowns []*ast.Posting) {
	for _, p := range unknowns {
		msg := "cannot interpolate cost: transaction has multiple unknown posting values"
		if p.Amount == nil {
			msg = "cannot interpolate amount: transaction has multiple unknown posting values"
		}
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    p.Span,
			Account: p.Account,
			Message: msg,
		})
	}
}

// solveResidual computes the per-currency net of the already-booked
// postings and returns the single residual the unknown must absorb,
// expressed as the [ast.Amount] that — added to the booked weights —
// makes the transaction balance. Each BookedPosting reflects the
// resolved cost (e.g. a reduction's matched lot cost), which is what
// makes this loop correct in the presence of partial cost specs the
// AST has not yet been written back for.
//
// On any failure (internal arithmetic error, zero residual, or residual
// spanning multiple currencies) a diagnostic is appended to r.errs and
// ok is false. The zero-residual wording branches on whether the
// unknown is an auto-posting or a deferred cost-spec.
func (r *Reducer) solveResidual(booked []BookedPosting, unknownP *ast.Posting) (*ast.Amount, bool) {
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
			return nil, false
		}
		if w == nil {
			continue
		}
		if existing, found := sums[cur]; found {
			if _, err := apd.BaseContext.Add(existing, existing, w); err != nil {
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    bp.Source.Span,
					Account: bp.Account,
					Message: "interpolate: accumulate weight: " + err.Error(),
				})
				return nil, false
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

	if len(nonZero) != 1 {
		var msg string
		if len(nonZero) == 0 {
			if unknownP.Amount == nil {
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
		return nil, false
	}

	out := &ast.Amount{Currency: nonZero[0]}
	if _, err := apd.BaseContext.Neg(&out.Number, sums[nonZero[0]]); err != nil {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "interpolate: negate residual: " + err.Error(),
		})
		return nil, false
	}
	return out, true
}

// fillAutoPosting writes the resolved residual into the auto-posting's
// Amount and books it, threading touched/before so the lazy capture-
// before dance stays consistent for an account first touched by the
// auto-posting. Errors emitted by bookOne are appended to r.errs; ok
// is true exactly when the booking succeeded.
func (r *Reducer) fillAutoPosting(
	txn *ast.Transaction,
	auto *ast.Posting,
	residual *ast.Amount,
	touched map[ast.Account]bool,
	before map[ast.Account]*Inventory,
) (BookedPosting, bool) {
	auto.Amount = residual
	captureBefore(r.state, before, touched, auto.Account)
	inv := r.ensureInventory(auto.Account)
	method := r.booking[auto.Account]
	bp, errs := bookOne(inv, auto, method, txn.Date, true)
	r.errs = append(r.errs, errs...)
	return bp, len(errs) == 0
}

// fillDeferredCost solves for the per-unit cost that absorbs residual
// across the posting's units, writes it back into deferred.Cost.PerUnit
// (so ResolveCost and the loader's writeAugmentationCost see the same
// number), and re-books the posting. A zero unit count or arithmetic
// failure is reported via r.errs and yields ok=false.
func (r *Reducer) fillDeferredCost(
	txn *ast.Transaction,
	deferred *ast.Posting,
	residual *ast.Amount,
) (BookedPosting, bool) {
	if deferred.Amount.Number.Sign() == 0 {
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    deferred.Span,
			Account: deferred.Account,
			Message: "deferred cost cannot be interpolated: posting has zero units",
		})
		return BookedPosting{}, false
	}
	perUnit := &ast.Amount{Currency: residual.Currency}
	if _, err := quoContext.Quo(&perUnit.Number, &residual.Number, &deferred.Amount.Number); err != nil {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    deferred.Span,
			Account: deferred.Account,
			Message: "interpolate: divide residual by units: " + err.Error(),
		})
		return BookedPosting{}, false
	}
	deferred.Cost.PerUnit = perUnit
	bp, errs := bookOne(r.ensureInventory(deferred.Account), deferred, r.booking[deferred.Account], txn.Date, false)
	r.errs = append(r.errs, errs...)
	return bp, len(errs) == 0
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
