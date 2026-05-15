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

// groupRef pairs a posting's index in [postingResolution.postings] with
// the weight-currency key of its booking group. For unknowns, currency
// is the candidate stamped at insertion time by
// [postingResolution.addUnknown] — non-empty when the posting commits
// to a specific currency ({ CUR } deferred, price-annotated auto), ""
// for fully free unknowns. Pass 2 overwrites it with the resolved
// currency once the residual is bound (a committed-group success keeps
// the same value; an applyResidualToUnknown failure path still stamps
// it so finalize can match the entry).
type groupRef struct {
	currency  string // weight currency
	postingAt int    // index into postingResolution.postings
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
// and invalidate any &postings[k] taken earlier. bookedDesc / unknownDesc
// carry posting offsets into postings; Source pointers stay nil on
// pr.booked until [postingResolution.finalize] runs after Pass 2 (and
// after any drop-application rebuild of pr.postings). Callers never
// observe a half-bound BookedPosting: completeness is pr's invariant.
//
// dropped records weight-currency keys whose currency group failed
// bookOne. It is nil for error-free transactions. finalize uses it to
// exclude failed groups when rebuilding pr.postings and to drive the
// inverse-booking rollback.
//
// The zero value is usable; [newPostingResolution] pre-sizes the
// slices for the common no-expansion case.
type postingResolution struct {
	// postings is the rebuilt list of postings for this transaction.
	// finalize binds Source pointers to addresses within this backing
	// array; on drop, finalize rebuilds the slice to exclude failed
	// groups and binds Source on survivors only. txn.Postings =
	// pr.postings is assigned by visitTxn after finalize returns.
	postings []ast.Posting

	// booked holds the BookedPosting records whose Source fields stay
	// nil until [postingResolution.finalize] binds them. Each entry is
	// otherwise complete (Account, Units, Lot, Reduction, InferredAuto
	// already populated by the add* method or by Pass 2).
	booked []BookedPosting

	// bookedDesc is parallel to booked: bookedDesc[j].postingAt is the
	// index into postings whose address becomes booked[j].Source, and
	// bookedDesc[j].currency is the weight-currency group key for that
	// entry.
	bookedDesc []groupRef

	// unknownDesc is parallel to the unknown postings (auto-posting and
	// deferred-augment). currency carries the candidate weight currency
	// stamped at insertion time by addUnknown via
	// unknownCandidateCurrency: non-empty for committed unknowns
	// ({ CUR } deferred, price-annotated), "" for free ones. Pass 2
	// overwrites it with the resolved currency once a residual is bound
	// (committed-group success keeps the same value). postingAt indexes
	// into postings.
	unknownDesc []groupRef

	// dropped is the set of weight-currency keys whose bookOne call
	// failed. nil until the first failure. The drop-application pass
	// reads this to rebuild txn.Postings without failed groups and to
	// apply inverse bookings to roll back each dropped currency.
	dropped map[string]bool
}

// newPostingResolution returns a postingResolution whose internal
// slices are pre-sized for the common case where every input posting
// produces exactly one rebuilt posting and one BookedPosting (no
// multi-lot expansion, no bookOne failures). hint should be the
// input transaction's posting count; over-allocation is harmless and
// under-allocation just falls back to append's normal growth.
// unknownDesc and dropped are left nil: most transactions carry at most
// one unknown and zero failures; the first use allocates a tight-fit
// slice or map respectively.
func newPostingResolution(hint int) postingResolution {
	return postingResolution{
		postings:   make([]ast.Posting, 0, hint),
		booked:     make([]BookedPosting, 0, hint),
		bookedDesc: make([]groupRef, 0, hint),
	}
}

// addUnknown records p as either the auto-posting or a deferred-augment
// posting. The posting is appended unchanged; the residual pass
// resolves its Amount or Cost from the transaction's residual. The
// descriptor currency is the candidate weight currency from
// [unknownCandidateCurrency] — non-empty when Pass 1 can already
// commit the unknown to a specific currency (cost-spec currency or
// price annotation), "" otherwise. Pass 2 overwrites it with the
// resolved currency once a residual is bound.
func (pr *postingResolution) addUnknown(p *ast.Posting) {
	pr.postings = append(pr.postings, *p)
	pr.unknownDesc = append(pr.unknownDesc, groupRef{
		currency:  unknownCandidateCurrency(p),
		postingAt: len(pr.postings) - 1,
	})
}

// markForDrop records the given weight-currency group as dropped. The
// failed posting is not appended to pr.postings — it will not appear in
// the output or in the residual computation. The dropped map is lazily
// initialized on first use so error-free transactions pay no allocation
// cost. Marking is idempotent: re-marking an already-dropped currency
// is a no-op.
//
// Callers must also call trace.prepareForRollback for every account
// involved in the failure, to ensure the state-diff pass can apply its
// exclusion rule (state == before → suppress) even when the booking
// failed before mutating the inventory.
func (pr *postingResolution) markForDrop(weightCurrency string) {
	if pr.dropped == nil {
		pr.dropped = make(map[string]bool)
	}
	pr.dropped[weightCurrency] = true
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
	pr.booked = append(pr.booked, BookedPosting{
		Account:   p.Account,
		Units:     *p.Amount.Clone(),
		Lot:       lot,
		Reduction: step,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: len(pr.postings) - 1})
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
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
		Lot:     lot,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: i})
}

// addCashAugmentation records an augmentation that carries no cost
// spec (a cash-leg posting). The rebuilt posting's Cost holder
// (typically nil) is preserved verbatim — synthesizing a degenerate
// *ast.Cost would change weight semantics for downstream consumers.
func (pr *postingResolution) addCashAugmentation(p *ast.Posting, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: len(pr.postings) - 1})
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
	pr.booked = append(pr.booked, BookedPosting{
		Account:   p.Account,
		Units:     *p.Amount.Clone(),
		Reduction: &step,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: i})
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
		pr.booked = append(pr.booked, BookedPosting{
			Account:   p.Account,
			Units:     *child.Amount.Clone(),
			Reduction: &step,
		})
		pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: step.Lot.Currency, postingAt: i})
	}
}

// bookedPostings returns the live addresses of every booked posting
// in input order. The result aliases pr.postings; callers must not
// retain it past a finalize that rebuilds the backing array. Used by
// Pass 2 to feed [Reducer.sumBookedWeights] without exposing the
// (still-incomplete) pr.booked slice.
func (pr *postingResolution) bookedPostings() []*ast.Posting {
	out := make([]*ast.Posting, len(pr.bookedDesc))
	for i, ref := range pr.bookedDesc {
		out[i] = &pr.postings[ref.postingAt]
	}
	return out
}

// unknownGroup is one (weight currency, postings) bucket produced by
// [postingResolution.groupForResidual]. Pass 2 solves the per-currency
// residual against booked alone and applies it to the single unknown
// in unknown; if unknown carries more than one entry the group is
// reported as ambiguous and dropped. booked and unknown are aliases
// into pr.postings — read-only during Pass 2.
type unknownGroup struct {
	currency string         // weight currency; non-empty for committed groups
	booked   []*ast.Posting // booked postings whose weight currency == currency
	unknown  []*ast.Posting // unknown postings whose candidate currency == currency
}

// groupForResidual partitions Pass 1's output into committed groups
// and free unknowns. A committed group covers one non-empty weight
// currency with at least one bidding unknown; its booked slice lists
// every booked posting whose weight currency matches. Groups with no
// bidding unknown are omitted (the validator owns the unbalanced
// case). free holds unknown postings whose candidate currency is "".
// Together committed[].unknown ∪ free covers every pr.unknownDesc
// entry exactly once; committed order is first-appearance.
func (pr *postingResolution) groupForResidual() (committed []unknownGroup, free []*ast.Posting) {
	if len(pr.unknownDesc) == 0 {
		return nil, nil
	}

	// Bucket unknowns by candidate currency, recording first-appearance
	// order so the returned committed slice is deterministic.
	bid := make(map[string][]*ast.Posting)
	var order []string
	for _, ref := range pr.unknownDesc {
		p := &pr.postings[ref.postingAt]
		if ref.currency == "" {
			free = append(free, p)
			continue
		}
		if _, seen := bid[ref.currency]; !seen {
			order = append(order, ref.currency)
		}
		bid[ref.currency] = append(bid[ref.currency], p)
	}
	if len(bid) == 0 {
		return nil, free
	}

	// Group bookings by weight currency, but only for currencies the
	// unknown bid set claims.
	booked := make(map[string][]*ast.Posting, len(bid))
	for _, ref := range pr.bookedDesc {
		if _, want := bid[ref.currency]; !want {
			continue
		}
		booked[ref.currency] = append(booked[ref.currency], &pr.postings[ref.postingAt])
	}

	committed = make([]unknownGroup, 0, len(order))
	for _, cur := range order {
		committed = append(committed, unknownGroup{
			currency: cur,
			booked:   booked[cur],
			unknown:  bid[cur],
		})
	}
	return committed, free
}

// unknownCandidateCurrency returns the weight currency a still-unknown
// posting will absorb in Pass 2's residual solve, or "" when the
// posting itself does not pin one. Precedence:
//
//  1. p.Cost != nil && p.Cost.GetCurrency() != "" → that currency.
//  2. p.Price != nil → p.Price.Amount.Currency.
//  3. otherwise "".
func unknownCandidateCurrency(p *ast.Posting) string {
	if p.Cost != nil {
		if cur := p.Cost.GetCurrency(); cur != "" {
			return cur
		}
	}
	if p.Price != nil {
		return p.Price.Amount.Currency
	}
	return ""
}

// finalize closes the per-transaction resolution: it applies any
// currency-group drops, binds Source pointers on the survivors, and
// returns the complete []BookedPosting. After this call pr.postings
// reflects only the surviving postings (caller assigns
// txn.Postings = pr.postings); pr is not used further.
//
// Hot path: if pr.dropped is empty, finalize binds Source on every
// pr.booked entry against the current pr.postings backing array (which
// is stable past Pass 1) and returns. No allocation beyond the slice
// header.
//
// Drop path: pr.postings is rebuilt in input order to exclude every
// currency group in pr.dropped, inverse bookings are applied for each
// dropped entry (recorded against trace via prepareForRollback so the
// state-diff pass observes the rollback), and Source on survivors is
// bound to the rebuilt slice. Survival is "currency not in pr.dropped";
// the "" key is never in pr.dropped, so Pass 2-unresolved free unknowns
// always survive. Failed postings were never appended to pr.postings
// (markForDrop does not append), so they need no separate exclusion.
//
// Errors from reverseBooking are CodeInternalError from apd arithmetic
// and do not occur for normal ledger inputs.
func (pr *postingResolution) finalize(trace *stateTrace, r *Reducer) []BookedPosting {
	if len(pr.dropped) == 0 {
		booked := pr.booked
		for i, ref := range pr.bookedDesc {
			booked[i].Source = &pr.postings[ref.postingAt]
		}
		return booked
	}

	survives := make([]bool, len(pr.postings))
	for _, ref := range pr.bookedDesc {
		if !pr.dropped[ref.currency] {
			survives[ref.postingAt] = true
		}
	}
	for _, ref := range pr.unknownDesc {
		if !pr.dropped[ref.currency] {
			survives[ref.postingAt] = true
		}
	}

	newIdx := make([]int, len(pr.postings))
	for i := range newIdx {
		newIdx[i] = -1
	}
	newPostings := make([]ast.Posting, 0, len(pr.postings))
	for i, p := range pr.postings {
		if survives[i] {
			newIdx[i] = len(newPostings)
			newPostings = append(newPostings, p)
		}
	}
	pr.postings = newPostings

	out := make([]BookedPosting, 0, len(pr.booked))
	for j, ref := range pr.bookedDesc {
		if pr.dropped[ref.currency] {
			inv := trace.prepareForRollback(pr.booked[j].Account)
			if err := reverseBooking(inv, pr.booked[j]); err != nil {
				r.errs = append(r.errs, asError(err, pr.booked[j].Account))
			}
			continue
		}
		bp := pr.booked[j]
		bp.Source = &pr.postings[newIdx[ref.postingAt]]
		out = append(out, bp)
	}
	return out
}

// asError returns err as an inventory Error: an existing Error passes
// through, anything else becomes CodeInternalError on account.
func asError(err error, account ast.Account) Error {
	if typed, ok := err.(Error); ok {
		return typed
	}
	return Error{Code: CodeInternalError, Account: account, Message: err.Error()}
}

// reverseBooking undoes a BookedPosting's effect on inv with the
// inverse Inventory.Add. An augmentation (bp.Reduction == nil) has its
// units negated; a reduction restores bp.Reduction.Units to the lot
// (the cash sentinel maps to a nil Cost so the cash slot merges).
//
// inv must be the live inventory for bp.Account, obtained via
// trace.prepareForRollback so diff() records the rollback. Errors are
// CodeInternalError from apd arithmetic and do not occur for normal
// ledger inputs.
func reverseBooking(inv *Inventory, bp BookedPosting) error {
	if bp.Reduction == nil {
		var neg apd.Decimal
		if _, err := apd.BaseContext.Neg(&neg, &bp.Units.Number); err != nil {
			return Error{
				Code:    CodeInternalError,
				Account: bp.Account,
				Message: "reverseBooking: negate augmentation units: " + err.Error(),
			}
		}
		return inv.Add(Position{
			Units: ast.Amount{Number: neg, Currency: bp.Units.Currency},
			Cost:  bp.Lot,
		})
	}
	var cost *Cost
	if bp.Reduction.Lot.Currency != "" || bp.Reduction.Lot.Number.Sign() != 0 {
		// Non-sentinel lot: clone to avoid aliasing the step's Lot value.
		lotCopy := bp.Reduction.Lot
		cost = lotCopy.Clone()
	}
	return inv.Add(Position{
		Units: ast.Amount{
			Number:   *ast.CloneDecimal(&bp.Reduction.Units), // positive magnitude; no sign flip
			Currency: bp.Units.Currency,                      // consumed commodity, from the posting's amount
		},
		Cost: cost,
	})
}

// visitTxn books a single transaction: it mutates the reducer's
// per-account inventory and returns the before/after snapshots and the
// surviving BookedPosting records. stop is reserved for future use; it
// is always false in the current implementation.
//
// A structurally-invalid transaction is rejected with a diagnostic without
// touching any inventory.
//
// Booking runs in two passes. Pass 1 books every explicit posting and
// stamps each unknown with a candidate weight currency (cost-spec
// currency or price annotation, "" otherwise). Pass 2 partitions the
// unknowns by candidate currency and resolves each committed group
// against its own per-currency residual; a single free unknown
// (candidate "") absorbs whatever currency remains unclaimed. Two
// unknowns sharing the same weight currency, or two free unknowns,
// remain ambiguous and yield one diagnostic per unknown.
//
// When a posting's booking fails, its whole weight-currency group is
// dropped: every posting sharing that currency is removed from
// txn.Postings and from the returned BookedPosting slice, and the
// group's inventory mutations are rolled back. Other currency groups in
// the same transaction are unaffected, and the transaction is still
// emitted even if every group was dropped. A posting that fails only
// because its cost number is deferred is the exception — it is retried
// in Pass 2 rather than dropped.
func (r *Reducer) visitTxn(txn *ast.Transaction) (
	before map[ast.Account]*Inventory,
	after map[ast.Account]*Inventory,
	booked []BookedPosting,
	stop bool,
) {
	// Reject structurally-invalid input.
	if !r.validateStructure(txn) {
		return map[ast.Account]*Inventory{}, nil, nil, false
	}

	trace := newStateTrace(r.state)
	pr := newPostingResolution(len(txn.Postings))

	// Pass 1: book each explicit posting; hold auto/deferred postings as unknowns.
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			// Auto-posting: booked in Pass 2 once the residual is known.
			pr.addUnknown(p)
			continue
		}

		inv := trace.prepareForEdit(p.Account)
		method := r.booking[p.Account] // zero value = BookingDefault
		lot, steps, errs := bookOne(inv, p, method, txn.Date)
		if len(errs) == 1 && errs[0].Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
			// Deferred cost: retried in Pass 2 (bookOne returned before mutating inventory).
			pr.addUnknown(p)
			continue
		}
		if len(errs) > 0 {
			r.errs = append(r.errs, errs...)
			pr.markForDrop(weightCurrencyFallback(p))
			trace.prepareForRollback(p.Account)
			continue
		}
		switch {
		case p.Cost != nil && p.Cost.IsBooked():
			// Second-run fixed-point path. Defensive guard: tight matcher
			// must not return multi-step; a violation would otherwise silently truncate.
			key := weightCurrencyFallback(p)
			if len(steps) > 1 {
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    p.Span,
					Account: p.Account,
					Message: "already-booked posting produced a multi-lot reduction; tight-matcher invariant violated",
				})
				pr.markForDrop(key)
				trace.prepareForRollback(p.Account)
				continue
			}
			pr.addAlreadyBooked(p, lot, firstStepOrNil(steps), key)
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

	// Pass 2: resolve unknowns against the residual, partitioned by
	// weight currency. Committed groups (cost-spec currency or price
	// annotation pins the unknown to a specific currency) are solved
	// per-currency; free unknowns absorb whatever currency remains
	// unclaimed. All mutations land on pr; the BookedPosting slice is
	// not materialized until finalize.
	committed, free := pr.groupForResidual()
	claimed := make(map[string]bool, len(committed))
	for _, g := range committed {
		claimed[g.currency] = true
		r.resolveCommittedGroup(g, &pr, trace, txn.Date)
	}
	r.resolveFreeUnknown(free, &pr, claimed, trace, txn.Date)

	// Materialize the booked slice: applies any currency-group drops,
	// binds Source pointers on survivors. diff observes
	// prepareForRollback marks finalize recorded for dropped groups.
	booked = pr.finalize(trace, r)
	txn.Postings = pr.postings
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
// rolledBack records accounts whose currency group was fully rolled back
// via inverse-operation bookings during finalize. diff() uses this
// set to suppress accounts that are back to their pre-transaction state
// from the visitor output.
type stateTrace struct {
	state      map[ast.Account]*Inventory
	before     map[ast.Account]*Inventory
	rolledBack map[ast.Account]struct{} // lazily initialized; nil until first prepareForRollback
}

// newStateTrace begins recording edits against state. before-snapshots
// are scoped to this trace; state is shared with the caller and is
// mutated in place by [stateTrace.prepareForEdit]. rolledBack is left
// nil and initialized lazily by the first [stateTrace.prepareForRollback] call.
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
	if _, seen := st.before[acct]; !seen {
		inv := st.state[acct]
		if inv == nil {
			inv = NewInventory()
			st.state[acct] = inv
			st.before[acct] = nil
		} else {
			st.before[acct] = inv.Clone()
		}
	}
	return st.state[acct]
}

// prepareForRollback records acct in the rolledBack set and returns the
// live inventory for acct. It is called when a failed currency group's
// account needs to be marked so diff() can suppress it if no net
// mutation occurred. The rolledBack set is used by diff() to suppress
// accounts whose state is back to its pre-transaction value from the
// visitor output.
//
// The rolledBack map is lazily initialized on first call. If acct has
// not yet been touched in this trace, prepareForEdit semantics apply
// (the account is added to before with a nil snapshot and a fresh
// inventory is installed if absent).
func (st *stateTrace) prepareForRollback(acct ast.Account) *Inventory {
	if st.rolledBack == nil {
		st.rolledBack = make(map[ast.Account]struct{})
	}
	st.rolledBack[acct] = struct{}{}
	return st.prepareForEdit(acct)
}

// diff returns the (before, after) pair for the visitor callback.
// The before map is the trace's own — diff transfers ownership to
// the caller, which is safe because a stateTrace is scoped to a
// single visitTxn invocation and is discarded immediately after.
// after is freshly constructed as clones of the current state for
// every account first touched by this trace.
//
// Exclusion rule: if acct is in rolledBack and its state equals its
// before-snapshot (meaning all mutations were successfully reversed),
// the account is omitted from both before and after. Inventory.Equal
// treats a nil before-snapshot as empty, so a newly-touched account
// rolled back to nothing is excluded. Accounts in rolledBack that still
// differ (partial mutation residue) are included as usual, so they
// remain visible in the diff output.
func (st *stateTrace) diff() (before, after map[ast.Account]*Inventory) {
	after = make(map[ast.Account]*Inventory, len(st.before))
	for acct := range st.before {
		if _, rolled := st.rolledBack[acct]; rolled && st.state[acct].Equal(st.before[acct]) {
			// The account was fully rolled back to its pre-transaction
			// state; suppress it so the visitor does not see a no-op diff.
			delete(st.before, acct) // Deleting the current key during a map range is safe per the Go spec.
			continue
		}
		inv := st.state[acct]
		if inv != nil {
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

// sumBookedWeights returns the per-currency net of the booked weights
// in postings, plus the order in which currencies were first seen.
// Each entry must be a non-nil pointer to a posting whose Amount is
// set ([PostingWeight] returns nil with no error for an auto-posting
// and that branch is silently skipped). On a posting-weight or
// accumulate-weight failure, a [CodeInternalError] diagnostic is
// appended to r.errs against the offending posting and ok is false.
func (r *Reducer) sumBookedWeights(postings []*ast.Posting) (sums map[string]*apd.Decimal, order []string, ok bool) {
	sums = map[string]*apd.Decimal{}
	for _, p := range postings {
		w, err := PostingWeight(p)
		if err != nil {
			r.errs = append(r.errs, Error{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "interpolate: posting weight: " + err.Error(),
			})
			return nil, nil, false
		}
		if w == nil {
			continue
		}
		if existing, found := sums[w.Currency]; found {
			if _, err := apd.BaseContext.Add(existing, existing, &w.Number); err != nil {
				r.errs = append(r.errs, Error{
					Code:    CodeInternalError,
					Span:    p.Span,
					Account: p.Account,
					Message: "interpolate: accumulate weight: " + err.Error(),
				})
				return nil, nil, false
			}
		} else {
			sums[w.Currency] = &w.Number
			order = append(order, w.Currency)
		}
	}
	return sums, order, true
}

// negatedResidual returns -sums[currency] as a freshly allocated
// [ast.Amount], or nil when the entry is absent or sums to zero. The
// returned Amount does not alias sums; callers may mutate it.
func negatedResidual(sums map[string]*apd.Decimal, currency string) (*ast.Amount, error) {
	v, ok := sums[currency]
	if !ok || v.IsZero() {
		return nil, nil
	}
	out := &ast.Amount{Currency: currency}
	if _, err := apd.BaseContext.Neg(&out.Number, v); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveCommittedGroup resolves Pass 2 for a single committed
// unknownGroup: multiple unknowns sharing the group's currency are
// reported as ambiguous (diagnostic only; postings remain and the
// group is *not* dropped, so the validator still sees them); a single
// unknown absorbs the group's negated booked-weight residual. Only
// single-unknown failures (zero residual, resolveCostFromResidual
// error, bookOne reject) mark g.currency for drop so finalize excludes
// the whole currency group. Mutations land on pr; the caller never
// observes a half-built BookedPosting slice.
func (r *Reducer) resolveCommittedGroup(
	g unknownGroup,
	pr *postingResolution,
	trace *stateTrace,
	txnDate time.Time,
) {
	if len(g.unknown) > 1 {
		r.flagAmbiguousUnknowns(g.unknown)
		return
	}
	unknownP := g.unknown[0]
	residual, ok := r.residualForCurrency(g.booked, g.currency, unknownP)
	if !ok {
		pr.markForDrop(g.currency)
		return
	}
	if !r.applyResidualToUnknown(unknownP, residual, pr, trace, txnDate) {
		pr.markForDrop(g.currency)
	}
}

// residualForCurrency sums postings' weights and returns the negated
// residual in currency, or (nil, false) after appending a
// CodeUnresolvableInterpolation / CodeInternalError diagnostic
// attributed to unknownP. A zero residual is reported with the
// existing zero-residual wording (auto-posting variant when
// unknownP.Amount == nil, deferred-cost variant otherwise).
func (r *Reducer) residualForCurrency(postings []*ast.Posting, currency string, unknownP *ast.Posting) (*ast.Amount, bool) {
	sums, _, ok := r.sumBookedWeights(postings)
	if !ok {
		return nil, false
	}
	residual, err := negatedResidual(sums, currency)
	if err != nil {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "interpolate: negate residual: " + err.Error(),
		})
		return nil, false
	}
	if residual == nil {
		msg := "deferred cost cannot be interpolated: every currency already balances"
		if unknownP.Amount == nil {
			msg = "auto-balanced posting has no residual to absorb; every currency already balances"
		}
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: msg,
		})
		return nil, false
	}
	return residual, true
}

// applyResidualToUnknown writes residual onto unknownP (auto-posting
// → Amount; deferred-cost → Cost via [Reducer.resolveCostFromResidual]),
// re-runs bookOne, and on success appends a [BookedPosting] (Source
// left nil; finalize binds it) plus the parallel bookedDesc entry. ok
// reports whether resolution succeeded. The bookOne-failure path calls
// markForDrop(residual.Currency) here, co-located with its rollback
// prepare; other failure paths (resolveCostFromResidual error,
// multi-step matcher violation, descIdx-invariant violation) leave
// drop-marking to the caller. When pr.dropped already covers
// residual.Currency the resolution silently joins the dropped group
// and returns ok=true.
func (r *Reducer) applyResidualToUnknown(
	unknownP *ast.Posting,
	residual *ast.Amount,
	pr *postingResolution,
	trace *stateTrace,
	txnDate time.Time,
) bool {
	// invariant: pr.unknownDesc carries unknownP at exactly one offset.
	descIdx := pr.unknownDescIndex(unknownP)
	if descIdx < 0 {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "interpolate: posting absent from unknownDesc; invariant violated",
		})
		return false
	}
	if pr.dropped[residual.Currency] {
		pr.unknownDesc[descIdx].currency = residual.Currency
		return true
	}
	inferred := unknownP.Amount == nil
	if inferred {
		// Auto-posting: write the inferred Amount. validateStructure
		// guarantees Cost nil; preserve it — bookOne must not write Cost here.
		unknownP.Amount = residual
	} else if err := r.resolveCostFromResidual(unknownP, residual, txnDate); err != nil {
		r.errs = append(r.errs, *err)
		return false
	}
	inv := trace.prepareForEdit(unknownP.Account)
	lot, steps, errs := bookOne(inv, unknownP, r.booking[unknownP.Account], txnDate)
	r.errs = append(r.errs, errs...)
	if len(errs) > 0 {
		pr.markForDrop(residual.Currency)
		trace.prepareForRollback(unknownP.Account)
		pr.unknownDesc[descIdx].currency = residual.Currency
		return false
	}
	if len(steps) > 1 {
		// invariant: the deferred branch installs a tight Cost so the matcher
		// returns one lot. Expansion would require splitting unknownP into
		// children mid-Pass 2, which the residual model does not support.
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "residual pass produced a multi-lot reduction; expansion is not supported here",
		})
		return false
	}
	pr.booked = append(pr.booked, BookedPosting{
		Account:      unknownP.Account,
		Units:        *unknownP.Amount.Clone(),
		Lot:          lot,
		Reduction:    firstStepOrNil(steps),
		InferredAuto: inferred,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{
		currency:  residual.Currency,
		postingAt: pr.unknownDesc[descIdx].postingAt,
	})
	pr.unknownDesc[descIdx].currency = residual.Currency
	return true
}

// resolveFreeUnknown handles Pass 2's free bucket: unknown postings
// whose candidate currency was not pinned by a cost-spec currency or
// price annotation. With more than one free entry the case is
// ambiguous; with exactly one, the unknown absorbs the unique
// remaining residual currency (currencies already claimed by
// committed groups or marked for drop are excluded). Mutations land
// on pr.
func (r *Reducer) resolveFreeUnknown(
	free []*ast.Posting,
	pr *postingResolution,
	claimed map[string]bool,
	trace *stateTrace,
	txnDate time.Time,
) {
	switch {
	case len(free) == 0:
		return
	case len(free) > 1:
		r.flagAmbiguousUnknowns(free)
		return
	}
	unknownP := free[0]
	sums, order, ok := r.sumBookedWeights(pr.bookedPostings())
	if !ok {
		return
	}
	unclaimed := make([]string, 0, len(order))
	// invariant: pr.dropped ⊆ claimed. resolveCommittedGroup stamps
	// claimed[g.currency] unconditionally before any markForDrop call,
	// so filtering by claimed alone covers both "bid by a committed
	// unknown" and "dropped by a committed-group failure".
	for _, cur := range order {
		if claimed[cur] {
			continue
		}
		if !sums[cur].IsZero() {
			unclaimed = append(unclaimed, cur)
		}
	}
	if len(unclaimed) != 1 {
		msg := "deferred cost cannot be interpolated: every currency already balances"
		if unknownP.Amount == nil {
			msg = "auto-balanced posting has no residual to absorb; every currency already balances"
		}
		if len(unclaimed) > 1 {
			msg = fmt.Sprintf("residual spans %d currencies %v but a single unknown can only absorb one", len(unclaimed), unclaimed)
		}
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: msg,
		})
		return
	}
	residual, err := negatedResidual(sums, unclaimed[0])
	if err != nil {
		r.errs = append(r.errs, Error{
			Code:    CodeInternalError,
			Span:    unknownP.Span,
			Account: unknownP.Account,
			Message: "interpolate: negate residual: " + err.Error(),
		})
		return
	}
	r.applyResidualToUnknown(unknownP, residual, pr, trace, txnDate)
}

// unknownDescIndex returns the offset in pr.unknownDesc whose
// posting address matches p, or -1 if absent. Pass 2 uses it to
// stamp the resolved currency back onto the descriptor for an
// unknown that [postingResolution.groupForResidual] surfaced.
func (pr *postingResolution) unknownDescIndex(p *ast.Posting) int {
	for i, ref := range pr.unknownDesc {
		if &pr.postings[ref.postingAt] == p {
			return i
		}
	}
	return -1
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
