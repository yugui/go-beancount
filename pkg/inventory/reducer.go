package inventory

import (
	"fmt"
	"iter"
	"time"

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
// reduction) are deep-cloned and the clone is returned in Walk's
// [ast.Directive] output. Other directives, and transactions whose
// booking is provably observation-only, pass through the output by
// reference.
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
// Amount, deferred PerUnit, single-lot reduction Cost) and any
// posting created by multi-lot reduction expansion. The two pointer
// worlds therefore differ when, and only when, the transaction was
// cloned, and on a multi-lot expansion the clone's Postings slice
// also grows past the input length.
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
// reduction is expanded into one posting per matched lot, each
// carrying its own resolved Cost) appears as a clone with the
// mutations applied. Transactions the reducer leaves untouched, and
// all non-Transaction directives, are returned by reference.
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

// postingResolution maintains the state during the first pass of 
// visitTxn: booking explicit postings and collecting unknowns.
type postingResolution struct {
	newPostings []ast.Posting

	// booked holds the BookedPosting records for every postings that successfully booked
	booked         []BookedPosting
	// bookedOffsets holds remembers the offset in newPostings for each booked posting's Source, so the Source pointer can be updated after the loop without searching by pointer identity. The two slices are parallel and append-only, so the offset is always len(newPostings)-1 at the time of booking.
	bookedOffsets []int

	unknowns []int
}

func (p1 *postingResolution) addUnknown(p ast.Posting) {
	p1.newPostings = append(p1.newPostings, p)
	p1.unknowns = append(p1.unknowns, len(p1.newPostings)-1)
}

func (p1 *postingResolution) addBooked(p ast.Posting, aug *ast.Lot, steps []ReductionStep) {
	p1.newPostings = append(p1.newPostings, p)
	p1.bookedOffsets = append(p1.bookedOffsets, len(p1.newPostings)-1)
	p1.booked = append(p1.booked, BookedPosting{
		Account: p.Account,
		Units: *p.Amount.Clone(),
		Lot: aug,
		Reductions: steps,
		InferredAuto: false,
	})
}

func (p1 *postingResolution) addLotAugmentation(p ast.Posting, aug *ast.Lot) {
	p1.newPostings = append(p1.newPostings, p)
	i := len(p1.newPostings) - 1
	p1.newPostings[i].Cost = aug.Clone()
	p1.bookedOffsets = append(p1.bookedOffsets, i)
	p1.booked = append(p1.booked, BookedPosting{
		Account: p.Account,
		Units: *p.Amount.Clone(),
		Lot: aug,
		Reductions: nil,
		InferredAuto: false,
	})
}

func (p1 *postingResolution) addCashAugmentation(p ast.Posting) {
	p1.newPostings = append(p1.newPostings, p)
	p1.bookedOffsets = append(p1.bookedOffsets, len(p1.newPostings)-1)
	p1.booked = append(p1.booked, BookedPosting{
		Account: p.Account,
		Units: *p.Amount.Clone(),
		Lot: nil,
		Reductions: nil,
		InferredAuto: false,
	})
}

func (p1 *postingResolution) addSingleLotReduction(p ast.Posting, r ReductionStep) {
	p1.newPostings = append(p1.newPostings, p)
	i := len(p1.newPostings) - 1

	lot := r.Lot
	if lot.Currency != "" || lot.Number.Sign() != 0 {
		p1.newPostings[i].Cost = lot.Clone()
	}

	p1.bookedOffsets = append(p1.bookedOffsets, i)
	p1.booked = append(p1.booked, BookedPosting{
		Account: p.Account,
		Units: *p.Amount.Clone(),
		Lot: nil,
		Reductions: []ReductionStep{r},
		InferredAuto: false,
	})
}

func (p1 *postingResolution) addMultiLotReduction(p ast.Posting, r ReductionStep) {
	p1.newPostings = append(p1.newPostings, p)
	i := len(p1.newPostings) - 1

	child := &p1.newPostings[i]
	child.Amount = &ast.Amount{
		Number:   signedMagnitude(&r.Units, p.Amount.Number.Negative),
		Currency: p.Amount.Currency,
	}
	child.Cost = r.Lot.Clone()
	p1.bookedOffsets = append(p1.bookedOffsets, i)
	p1.booked = append(p1.booked, BookedPosting{
		Account:    p.Account,
		Units:      *child.Amount.Clone(),
		Reductions: []ReductionStep{r},
	})
}

// finalize returns the booked postings with their Source pointers updated to point into the newPostings slice, and the unknowns remapped to point into newPostings as well. This must be called after all calls to addBooked/addLotAugmentation/addSingleLotReduction/addMultiLotReduction/addUnknown are done, and before any of the booked postings or unknowns are passed to Pass 3, so that the Source pointers are correct for solveResidual and any subsequent bookOne calls.
func (p1 *postingResolution) finalize() (booked []BookedPosting, unknowns []*ast.Posting) {
	booked = make([]BookedPosting, len(p1.booked))
	for i, bp := range p1.booked {
		offset := p1.bookedOffsets[i]
		bp.Source = &p1.newPostings[offset]
		booked[i] = bp
	}
	unknowns = make([]*ast.Posting, len(p1.unknowns))
	for i, offset := range p1.unknowns {
		unknowns[i] = &p1.newPostings[offset]
	}
	return booked, unknowns
}


// needsBookingClone reports whether txn could be mutated by the booking
// pass and therefore must be cloned before its postings are handed to
// the booking machinery. The pass fills auto-posting amounts and may
// resolve parse-tier *ast.CostSpec into booked *ast.Cost values; a
// transaction whose postings all carry an Amount and either have no
// cost or already hold a booked *ast.Cost is observationally
// unchanged by Walk and reuses its pointer in the output. The
// IsBooked check is what makes a second reducer run over its own
// output skip the clone — the booked variant is not mutated again.
func needsBookingClone(txn *ast.Transaction) bool {
	for _, p := range txn.Postings {
		if p.Amount == nil {
			return true
		}
		if p.Cost != nil && !p.Cost.IsBooked() {
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
//
// The body runs three passes:
//
//   - Pass 1 books explicit postings against inventory state and
//     collects unknowns (the auto-posting and any deferred-augment
//     posting held back from booking). No *ast.Cost is installed
//     here; that is Pass 2's responsibility.
//   - Pass 2 ([resolveBookedCosts]) installs *ast.Cost on every
//     booked posting: an augmentation gets bp.Lot in place, a
//     reduction matching a single non-cash lot gets the matched
//     step.Lot in place, and a reduction matching multiple lots is
//     replaced by one posting per matched lot, each carrying that
//     lot's *ast.Cost.
//   - Pass 3 solves the residual and books the single unknown.
//
// Pass 2 runs before Pass 3 because Pass 3's residual computation
// reads PostingWeight on each booked Source; after Pass 2 every
// booked Source carries a concrete *ast.Cost and the weight is
// well-defined per posting without any multi-lot branching.
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
	pr := new(postingResolution)

	// Pass 1: book explicit postings while collecting unknowns. A
	// posting that fails with CodeAugmentationRequiresCost AND has a
	// cost spec without a number is held back as a deferred unknown
	// (Pass 3 may infer its per-unit cost from the residual). The
	// auto-posting (Amount == nil) is also an unknown. If more than
	// one unknown is collected, the transaction is ambiguous and is
	// reported below.
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			// Auto-posting (validated single & no cost/price by
			// validateStructure).
			pr.addUnknown(*p)
			continue
		}

		inv := trace.prepareForEdit(p.Account)
		method := r.booking[p.Account] // zero value = BookingDefault
		aug, steps, errs := bookOne(inv, p, method, txn.Date)
		if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
			pr.addUnknown(*p)
			continue
		}
		r.errs = append(r.errs, errs...)
		// TODO(failed-posting-removal): upstream beancount removes the
		// entire currency group from txn.Postings when book_reductions
		// returns errors. The Go reducer currently keeps the failing
		// posting with its original parse-tier *ast.CostSpec. Matching
		// upstream requires structurally removing failing postings
		// here; this is a wider semantic change than the current slice
		// scope.
		if len(errs) > 0 {
			continue
		}
		if p.Cost != nil && !p.Cost.IsBooked() {
			pr.addBooked(*p, aug, steps)
			continue
		}

		if aug != nil {
			pr.addLotAugmentation(*p, aug)
			continue
		}
		switch len(steps) {
		case 0:
			pr.addCashAugmentation(*p)
		case 1:
			pr.addSingleLotReduction(*p, steps[0])
		default:
			for _, r := range steps {
				pr.addMultiLotReduction(*p, r)
			}
		}
	}

	txn.Postings = pr.newPostings
	var unknowns []*ast.Posting
	booked, unknowns = pr.finalize()

	// Pass 3: resolve the single unknown, if any.
	switch {
	case len(unknowns) > 1:
		// Ambiguous: too many unknowns for a single residual to pin
		// down. Emit one diagnostic per unknown so the user sees every
		// site they need to fix.
		r.flagAmbiguousUnknowns(unknowns)
	case len(unknowns) == 1:
		unknownP := unknowns[0]
		residual, ok := r.solveResidual(booked, unknownP)
		if !ok {
			break // solveResidual already appended the diagnostic
		}
		inferred := unknownP.Amount == nil
		if inferred {
			// Auto-posting: write the inferred Amount. validateStructure
			// guarantees unknownP.Cost is nil, and this branch must
			// preserve that — even if bookOne below ends up matching a
			// held-at-cost lot, the auto-posting's Cost is deliberately
			// not written. (See the no-install rationale next to the
			// Pass 1 install block above; we apply it here by omission.)
			unknownP.Amount = residual
		} else if err := r.resolveCostFromResidual(unknownP, residual, txn.Date); err != nil {
			r.errs = append(r.errs, *err)
			break
		}
		// The deferred branch pre-installed *ast.Cost above, so bookOne
		// takes the ResolveCost(*ast.Cost) short-circuit path; the
		// auto-posting branch left Cost nil. No post-bookOne install is
		// needed in either case.
		inv := trace.prepareForEdit(unknownP.Account)
		aug, steps, errs := bookOne(inv, unknownP, r.booking[unknownP.Account], txn.Date)
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			booked = append(booked, BookedPosting{
				Source: unknownP,
				Account: unknownP.Account,
				Units: *unknownP.Amount.Clone(),
				Lot: aug,
				Reductions: steps,
				InferredAuto: inferred,
			})
		}
	}

	before, after = trace.diff()
	return before, after, booked, false
}

// resolveBookedCosts installs the resolved *ast.Cost on every booked
// posting and rebuilds txn.Postings to interleave per-lot children at
// the position of each multi-lot reducing parent.
//
// The shape of the install depends on bp, dispatched per booked entry:
//
//   - Posting already booked (Cost is *ast.Cost): second-run fixed
//     point. Pass through with Source rebound; do not re-install.
//   - Augmentation (bp.Lot != nil): install bp.Lot.Clone() on the
//     pass-through posting.
//   - Cash augmentation (bp.Lot == nil, no reductions): pass through
//     with no Cost install. The posting's parse-tier Cost holder
//     (typically nil for cash lines) is preserved.
//   - Single-lot reduction (len(bp.Reductions) == 1): install
//     step.Lot.Clone() on the pass-through posting. A cash-sentinel
//     step (zero-value Lot) leaves the posting's Cost holder
//     untouched so PostingWeight falls through to the price branch.
//   - Multi-lot reduction (len(bp.Reductions) > 1): replace the
//     parent posting with one child per matched lot. Each child
//     carries that step's *ast.Cost and the signed magnitude of
//     the step's units. step.Lot is installed unconditionally
//     here — Inventory.Reduce never returns a multi-step result
//     that includes a cash sentinel, because cost-bearing and
//     cash positions of the same commodity cannot coexist on a
//     single inventory row.
//
// txn.Postings is always rebuilt. BookedPosting.Source pointers in
// the returned newBooked alias the new txn.Postings; unknowns is
// remapped through the same rewrite so the caller's Pass 3 receives
// pointers into the new slice. A posting that is in neither booked
// nor unknowns (e.g. a posting whose bookOne failed) is preserved in
// txn.Postings as-is, with no Cost install and no entry in newBooked
// or newUnknowns.
//
// On a second reducer run over its own output every booked posting
// already carries *ast.Cost and takes the alreadyBooked branch
// above; bookOne's tight NewCostMatcher re-selects exactly the same
// lot so every reduction is single-step. txn.Postings is still
// rebuilt (deterministic, order-preserving), so the output is
// byte-identical to the input. This is what makes
// TestReducerRun_OutputIsFixedPoint hold.
func resolveBookedCosts(
	txn *ast.Transaction,
	booked []BookedPosting,
	unknowns []*ast.Posting,
) (newBooked []BookedPosting, newUnknowns []*ast.Posting) {
	// Sweep booked once to compute the rebuilt-slice capacity and the
	// Source-pointer reverse map.
	extra := 0
	bySource := make(map[*ast.Posting]int, len(booked))
	for i, bp := range booked {
		bySource[bp.Source] = i
		if n := len(bp.Reductions); n > 1 {
			extra += n - 1
		}
	}
	// unknownSet distinguishes auto-posting / deferred-augment entries
	// (handed to Pass 3) from bookOne-failed entries (preserved but
	// not given to Pass 3). Without this set the !hasBP branch below
	// could not tell them apart, since both are absent from booked.
	unknownSet := make(map[*ast.Posting]struct{}, len(unknowns))
	for _, u := range unknowns {
		unknownSet[u] = struct{}{}
	}

	// newPostings is pre-sized to its final capacity so append never
	// reallocates and &newPostings[i] is stable for the slice's
	// lifetime — load-bearing because BookedPosting.Source and the
	// unknown remap both alias into it.
	newPostings := make([]ast.Posting, 0, len(txn.Postings)+extra)
	newBooked = make([]BookedPosting, 0, len(booked)+extra)
	newUnknowns = make([]*ast.Posting, 0, len(unknowns))

	for i := range txn.Postings {
		old := &txn.Postings[i]

		bIdx, hasBP := bySource[old]
		if !hasBP {
			// Unknown (Pass 3) or bookOne-failed (printer-only).
			newPostings = append(newPostings, *old)
			if _, isUnknown := unknownSet[old]; isUnknown {
				newUnknowns = append(newUnknowns, &newPostings[len(newPostings)-1])
			}
			continue
		}
		bp := booked[bIdx]

		if old.Cost != nil && old.Cost.IsBooked() {
			// Already booked.
			newPostings = append(newPostings, *old)
			bp.Source = &newPostings[len(newPostings)-1]
			newBooked = append(newBooked, bp)
			continue
		}

		if bp.Lot != nil {
			// Augmentation.
			newPostings = append(newPostings, *old)
			newP := &newPostings[len(newPostings)-1]
			newP.Cost = bp.Lot.Clone()
			bp.Source = newP
			newBooked = append(newBooked, bp)
			continue
		}

		switch len(bp.Reductions) {
		case 0:
			// Cash augmentation.
			newPostings = append(newPostings, *old)
			bp.Source = &newPostings[len(newPostings)-1]
			newBooked = append(newBooked, bp)
		case 1:
			// Single-lot reduction.
			newPostings = append(newPostings, *old)
			newP := &newPostings[len(newPostings)-1]
			lot := &bp.Reductions[0].Lot
			if lot.Currency != "" || lot.Number.Sign() != 0 {
				newP.Cost = lot.Clone()
			}
			bp.Source = newP
			newBooked = append(newBooked, bp)
		default:
			// Multi-lot expansion.
			for s := range bp.Reductions {
				step := &bp.Reductions[s]
				newPostings = append(newPostings, old.Clone())
				child := &newPostings[len(newPostings)-1]
				child.Amount = &ast.Amount{
					Number:   signedMagnitude(&step.Units, old.Amount.Number.Negative),
					Currency: old.Amount.Currency,
				}
				child.Cost = step.Lot.Clone()
				newBooked = append(newBooked, BookedPosting{
					Source:     child,
					Account:    bp.Account,
					Units:      *child.Amount.Clone(),
					Reductions: []ReductionStep{*step},
				})
			}
		}
	}
	txn.Postings = newPostings
	return newBooked, newUnknowns
}

// signedMagnitude returns a copy of magnitude whose Negative flag
// matches the negative argument. [ReductionStep.Units] holds a
// non-negative magnitude per the reducer contract; a child posting
// produced by expanding a multi-lot reduction must carry the signed
// units of the parent posting that produced the reduction. The
// clone is necessary so the child owns its coefficient buffer
// independent of step.Units, which the booking layer continues to
// read.
func signedMagnitude(magnitude *apd.Decimal, negative bool) apd.Decimal {
	n := *ast.CloneDecimal(magnitude)
	if negative {
		n.Negative = true
	}
	return n
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
// At this point Pass 2 ([resolveBookedCosts]) has installed *ast.Cost
// on every booked posting — augmentation in place, single-lot
// reduction in place, multi-lot reduction as per-lot children — so
// [PostingWeight] reads its Cost branch on every entry and yields
// the same exact figure that [pkg/validation] will see when it
// checks transaction balance. There is no separate booked-only
// weight path; the reducer and validation share a single
// formula.
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
		w, err := PostingWeight(bp.Source)
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
		if existing, found := sums[w.Currency]; found {
			if _, err := apd.BaseContext.Add(existing, existing, &w.Number); err != nil {
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    bp.Source.Span,
					Account: bp.Account,
					Message: "interpolate: accumulate weight: " + err.Error(),
				})
				return nil, false
			}
		} else {
			sums[w.Currency] = &w.Number
			order = append(order, w.Currency)
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

// resolveCostFromResidual constructs the booked *ast.Cost for a
// deferred-augment posting (one written as `{}` and held back from
// Pass 1) using the residual Pass 3 derives from the rest of the
// transaction. The synthesized Cost is installed on p.Cost in place of the
// parse-tier *ast.CostSpec, so the subsequent bookOne call takes the
// ResolveCost(*ast.Cost) short-circuit branch.
//
// Number is residual / |p.Amount| at the divider's full precision;
// Total retains residual verbatim so PostingWeight's Total branch
// reproduces the user-paid amount without precision loss. Date and
// Label are inherited from the parse-tier *ast.CostSpec when set,
// otherwise Date falls back to the transaction date (matching
// ResolveCost's default for spec.Date == nil).
//
// A zero-unit posting or apd.Decimal arithmetic failure is reported
// as an *Error so the caller can append it to r.errs and abort the
// Pass 3 interpolation.
func (r *Reducer) resolveCostFromResidual(p *ast.Posting, residual *ast.Amount, txnDate time.Time) *Error {
	if p.Amount.Number.Sign() == 0 {
		return &Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    p.Span,
			Account: p.Account,
			Message: "deferred cost cannot be interpolated: posting has zero units",
		}
	}
	absUnits := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(absUnits, &p.Amount.Number); err != nil {
		return &Error{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "interpolate: abs units: " + err.Error(),
		}
	}
	var perUnit apd.Decimal
	if _, err := quoContext.Quo(&perUnit, &residual.Number, absUnits); err != nil {
		return &Error{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "interpolate: divide residual by units: " + err.Error(),
		}
	}
	date := txnDate
	var label string
	if spec, ok := p.Cost.(*ast.CostSpec); ok && spec != nil {
		if spec.Date != nil {
			date = *spec.Date
		}
		label = spec.Label
	}
	p.Cost = &ast.Cost{
		Number:   perUnit,
		Currency: residual.Currency,
		Date:     date,
		Label:    label,
		Total:    &ast.Amount{Number: *ast.CloneDecimal(&residual.Number), Currency: residual.Currency},
	}
	return nil
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
