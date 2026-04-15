package inventory

import (
	"fmt"

	apd "github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
)

// Reducer streams through an [ast.Ledger], maintaining per-account
// Inventory state and emitting [BookedPosting] records via a caller-
// supplied visitor. The primary entry point is [Reducer.Walk]; the
// higher-level Run / Inspect convenience wrappers will arrive in Phase
// 5 Step 7.
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
			m, err := d.ResolveBookingMethod()
			if err != nil {
				r.errs = append(r.errs, Error{
					Code:    CodeInvalidBookingMethod,
					Span:    d.Span,
					Account: d.Account,
					Message: fmt.Sprintf("invalid booking method %q: %v", d.Booking, err),
				})
				m = ast.BookingDefault
			}
			r.booking[d.Account] = m
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

	// Pass 1: book explicit postings.
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
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			booked = append(booked, bp)
		}
	}

	// Pass 2: auto-posting inference.
	if autoIdx >= 0 {
		residual, residualErrs := r.computeResidual(txn, autoIdx)
		if len(residualErrs) > 0 {
			r.errs = append(r.errs, residualErrs...)
		} else {
			p := &txn.Postings[autoIdx]
			amt := &ast.Amount{Currency: residual.Currency}
			amt.Number.Set(&residual.Number)
			p.Amount = amt
			captureBefore(r.state, before, touched, p.Account)
			inv := r.ensureInventory(p.Account)
			method := r.booking[p.Account]
			bp, errs := bookOne(inv, p, method, txn.Date, true)
			r.errs = append(r.errs, errs...)
			if len(errs) == 0 {
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

// computeResidual sums the signed weights of every explicit posting in
// txn and returns the amount the auto-posting at autoIdx must absorb.
//
// Beancount permits multiple currencies in a single transaction (as in
// a foreign-currency conversion), but the inferred auto-posting must
// settle exactly one currency. A residual that is already zero in every
// currency (or one that has more than one non-zero currency) is
// rejected with [CodeUnresolvableAutoPosting].
//
// The decimal returned by [validation.PostingWeight] is always a fresh
// allocation, so negating it in place does not corrupt the AST.
func (r *Reducer) computeResidual(txn *ast.Transaction, autoIdx int) (ast.Amount, []Error) {
	sums := map[string]*apd.Decimal{}
	order := []string{} // stable reporting order

	for i := range txn.Postings {
		if i == autoIdx {
			continue
		}
		p := &txn.Postings[i]
		if p.Amount == nil {
			// Should not happen: visitTxn has already rejected
			// transactions with more than one auto-posting before
			// reaching computeResidual.
			continue
		}
		w, cur, err := validation.PostingWeight(p)
		if err != nil {
			return ast.Amount{}, []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "compute residual: posting weight: " + err.Error(),
			}}
		}
		if w == nil {
			// A nil weight with a nil error denotes an auto-posting,
			// which we already filtered above. Defensive skip.
			continue
		}
		if existing, ok := sums[cur]; ok {
			if _, err := apd.BaseContext.Add(existing, existing, w); err != nil {
				return ast.Amount{}, []Error{{
					Code:    CodeInternalError,
					Span:    p.Span,
					Account: p.Account,
					Message: "compute residual: accumulate weight: " + err.Error(),
				}}
			}
		} else {
			sums[cur] = w
			order = append(order, cur)
		}
	}

	// Filter out currencies whose residual is exactly zero.
	nonZero := make([]string, 0, len(order))
	for _, cur := range order {
		if !sums[cur].IsZero() {
			nonZero = append(nonZero, cur)
		}
	}

	autoP := &txn.Postings[autoIdx]
	switch len(nonZero) {
	case 0:
		return ast.Amount{}, []Error{{
			Code:    CodeUnresolvableAutoPosting,
			Span:    autoP.Span,
			Account: autoP.Account,
			Message: "auto-balanced posting has no residual to absorb; every currency already balances",
		}}
	case 1:
		cur := nonZero[0]
		residual := sums[cur]
		// Negate in place: PostingWeight returned a fresh allocation, so
		// this does not disturb any AST field.
		if _, err := apd.BaseContext.Neg(residual, residual); err != nil {
			return ast.Amount{}, []Error{{
				Code:    CodeInternalError,
				Span:    autoP.Span,
				Account: autoP.Account,
				Message: "compute residual: negate: " + err.Error(),
			}}
		}
		out := ast.Amount{Currency: cur}
		out.Number.Set(residual)
		return out, nil
	default:
		return ast.Amount{}, []Error{{
			Code:    CodeUnresolvableAutoPosting,
			Span:    autoP.Span,
			Account: autoP.Account,
			Message: fmt.Sprintf("auto-balanced posting cannot absorb residual across %d currencies: %v", len(nonZero), nonZero),
		}}
	}
}
