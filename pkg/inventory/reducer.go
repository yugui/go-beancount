package inventory

import (
	"fmt"
	"iter"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Reducer streams through a sequence of [ast.Directive] values,
// maintaining per-account [Inventory] state and emitting
// [BookedPosting] records via a caller-supplied visitor. The primary
// entry point is [Reducer.Walk]; see [Reducer.Run] for a batch
// convenience that retains only the final per-account state, and
// [Reducer.Inspect] for an on-demand single-transaction view.
//
// A Reducer is not safe for concurrent use. It is reusable: calling
// [Reducer.Walk] repeatedly on the same Reducer produces identical
// results because Walk resets internal state at entry and re-iterates
// the directives sequence supplied at construction.
//
// Walk does not mutate its input. Transactions whose booking pass would
// edit a posting (auto-balanced amount, deferred cost, multi-lot
// reduction with a bare cost spec) are deep-cloned and the clone is
// returned in Walk's [ast.Directive] output. Other directives, and
// transactions whose booking is provably observation-only, pass through
// the output by reference.
type Reducer struct {
	// directives is the input sequence supplied at construction. Walk
	// re-iterates it on every call; iter.Seq2 callers must therefore
	// hand in a replayable sequence (e.g. [ast.Ledger.All] or
	// [slices.All] over a stable slice).
	directives iter.Seq2[int, ast.Directive]
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

// NewReducer returns a Reducer that will iterate the given directives
// sequence on each [Reducer.Walk] call. The sequence MUST be replayable;
// an [ast.Ledger.All] iterator is the canonical source. The caller must
// not mutate the underlying directives between calls — Walk treats them
// as immutable input.
func NewReducer(directives iter.Seq2[int, ast.Directive]) *Reducer {
	return &Reducer{directives: directives}
}

// VisitFunc is called once per [ast.Transaction] during [Reducer.Walk].
//
// Pointer contract: txn is always the caller's original input pointer
// (so [Reducer.Inspect] and other identity-based lookups work), while
// each [BookedPosting] in booked has its Source field pointing into
// the reducer's working copy — a clone if the reducer had to mutate
// any posting, the original otherwise. Reading fields on Source
// observes any interpolation the reducer performed (auto-posting
// Amount, deferred PerUnit, multi-lot reduction Total). The two
// pointer worlds therefore differ when, and only when, the
// transaction was cloned.
//
// The before and after maps contain only the accounts touched by the
// transaction. An account that was never seen before the transaction
// maps to a nil *Inventory in before; after always holds a non-nil
// (possibly empty) deep-copied snapshot. Both maps' *Inventory values
// are fresh clones the callback may retain beyond the invocation
// without risk of later mutation by Walk.
//
// Returning false terminates iteration early.
type VisitFunc func(
	txn *ast.Transaction,
	before map[ast.Account]*Inventory,
	after map[ast.Account]*Inventory,
	booked []BookedPosting,
) bool

// Walk iterates the directives sequence in order, applying
// per-transaction booking to the internal per-account [Inventory]
// state and invoking visit for each transaction that touched at least
// one account.
//
// Directives other than Open, Close, and Transaction are passed through
// to the output unchanged. Balance, Pad, and Price checks are already
// enforced by [pkg/validation] during its own pass and are not
// re-evaluated here.
//
// Walk does not mutate the directives it iterates. The first return
// value is a fresh [ast.Directive] slice containing the booking
// outcome: every transaction that the reducer needed to mutate (an
// auto-balanced posting receives the inferred Amount; a deferred
// cost-spec posting receives the inferred PerUnit; a multi-lot
// reduction with no concrete cost number receives a synthesized Total)
// appears as a clone with the mutations applied. Transactions the
// reducer leaves untouched, and all non-Transaction directives, are
// returned by reference.
//
// Errors collected during the walk are returned as a fresh slice the
// caller may retain. An error does not stop iteration unless the
// visitor returns false; subsequent transactions still run even after
// errors are recorded.
//
// Walk is reusable: state is reset to empty at the start of each call.
// Re-walking pays the full O(N) cost of re-iterating and re-cloning.
func (r *Reducer) Walk(visit VisitFunc) ([]ast.Directive, []Error) {
	r.state = map[ast.Account]*Inventory{}
	r.booking = map[ast.Account]ast.BookingMethod{}
	r.errs = nil

	var out []ast.Directive
	for _, d := range r.directives {
		switch d := d.(type) {
		case *ast.Open:
			r.booking[d.Account] = d.Booking
			out = append(out, d)
		case *ast.Close:
			out = append(out, d)
		case *ast.Transaction:
			booked := d
			if needsBookingClone(d) {
				booked = d.Clone()
			}
			before, after, bookedPostings, stop := r.visitTxn(booked)
			out = append(out, booked)
			if len(bookedPostings) == 0 && len(before) == 0 {
				// Transaction had no bookable postings (e.g., a purely
				// defensive placeholder). Skip the visitor to keep
				// signal-to-noise high for common cases.
				continue
			}
			if visit != nil {
				if !visit(d, before, after, bookedPostings) {
					stop = true
				}
			}
			if stop {
				return out, append([]Error(nil), r.errs...)
			}
		default:
			out = append(out, d)
		}
	}

	return out, append([]Error(nil), r.errs...)
}

// needsBookingClone reports whether txn could be mutated by the booking
// pass and therefore must be cloned before its postings are handed to
// the booking machinery. The pass fills auto-posting amounts and may
// rewrite cost specs (deferred PerUnit, multi-lot reduction Total); a
// transaction with neither marker — every posting carries an Amount and
// no Cost — is observationally unchanged by Walk and reuses its
// pointer in the output, avoiding redundant copying on cash-flow-heavy
// ledgers.
func needsBookingClone(txn *ast.Transaction) bool {
	for _, p := range txn.Postings {
		if p.Cost != nil || p.Amount == nil {
			return true
		}
	}
	return false
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
	// Reject structurally-invalid transactions before mutating any
	// account state, so a rejected transaction leaves inventory
	// untouched.
	if !r.validateStructure(txn) {
		return map[ast.Account]*Inventory{}, nil, nil, false
	}

	trace := newStateTrace(r.state)

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

		inv := trace.prepareForEdit(p.Account)
		method := r.booking[p.Account] // zero value = BookingDefault
		bp, errs := bookOne(inv, p, method, txn.Date, false)
		if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
			unknowns = append(unknowns, p)
			continue
		}
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			r.fillMissingCostFromReductions(p, bp.Reductions)
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
				bp, fillOk = r.fillAutoPosting(txn, unknownP, residual, trace)
			} else {
				bp, fillOk = r.fillDeferredCost(txn, unknownP, residual, trace)
			}
			if fillOk {
				r.fillMissingCostFromReductions(unknownP, bp.Reductions)
				booked = append(booked, bp)
			}
		}
	}

	before, after = trace.diff()
	return before, after, booked, false
}

// fillMissingCostFromReductions writes a synthesized Cost.Total into p
// when the user-supplied cost spec carries no concrete number — i.e.
// `{}`, `{date}`, or `{label}` on a multi-lot reduction. The total is:
//
//	Σ |step.Units| × step.Lot.Number
//
// across the matched reduction steps, which is the figure the
// transaction-balance validator (via PostingWeight's Total branch)
// needs to see in cost currency.
//
// When the user wrote a concrete per-unit (`{X CUR}`) or both per-unit
// and total (`{X # T CUR}`), the matcher already guarantees every
// matched lot shares that per-unit cost, so units * PerUnit equals the
// per-step sum and PostingWeight produces the correct weight straight
// from the user's spec. In those cases this function is a no-op; the
// AST is preserved verbatim, mirroring the augmentation-side policy of
// not rewriting concrete user numbers.
//
// Arithmetic errors on apd.BaseContext are not expected for ledger-
// realistic numbers, but if one occurs the function records a
// CodeInternalError diagnostic on the reducer rather than silently
// leaving the cost spec unwritten — a silent skip would let the
// validator fall through to the price branch and produce a subtly
// wrong weight.
func (r *Reducer) fillMissingCostFromReductions(p *ast.Posting, steps []ReductionStep) {
	if len(steps) == 0 {
		return
	}
	if p.Cost == nil || p.Cost.PerUnit != nil || p.Cost.Total != nil {
		return
	}
	var total apd.Decimal
	currency := steps[0].Lot.Currency
	for i := range steps {
		s := &steps[i]
		var part apd.Decimal
		// step.Units is non-negative magnitude per the reducer
		// contract, so part = step.Units * step.Lot.Number is
		// positive on a normal long lot with a positive Number.
		if _, err := apd.BaseContext.Mul(&part, &s.Units, &s.Lot.Number); err != nil {
			r.errs = append(r.errs, Error{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "synthesize reduction total: multiply step: " + err.Error(),
			})
			return
		}
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &total, &part); err != nil {
			r.errs = append(r.errs, Error{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "synthesize reduction total: accumulate step: " + err.Error(),
			})
			return
		}
		total.Set(&sum)
	}
	p.Cost.Total = &ast.Amount{
		Number:   total,
		Currency: currency,
	}
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

// stateTrace records edits to a per-account inventory map within the
// scope of a single transaction. It pairs the long-lived state map
// (shared with the owning Reducer, mutated in place by edits) with a
// before-snapshot map (owned by the trace, populated lazily on first
// touch of each account) so the two stay consistent by construction.
//
// A nil before-value records that the account had no inventory prior
// to this trace — that nil is the visitor-contract signal for "newly
// touched account".
type stateTrace struct {
	state  map[ast.Account]*Inventory
	before map[ast.Account]*Inventory
}

// newStateTrace begins recording edits against state. before-snapshots
// are scoped to this trace; state is shared with the caller and is
// mutated in place by [stateTrace.prepareForEdit].
func newStateTrace(state map[ast.Account]*Inventory) *stateTrace {
	return &stateTrace{
		state:  state,
		before: map[ast.Account]*Inventory{},
	}
}

// prepareForEdit returns the inventory to mutate for acct. On the
// first call for a given acct in this trace, it deep-clones the
// account's current inventory into the before-snapshot (or records
// nil if the account had no inventory yet) and lazily creates an
// inventory if one did not exist. Subsequent calls return the same
// inventory pointer without re-snapshotting.
func (st *stateTrace) prepareForEdit(acct ast.Account) *Inventory {
	if _, seen := st.before[acct]; seen {
		return st.state[acct]
	}
	inv := st.state[acct]
	if inv == nil {
		inv = NewInventory()
		st.state[acct] = inv
		st.before[acct] = nil
	} else {
		st.before[acct] = inv.Clone()
	}
	return inv
}

// diff returns the (before, after) pair for the visitor callback.
// The before map is the trace's own — diff transfers ownership to
// the caller, which is safe because a stateTrace is scoped to a
// single visitTxn invocation and is discarded immediately after.
// after is freshly constructed as clones of the current state for
// every account first touched by this trace.
func (st *stateTrace) diff() (before, after map[ast.Account]*Inventory) {
	after = make(map[ast.Account]*Inventory, len(st.before))
	for acct := range st.before {
		if inv := st.state[acct]; inv != nil {
			after[acct] = inv.Clone()
		} else {
			// Defensive: prepareForEdit always installs a non-nil
			// inventory and the booking layer never deletes one,
			// so this branch should be unreachable in practice. Fall
			// back to an empty inventory so the visitor contract
			// (after[acct] non-nil for every touched account) is
			// preserved even if the invariant ever shifts.
			after[acct] = NewInventory()
		}
	}
	return st.before, after
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
// makes the transaction balance.
//
// At this point Pass 1 has already written back any reduction Total
// the matcher inferred (see fillMissingCostFromReductions) and any
// concrete user cost spec is preserved on the AST verbatim, so reading
// the weight via [PostingWeight] yields the same exact figure that
// [pkg/validation] will see when it checks transaction balance. There
// is no separate booked-only weight path; the reducer and validation
// share a single formula.
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
		w, cur, err := PostingWeight(bp.Source)
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
// Amount and books it, using trace.prepareForEdit to record the prior
// state of the auto-posting's account if this is the first time the
// transaction has touched it. Errors emitted by bookOne are appended
// to r.errs; ok is true exactly when the booking succeeded.
func (r *Reducer) fillAutoPosting(
	txn *ast.Transaction,
	auto *ast.Posting,
	residual *ast.Amount,
	trace *stateTrace,
) (BookedPosting, bool) {
	auto.Amount = residual
	inv := trace.prepareForEdit(auto.Account)
	method := r.booking[auto.Account]
	bp, errs := bookOne(inv, auto, method, txn.Date, true)
	r.errs = append(r.errs, errs...)
	return bp, len(errs) == 0
}

// fillDeferredCost solves for the per-unit cost that absorbs residual
// across the posting's units, writes it back into deferred.Cost.PerUnit
// (so ResolveCost and PostingWeight see the same number), and re-books
// the posting. A zero unit count or arithmetic failure is reported via
// r.errs and yields ok=false. The deferred posting's account was
// already prepared for edit during Pass 1, so the prepareForEdit call
// here is a fetch.
func (r *Reducer) fillDeferredCost(
	txn *ast.Transaction,
	deferred *ast.Posting,
	residual *ast.Amount,
	trace *stateTrace,
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
	inv := trace.prepareForEdit(deferred.Account)
	bp, errs := bookOne(inv, deferred, r.booking[deferred.Account], txn.Date, false)
	r.errs = append(r.errs, errs...)
	return bp, len(errs) == 0
}

// Run walks the directives without a visitor, returning the booked
// directive output and any collected errors. It is equivalent to
// calling [Reducer.Walk] with a nil visitor.
func (r *Reducer) Run() ([]ast.Directive, []Error) {
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
// After are independent deep copies; Booked entries' Source pointers
// alias the reducer's working clone of the transaction (or the input
// transaction itself when no clone was needed).
type Inspection struct {
	Before map[ast.Account]*Inventory
	After  map[ast.Account]*Inventory
	Booked []BookedPosting
}

// Inspect reconstructs a single transaction's view by re-walking the
// directives sequence from the start until it reaches txn. It is
// intended for bean-doctor-style trouble-shooting; each call costs O(N)
// in the number of directives up to txn.
//
// The txn argument is matched by pointer identity against the
// transactions yielded by the directives sequence. Callers MUST pass
// the exact *ast.Transaction pointer that appears in the input; a
// freshly constructed transaction with equivalent fields will not
// match.
//
// For repeated inspections over a large input, callers should prefer
// [Reducer.Walk] with a visitor that stops at the target transaction.
//
// Returns (nil, errors) if txn is not found in the directives sequence
// or if the walk ended before reaching it. The errors slice always
// contains the errors collected up to (and including) the point where
// the walk stopped.
//
// After Inspect returns, the reducer's internal state reflects the
// directive position immediately after the target transaction, not the
// final state of the input. Every subsequent [Reducer.Walk] or
// [Reducer.Run] call resets the reducer's internal state at entry, so
// invoking Run after Inspect fully restores the final state rather
// than applying additional directives on top of the mid-walk state.
func (r *Reducer) Inspect(txn *ast.Transaction) (*Inspection, []Error) {
	if txn == nil {
		return nil, nil
	}
	var hit *Inspection
	_, errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
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
