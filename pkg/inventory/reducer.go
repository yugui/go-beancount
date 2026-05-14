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

// currencyGroupBucket tracks which entries in a postingResolution's flat
// postings/booked slices belong to a single weight-currency group. Indices
// are offsets into the flat slices; no copies are held. The dropped flag is
// set by addPreserved (bookOne failure) to signal that the entire group
// should be excluded from the final txn.Postings. At this stage the flag
// is recorded but the drop-application pass that acts on it — rebuilding
// txn.Postings to exclude dropped groups — is not yet wired; the dropped
// flag is a no-op until that pass is added.
//
// unknownIdx is populated once solveResidual determines the residual currency
// for an auto-posting or deferred-augment posting; it is left empty until
// that point.
type currencyGroupBucket struct {
	postingsIdx []int // offsets into postingResolution.postings
	bookedIdx   []int // offsets into postingResolution.booked
	unknownIdx  []int // offsets into postingResolution.postings for unknowns joined to this group
	dropped     bool
}

// postingResolution accumulates the per-transaction outcome of routing
// each ast.Posting through bookOne into the rebuilt txn.Postings list
// and the parallel []BookedPosting visitTxn returns. It is the single
// owner of three intertwined concerns that used to be split across a
// "book" pass and a separate "install" pass:
//
//  1. Rebuilding txn.Postings (multi-lot reductions expand into one
//     child per matched lot, so the posting count changes).
//  2. Installing the resolved *ast.Cost on each rebuilt posting under
//     the rules dictated by the booking outcome (lot augmentation,
//     cash augmentation, single-lot reduction with optional
//     cash-sentinel skip, multi-lot expansion, second-run fixed
//     point).
//  3. Constructing the BookedPosting records the visitor will see,
//     with Source pointers aliasing into the rebuilt slice.
//
// Concern (1) is why pointers cannot be assigned eagerly: a later
// append in the same loop may reallocate the postings backing array
// and invalidate any &postings[k] taken earlier. The bookedAt /
// unknownAt offsets defer pointer binding until [finalize], which runs
// after all appends are done.
//
// groups maps a weight-currency key to the bucket of postings,
// BookedPostings, and unknowns belonging to that currency group. The
// flat postings/booked slices remain the source of truth for ordering
// and Source-pointer binding; buckets hold only indices. The map is
// populated by add* methods; the dropped flags are recorded here but
// not yet acted on — the drop-application pass that rebuilds
// txn.Postings to exclude failed groups is a future step.
//
// The zero value is usable; [newPostingResolution] pre-sizes the
// slices for the common no-expansion case.
type postingResolution struct {
	// postings is the rebuilt list of postings for this transaction.
	// visitTxn assigns txn.Postings = postings before calling finalize
	// so BookedPosting.Source pointers alias the caller-visible slice.
	postings []ast.Posting

	// booked is parallel to bookedAt: each entry's Source field is
	// filled by finalize from postings[bookedAt[i]]. Until then the
	// field is left zero.
	booked   []BookedPosting
	bookedAt []int

	// unknownAt holds offsets into postings for the auto-posting and
	// any deferred-augment posting that the residual pass will
	// resolve.
	unknownAt []int

	// groups maps weight-currency key to the currency group bucket for
	// that key. Populated by add* methods. The drop-application pass
	// will read dropped flags to rebuild txn.Postings without failed
	// groups; that pass is not yet implemented.
	groups map[string]*currencyGroupBucket
}

// bucketFor returns the currencyGroupBucket for the given weight-currency key,
// creating it on first access.
func (pr *postingResolution) bucketFor(weightCurrency string) *currencyGroupBucket {
	if pr.groups == nil {
		pr.groups = make(map[string]*currencyGroupBucket)
	}
	b, ok := pr.groups[weightCurrency]
	if !ok {
		b = &currencyGroupBucket{}
		pr.groups[weightCurrency] = b
	}
	return b
}

// newPostingResolution returns a postingResolution whose internal
// slices are pre-sized for the common case where every input posting
// produces exactly one rebuilt posting and one BookedPosting (no
// multi-lot expansion, no bookOne failures). hint should be the
// input transaction's posting count; over-allocation is harmless and
// under-allocation just falls back to append's normal growth.
// unknownAt is left nil: most transactions carry at most one unknown
// and the first append allocates a tight-fit slice anyway.
func newPostingResolution(hint int) postingResolution {
	return postingResolution{
		postings: make([]ast.Posting, 0, hint),
		booked:   make([]BookedPosting, 0, hint),
		bookedAt: make([]int, 0, hint),
	}
}

// addUnknown records p as either the auto-posting or a deferred-augment
// posting. The posting is appended unchanged; the residual pass
// resolves its Amount or Cost from the transaction's residual.
func (pr *postingResolution) addUnknown(p *ast.Posting) {
	pr.postings = append(pr.postings, *p)
	pr.unknownAt = append(pr.unknownAt, len(pr.postings)-1)
}

// addPreserved records a posting whose bookOne returned errors. The
// posting is appended unchanged for the printer; no BookedPosting is
// emitted and the residual pass does not see it. The posting's index is
// registered in the group bucket and the bucket is marked dropped so
// the drop-application pass can exclude the entire currency group from
// txn.Postings. Setting dropped is idempotent: if another posting in
// the same group was already marked dropped, this call re-marks it and
// adds the new index, which is harmless.
func (pr *postingResolution) addPreserved(p *ast.Posting, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	b := pr.bucketFor(weightCurrency)
	b.postingsIdx = append(b.postingsIdx, len(pr.postings)-1)
	b.dropped = true
}

// addAlreadyBooked records a posting that arrived carrying a booked
// *ast.Cost. This is the second-run fixed-point path: bookOne still
// runs to mutate the inventory, but the posting's Cost is preserved
// pointer-identical so a reducer round-trip is byte-identical
// (pinned by TestReducerRun_OutputIsFixedPoint). lot or step may be
// non-nil — they are recorded in the BookedPosting so downstream
// consumers can read the matched lot identity — but no Cost is
// installed on the posting because p.Cost already reflects it.
func (pr *postingResolution) addAlreadyBooked(p *ast.Posting, lot *Lot, step *ReductionStep, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	postIdx := len(pr.postings) - 1
	pr.bookedAt = append(pr.bookedAt, postIdx)
	pr.booked = append(pr.booked, BookedPosting{
		Account:   p.Account,
		Units:     *p.Amount.Clone(),
		Lot:       lot,
		Reduction: step,
	})
	b := pr.bucketFor(weightCurrency)
	b.postingsIdx = append(b.postingsIdx, postIdx)
	b.bookedIdx = append(b.bookedIdx, len(pr.booked)-1)
}

// addLotAugmentation records a first-run augmentation that carried a
// cost spec. The rebuilt posting gets a fresh *ast.Cost cloned from
// lot, replacing the parse-tier *ast.CostSpec; the BookedPosting
// keeps lot in its Lot field so consumers can read it without
// re-resolving.
func (pr *postingResolution) addLotAugmentation(p *ast.Posting, lot *Lot, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	i := len(pr.postings) - 1
	pr.postings[i].Cost = lot.Clone()
	pr.bookedAt = append(pr.bookedAt, i)
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
		Lot:     lot,
	})
	b := pr.bucketFor(weightCurrency)
	b.postingsIdx = append(b.postingsIdx, i)
	b.bookedIdx = append(b.bookedIdx, len(pr.booked)-1)
}

// addCashAugmentation records an augmentation that carries no cost
// spec (a cash-leg posting). The rebuilt posting's Cost holder
// (typically nil) is preserved verbatim — synthesizing a degenerate
// *ast.Cost would change weight semantics for downstream consumers.
func (pr *postingResolution) addCashAugmentation(p *ast.Posting, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	postIdx := len(pr.postings) - 1
	pr.bookedAt = append(pr.bookedAt, postIdx)
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
	})
	b := pr.bucketFor(weightCurrency)
	b.postingsIdx = append(b.postingsIdx, postIdx)
	b.bookedIdx = append(b.bookedIdx, len(pr.booked)-1)
}

// addSingleLotReduction records a reduction whose matcher selected
// exactly one lot. The rebuilt posting gets the matched lot's cost
// installed, except for the cash-sentinel step (zero-value Lot): in
// that case the parse-tier Cost holder is left alone so
// [PostingWeight] falls through to the price branch.
func (pr *postingResolution) addSingleLotReduction(p *ast.Posting, step ReductionStep, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	i := len(pr.postings) - 1
	if step.Lot.Currency != "" || step.Lot.Number.Sign() != 0 {
		pr.postings[i].Cost = step.Lot.Clone()
	}
	pr.bookedAt = append(pr.bookedAt, i)
	pr.booked = append(pr.booked, BookedPosting{
		Account:   p.Account,
		Units:     *p.Amount.Clone(),
		Reduction: &step,
	})
	b := pr.bucketFor(weightCurrency)
	b.postingsIdx = append(b.postingsIdx, i)
	b.bookedIdx = append(b.bookedIdx, len(pr.booked)-1)
}

// addMultiLotReduction expands a reduction that matched multiple lots
// into one child posting per step, each carrying that step's lot and
// the signed magnitude of the step's units (the sign comes from the
// parent posting). Each child is a deep clone of p so siblings do not
// share Meta / Price substructures.
//
// Inventory.Reduce never returns a multi-step result that includes the
// cash sentinel — cost-bearing and cash positions of the same
// commodity cannot coexist on a single inventory row — so step.Lot is
// installed unconditionally here.
//
// Each child is registered under step.Lot.Currency (its weight
// currency), which may differ across steps when the parent posting
// matched lots with different cost currencies (e.g. -20 AAPL {} can
// reduce both AAPL{USD} and AAPL{EUR} lots). step.Lot.Currency is
// always non-empty for multi-step results.
func (pr *postingResolution) addMultiLotReduction(p *ast.Posting, steps []ReductionStep) {
	// step (the range value) is a fresh per-iteration variable in
	// Go 1.22+, so &step is heap-owned by the BookedPosting below and
	// does not alias the caller's steps slice — symmetric with
	// addSingleLotReduction's value-parameter form.
	for _, step := range steps {
		pr.postings = append(pr.postings, p.Clone())
		i := len(pr.postings) - 1
		child := &pr.postings[i]
		child.Amount = &ast.Amount{
			Number:   signedMagnitude(&step.Units, p.Amount.Number.Negative),
			Currency: p.Amount.Currency,
		}
		child.Cost = step.Lot.Clone()
		pr.bookedAt = append(pr.bookedAt, i)
		pr.booked = append(pr.booked, BookedPosting{
			Account:   p.Account,
			Units:     *child.Amount.Clone(),
			Reduction: &step,
		})
		b := pr.bucketFor(step.Lot.Currency)
		b.postingsIdx = append(b.postingsIdx, i)
		b.bookedIdx = append(b.bookedIdx, len(pr.booked)-1)
	}
}

// finalize binds the Source pointers on every BookedPosting and the
// unknown pointers to indices into pr.postings, which by this point
// must be the slice the caller intends to publish (typically already
// assigned to txn.Postings). It must run after every add* call,
// because intermediate appends may have grown the backing array.
func (pr *postingResolution) finalize() (booked []BookedPosting, unknowns []*ast.Posting) {
	booked = pr.booked
	for i, off := range pr.bookedAt {
		booked[i].Source = &pr.postings[off]
	}
	unknowns = make([]*ast.Posting, len(pr.unknownAt))
	for i, off := range pr.unknownAt {
		unknowns[i] = &pr.postings[off]
	}
	return booked, unknowns
}

// visitTxn performs the per-transaction booking pass, mutating the
// reducer's per-account state in place and returning the before/after
// snapshots plus the booked postings. The stop return value is reserved
// for future use (e.g. fatal structural errors); today it is always
// false when the function returns normally.
//
// The body runs two passes:
//
//   - Pass 1 books each explicit posting via [bookOne] and routes the
//     outcome through a [postingResolution] helper that rebuilds
//     txn.Postings, installs the resolved *ast.Cost where needed,
//     expands multi-lot reductions into per-lot children, and
//     constructs the BookedPosting records. Unknowns (auto-posting,
//     deferred augmentation) are recorded by offset for Pass 2.
//   - Pass 2 solves the residual and books the single unknown.
//
// PostingWeight on every booked Source is well-defined for Pass 2's
// residual computation because Pass 1 installs *ast.Cost on every
// posting that needs one before finalize binds Source pointers.
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
	pr := newPostingResolution(len(txn.Postings))

	// Pass 1: classify each posting via bookOne and route the outcome
	// through pr. A posting that fails with CodeAugmentationRequiresCost
	// AND has a cost spec without a number is held back as a deferred
	// unknown (Pass 2 may infer its per-unit cost from the residual).
	// The auto-posting (Amount == nil) is also an unknown. If more
	// than one unknown is collected, the transaction is ambiguous and
	// is reported below.
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			// Auto-posting (validated single & no cost/price by
			// validateStructure).
			pr.addUnknown(p)
			continue
		}

		inv := trace.prepareForEdit(p.Account)
		method := r.booking[p.Account] // zero value = BookingDefault
		lot, steps, errs := bookOne(inv, p, method, txn.Date)
		if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
			pr.addUnknown(p)
			continue
		}
		if len(errs) > 0 {
			r.errs = append(r.errs, errs...)
			pr.addPreserved(p, weightCurrencyFallback(p))
			continue
		}
		switch {
		case p.Cost != nil && p.Cost.IsBooked():
			// Already-booked second-run path: bookOne's tight matcher
			// produces either a single-step reduction or an
			// augmentation lot, never multi-step. The guard is
			// defensive — if the invariant ever broke, firstStepOrNil
			// would silently drop the extra steps.
			if len(steps) > 1 {
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    p.Span,
					Account: p.Account,
					Message: "already-booked posting produced a multi-lot reduction; tight-matcher invariant violated",
				})
				pr.addPreserved(p, weightCurrencyFallback(p))
				continue
			}
			pr.addAlreadyBooked(p, lot, firstStepOrNil(steps), weightCurrencyFallback(p))
		case lot != nil:
			pr.addLotAugmentation(p, lot, lot.Currency)
		case len(steps) == 0:
			pr.addCashAugmentation(p, cashGroupKey(p))
		case len(steps) == 1:
			pr.addSingleLotReduction(p, steps[0], reductionGroupKey(p, steps[0]))
		default:
			pr.addMultiLotReduction(p, steps)
		}
	}

	txn.Postings = pr.postings
	var unknowns []*ast.Posting
	booked, unknowns = pr.finalize()

	// Pass 2: resolve the single unknown, if any.
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
			// not written.
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
		lot, steps, errs := bookOne(inv, unknownP, r.booking[unknownP.Account], txn.Date)
		r.errs = append(r.errs, errs...)
		if len(errs) == 0 {
			if len(steps) > 1 {
				// Multi-lot reductions from the residual pass would
				// need to expand txn.Postings after Source pointers are
				// already bound, which is not safe. The deferred-
				// augment branch installs a tight *ast.Cost so its
				// matcher returns a single lot; the auto-posting branch
				// is typically a cash residual that augments. If we
				// ever land here we want a diagnostic, not a silently-
				// truncated BookedPosting.
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    unknownP.Span,
					Account: unknownP.Account,
					Message: "residual pass produced a multi-lot reduction; expansion is not supported here",
				})
				break
			}
			// The unknown is already at its final offset in txn.Postings,
			// so its address is stable: appending one BookedPosting here
			// does not invalidate any of the Source pointers finalize
			// just bound.
			booked = append(booked, BookedPosting{
				Source:       unknownP,
				Account:      unknownP.Account,
				Units:        *unknownP.Amount.Clone(),
				Lot:          lot,
				Reduction:    firstStepOrNil(steps),
				InferredAuto: inferred,
			})
		}
	}

	before, after = trace.diff()
	return before, after, booked, false
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

// firstStepOrNil returns &steps[0] when steps has exactly one entry,
// and nil otherwise. Callers use it on the already-booked second-run
// path and the residual pass, where the matcher is constrained to a
// single lot identity and bookOne therefore returns 0 or 1 step. Both
// call sites guard against `len(steps) > 1` before invoking this
// helper and emit CodeInternalError diagnostics if the tight-matcher
// invariant were ever broken; this function should never observe
// multi-step input in practice.
func firstStepOrNil(steps []ReductionStep) *ReductionStep {
	if len(steps) != 1 {
		return nil
	}
	return &steps[0]
}

// weightCurrencyFallback returns the weight currency of p using
// PostingWeight, falling back to p.Amount.Currency on error. It is
// used for the already-booked path (p.Cost.IsBooked() is true, so
// PostingWeight uses the cost branch) and for error paths where the
// posting's cost may or may not be set.
//
// The w == nil branch (PostingWeight returns nil, nil) is only reachable
// when p.Amount == nil (auto-posting). All current callers are invoked
// only on postings where p.Amount != nil, so this branch is unreachable
// in practice; the fallback is retained for safety.
func weightCurrencyFallback(p *ast.Posting) string {
	w, err := PostingWeight(p)
	if err != nil || w == nil {
		return p.Amount.Currency
	}
	return w.Currency
}

// cashGroupKey returns the weight currency for a cash-augmentation
// posting (no cost, no lot). Price annotation takes precedence over
// plain amount currency, matching PostingWeight's precedence rules.
func cashGroupKey(p *ast.Posting) string {
	if p.Price != nil {
		return p.Price.Amount.Currency
	}
	return p.Amount.Currency
}

// reductionGroupKey returns the weight currency for a single-lot
// reduction. For non-sentinel lots (cost-bearing), the key is the lot
// currency. For the cash-sentinel step (zero-value Lot), PostingWeight
// falls through to price or amount currency.
func reductionGroupKey(p *ast.Posting, step ReductionStep) string {
	if step.Lot.Currency != "" || step.Lot.Number.Sign() != 0 {
		return step.Lot.Currency
	}
	// Cash-sentinel: no lot installed; use price or amount currency.
	return cashGroupKey(p)
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

// groupToken is an opaque handle returned by [stateTrace.enterGroup].
// It identifies an open group scope and must be passed to
// [stateTrace.commitGroup] to close it.
type groupToken struct{ id uint64 }

// stateTrace records edits to a per-account inventory map within the
// scope of a single transaction. It pairs the long-lived state map
// (shared with the owning Reducer, mutated in place by edits) with a
// before-snapshot map (owned by the trace, populated lazily on first
// touch of each account) so the two stay consistent by construction.
//
// A nil before-value records that the account had no inventory prior
// to this trace — that nil is the visitor-contract signal for "newly
// touched account".
//
// Group checkpoint/rollback: callers may open a group scope via
// [stateTrace.enterGroup], which causes subsequent [stateTrace.prepareForEdit]
// calls to also snapshot into a per-group restore map. [stateTrace.commitGroup]
// files the scope's snapshots under a currency-key, and
// [stateTrace.rollbackGroup] restores every account the group touched to its
// pre-group state.
type stateTrace struct {
	state  map[ast.Account]*Inventory
	before map[ast.Account]*Inventory

	// pending holds snapshots for the currently open group scope. nil
	// when no scope is open. enterGroup allocates it; commitGroup files
	// it into groups and sets it back to nil.
	pending map[ast.Account]*Inventory
	// pendingToken is the token of the currently open scope. It is used
	// to detect misuse (double-enter, commit-without-enter).
	pendingToken groupToken
	// nextTokenID is incremented on each enterGroup to mint unique tokens.
	nextTokenID uint64
	// groups maps weight-currency key → per-account restore snapshots,
	// accumulated across all commit cycles of that group.
	groups map[string]map[ast.Account]*Inventory
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

// enterGroup opens a group checkpoint scope. Until the matching
// commitGroup call, every prepareForEdit that touches an account for
// the first time within this scope records a restore snapshot for that
// account. Returns an opaque token that must be passed to commitGroup.
// Panics if a scope is already open (group scopes do not nest).
func (st *stateTrace) enterGroup() groupToken {
	if st.pending != nil {
		panic("stateTrace.enterGroup: previous group scope was not committed")
	}
	st.nextTokenID++
	tok := groupToken{id: st.nextTokenID}
	st.pending = make(map[ast.Account]*Inventory)
	st.pendingToken = tok
	return tok
}

// commitGroup closes the scope opened by tok and files its per-account
// snapshots under key (the group's weight-currency string). Filing uses
// first-touch-wins: if an account already has a snapshot under key from
// an earlier cycle of the same group, the earlier snapshot is kept
// because it is closer to the group's true pre-state. The snapshots are
// retained (not discarded) so that a later rollbackGroup(key) can undo
// all mutations accumulated across multiple enter/commit cycles.
// Panics if tok does not match the open scope, or if no scope is open.
func (st *stateTrace) commitGroup(tok groupToken, key string) {
	if st.pending == nil {
		panic("stateTrace.commitGroup: no group scope is open")
	}
	if tok != st.pendingToken {
		panic("stateTrace.commitGroup: token does not match the open scope")
	}

	if st.groups == nil {
		st.groups = make(map[string]map[ast.Account]*Inventory)
	}
	bucket, exists := st.groups[key]
	if !exists {
		// First commit for this key: take ownership of the pending map
		// as the bucket. This is the normal path — it avoids a map copy.
		st.groups[key] = st.pending
	} else {
		// Merge pending into the existing bucket with first-touch-wins.
		for acct, snap := range st.pending {
			if _, already := bucket[acct]; !already {
				bucket[acct] = snap
			}
		}
	}
	st.pending = nil
	st.pendingToken = groupToken{}
}

// rollbackGroup undoes every mutation filed under key, restoring each
// affected account's inventory in st.state to the snapshot taken when
// that group first touched it. It is idempotent: calling it with an
// unknown key or a key that has already been rolled back is a no-op.
// rollbackGroup does not touch st.before; the before-map contract is
// unaffected by group rollbacks.
func (st *stateTrace) rollbackGroup(key string) {
	bucket := st.groups[key]
	if bucket == nil {
		return
	}
	for acct, snap := range bucket {
		if snap == nil {
			// The account did not exist before this group touched it.
			// stateTrace never removes an account from st.state once it
			// has been prepared — that is an invariant of this type —
			// so restore with an empty inventory rather than deleting
			// the entry.
			st.state[acct] = NewInventory()
		} else {
			// Clone so that st.state[acct] and the snapshot (which may
			// alias st.before[acct] via the first-touch optimisation in
			// prepareForEdit) remain independent after rollback. This
			// preserves the invariant that live state is always
			// independent of st.before.
			st.state[acct] = snap.Clone()
		}
	}
	delete(st.groups, key)
}

// prepareForEdit returns the inventory to mutate for acct. On the
// first call for a given acct in this trace, it deep-clones the
// account's current inventory into the before-snapshot (or records
// nil if the account had no inventory yet) and lazily creates an
// inventory if one did not exist. Subsequent calls return the same
// inventory pointer without re-snapshotting.
//
// When a group scope is open, prepareForEdit additionally records a
// restore snapshot in the pending map for any account touched for the
// first time within this scope. This does not affect the return value
// or the before-map — group snapshots are purely internal.
func (st *stateTrace) prepareForEdit(acct ast.Account) *Inventory {
	firstTouchInTrace := false
	if _, seen := st.before[acct]; !seen {
		firstTouchInTrace = true
		inv := st.state[acct]
		if inv == nil {
			inv = NewInventory()
			st.state[acct] = inv
			st.before[acct] = nil
		} else {
			st.before[acct] = inv.Clone()
		}
	}

	if st.pending != nil {
		if _, seen := st.pending[acct]; !seen {
			if firstTouchInTrace {
				// This group is also the first toucher in the trace, so
				// st.before[acct] is the correct pre-state. Alias it
				// directly — no extra clone needed.
				st.pending[acct] = st.before[acct]
			} else {
				// Another scope (or the trace itself outside a scope)
				// already touched this account before this group opened.
				// Snapshot the current live state as this group's restore
				// point.
				//
				// This approximation relies on the R1 independence
				// invariant: different currency groups affect disjoint
				// inventory slots, so restoring one group's snapshot does
				// not corrupt mutations from a separately committed group.
				// Known limitation: if two groups share a Position slot
				// (e.g. plain cash and a price-annotated posting on the
				// same commodity), rolling back one group may incorrectly
				// re-surface the other group's mutation. In practice this
				// only arises when the other group fails bookOne, which is
				// uncommon for plain cash postings.
				if inv := st.state[acct]; inv != nil {
					st.pending[acct] = inv.Clone()
				} else {
					st.pending[acct] = nil
				}
			}
		}
	}

	return st.state[acct]
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
// By the time solveResidual runs, [postingResolution] has installed
// *ast.Cost on every booked posting — augmentation in place, single-
// lot reduction in place, multi-lot reduction as per-lot children —
// so [PostingWeight] reads its Cost branch on every entry and yields
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
// Pass 1) using the residual visitTxn derives from the rest of the
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
// residual interpolation.
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
