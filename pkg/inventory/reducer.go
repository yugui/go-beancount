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
// the same value;
// [postingResolution.recordUnknownFailed] still stamps it on Pass 2
// failure so finalize can match the entry).
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

// residualGroup is one per-currency entry produced by
// [postingResolution.groupForResidual]. Bidders share the candidate
// currency stamped at addUnknown time (the well-formed case is
// len(unknown) == 1).
//
//   - len(unknown) == 0: no bidder for this currency. The Reducer
//     forwards residual to the free path when it is non-zero.
//   - len(unknown) == 1: the Reducer synthesizes Cost or Amount from
//     residual and books the unknown.
//   - len(unknown)  > 1: ambiguous; the Reducer emits the ambiguity
//     diagnostic and residual stays claimed by the unresolved bidders.
//
// residual.Currency always equals currency. residual.Number may be
// zero (the currency balances on the booked side); this is a valid
// interpolation outcome, not an error.
type residualGroup struct {
	currency string
	unknown  []*ast.Posting
	residual ast.Amount
}

// groupForResidual partitions Pass 1's output into per-currency residual
// groups plus the free unknowns. A single walk of pr.bookedDesc sums
// the booked weights; the postingResolution never exposes a flat list
// of booked postings to the Reducer side.
//
// groups has one entry per (a) bid currency not in pr.dropped, in
// first-appearance order from pr.unknownDesc, and (b) any remaining
// sum currency with non-zero residual, in first-appearance order from
// booked weights. The second category drives the free path: its
// residuals are what a free unknown may absorb.
//
// free lists every unknown whose candidate currency is "".
//
// Unknowns whose candidate currency is in pr.dropped are silently
// joined: their unknownDesc.currency already names a dropped currency,
// so finalize's drop filter excludes them automatically. groupForResidual
// simply skips them and emits no group on their behalf. The free-path
// counterpart — a free unknown whose only sum-only residual is in a
// dropped currency — relies on the same finalize mechanism: bookOne
// runs, the resulting BookedPosting is appended under the dropped
// currency, finalize reverses the booking and excludes the posting.
//
// err is non-nil iff summing booked weights or negating a residual
// failed (apd arithmetic invariants): a non-recoverable internal
// error, not a user book-keeping mistake. Callers must not proceed
// with Pass 2 in that case.
func (pr *postingResolution) groupForResidual() (
	groups []residualGroup,
	free []*ast.Posting,
	err error,
) {
	if len(pr.unknownDesc) == 0 {
		return nil, nil, nil
	}

	sums := map[string]*apd.Decimal{}
	var sumOrder []string
	for _, ref := range pr.bookedDesc {
		p := &pr.postings[ref.postingAt]
		w, werr := PostingWeight(p)
		if werr != nil {
			return nil, nil, Error{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "interpolate: posting weight: " + werr.Error(),
			}
		}
		if w == nil {
			continue
		}
		existing, found := sums[w.Currency]
		if !found {
			sums[w.Currency] = &w.Number
			sumOrder = append(sumOrder, w.Currency)
			continue
		}
		if _, aerr := apd.BaseContext.Add(existing, existing, &w.Number); aerr != nil {
			return nil, nil, Error{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "interpolate: accumulate weight: " + aerr.Error(),
			}
		}
	}

	bid := map[string][]*ast.Posting{}
	var bidOrder []string
	for _, ref := range pr.unknownDesc {
		p := &pr.postings[ref.postingAt]
		if ref.currency == "" {
			free = append(free, p)
			continue
		}
		if pr.dropped[ref.currency] {
			// silent-join: finalize excludes this unknown via its
			// already-stamped dropped currency.
			continue
		}
		if _, seen := bid[ref.currency]; !seen {
			bidOrder = append(bidOrder, ref.currency)
		}
		bid[ref.currency] = append(bid[ref.currency], p)
	}

	negate := func(span ast.Span, account ast.Account, s *apd.Decimal) (apd.Decimal, error) {
		var neg apd.Decimal
		if _, nerr := apd.BaseContext.Neg(&neg, s); nerr != nil {
			return apd.Decimal{}, Error{
				Code:    CodeInternalError,
				Span:    span,
				Account: account,
				Message: "interpolate: negate residual: " + nerr.Error(),
			}
		}
		return neg, nil
	}

	for _, cur := range bidOrder {
		residual := ast.Amount{Currency: cur}
		if s, ok := sums[cur]; ok {
			neg, nerr := negate(bid[cur][0].Span, bid[cur][0].Account, s)
			if nerr != nil {
				return nil, nil, nerr
			}
			residual.Number = neg
		}
		groups = append(groups, residualGroup{
			currency: cur,
			unknown:  bid[cur],
			residual: residual,
		})
	}
	for _, cur := range sumOrder {
		if _, claimed := bid[cur]; claimed {
			continue
		}
		s := sums[cur]
		if s.IsZero() {
			continue
		}
		neg, nerr := negate(ast.Span{}, "", s)
		if nerr != nil {
			return nil, nil, nerr
		}
		groups = append(groups, residualGroup{
			currency: cur,
			residual: ast.Amount{Number: neg, Currency: cur},
		})
	}

	return groups, free, nil
}

// promoteLotAugmentation completes a Pass 2 deferred augmentation:
// the synthesized Cost on p is replaced with the booked-tier
// lot.Clone(), and the unknown is recorded as a BookedPosting with
// Lot set. Mirrors [postingResolution.addLotAugmentation] on the
// Pass 1 side; InferredAuto is false because the user wrote Amount
// and only the cost was resolved by the residual pass.
func (pr *postingResolution) promoteLotAugmentation(p *ast.Posting, lot *Lot, currency string) {
	p.Cost = lot.Clone()
	descIdx := pr.unknownDescIndex(p)
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
		Lot:     lot,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{
		currency:  currency,
		postingAt: pr.unknownDesc[descIdx].postingAt,
	})
	pr.unknownDesc[descIdx].currency = currency
}

// promoteCashAugmentation completes a Pass 2 auto-posting whose
// residual is a positive cash augmentation: Amount is written from
// the synthesized residual, no Cost is installed. Mirrors
// [postingResolution.addCashAugmentation]; InferredAuto is true.
func (pr *postingResolution) promoteCashAugmentation(p *ast.Posting, amt ast.Amount, currency string) {
	a := amt
	p.Amount = &a
	descIdx := pr.unknownDescIndex(p)
	pr.booked = append(pr.booked, BookedPosting{
		Account:      p.Account,
		Units:        *p.Amount.Clone(),
		InferredAuto: true,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{
		currency:  currency,
		postingAt: pr.unknownDesc[descIdx].postingAt,
	})
	pr.unknownDesc[descIdx].currency = currency
}

// promoteSingleLotReduction completes a Pass 2 auto-posting whose
// residual resolved to a single-lot reduction (typically the
// cash-sentinel step produced when an auto absorbs a negative cash
// residual). Amount is written from the synthesized residual;
// step.Lot is installed as Cost only when it is a real lot — the
// cash-sentinel skip mirrors [postingResolution.addSingleLotReduction].
// InferredAuto is true.
func (pr *postingResolution) promoteSingleLotReduction(p *ast.Posting, step ReductionStep, amt ast.Amount, currency string) {
	a := amt
	p.Amount = &a
	if step.Lot.Currency != "" || step.Lot.Number.Sign() != 0 {
		p.Cost = step.Lot.Clone()
	}
	descIdx := pr.unknownDescIndex(p)
	pr.booked = append(pr.booked, BookedPosting{
		Account:      p.Account,
		Units:        *p.Amount.Clone(),
		Reduction:    &step,
		InferredAuto: true,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{
		currency:  currency,
		postingAt: pr.unknownDesc[descIdx].postingAt,
	})
	pr.unknownDesc[descIdx].currency = currency
}

// recordUnknownFailed records a Pass 2 failure or silent-join: currency
// is marked for drop (idempotent) and the unknownDesc entry is stamped
// so finalize excludes the unknown from the rebuilt postings.
func (pr *postingResolution) recordUnknownFailed(unknownP *ast.Posting, currency string) {
	pr.markForDrop(currency)
	descIdx := pr.unknownDescIndex(unknownP)
	pr.unknownDesc[descIdx].currency = currency
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

	// Pass 2: resolve unknowns against the per-currency residual.
	// pr.groupForResidual produces the partition; the book closure
	// captures the per-transaction context (pr, trace, txnDate,
	// r.booking) so the committed and free paths see a clean
	// (orig, candidate, currency) entry point. Its body mirrors
	// Pass 1's loop body — prepareForEdit, bookOne, dispatch by
	// result, rollback on failure — and dispatches to pr.promote*
	// (Pass 2's symmetric counterpart to pr.add*).
	groups, free, gerr := pr.groupForResidual()
	if gerr != nil {
		r.errs = append(r.errs, asError(gerr, ""))
	} else {
		book := func(orig, candidate *ast.Posting, currency string) []Error {
			inv := trace.prepareForEdit(orig.Account)
			lot, steps, errs := bookOne(inv, candidate, r.booking[orig.Account], txn.Date)
			if len(errs) > 0 {
				trace.prepareForRollback(orig.Account)
				pr.recordUnknownFailed(orig, currency)
				return errs
			}
			switch {
			case lot != nil:
				pr.promoteLotAugmentation(orig, lot, currency)
			case len(steps) == 0:
				pr.promoteCashAugmentation(orig, *candidate.Amount, currency)
			case len(steps) == 1:
				pr.promoteSingleLotReduction(orig, steps[0], *candidate.Amount, currency)
			default:
				trace.prepareForRollback(orig.Account)
				pr.recordUnknownFailed(orig, currency)
				return []Error{{
					Code:    CodeInternalError,
					Span:    orig.Span,
					Account: orig.Account,
					Message: "residual booking produced a multi-lot reduction",
				}}
			}
			return nil
		}

		freeResiduals := r.resolveResidualGroups(groups, txn.Date, book)
		r.resolveFreeResiduals(free, freeResiduals, txn.Date, book)
	}

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

// resolveResidualGroups walks Pass 2's per-currency groups returned
// by [postingResolution.groupForResidual]. Groups with one unknown
// bidder (the well-formed committed case) are booked via book;
// zero-bidder groups contribute their non-zero residual to the
// returned slice for the free path; multi-bidder groups emit one
// ambiguity diagnostic each.
//
// validateStructure guarantees that a bidder has Amount != nil
// (auto-postings cannot carry Cost or Price); a bidder with zero
// units is rejected with CodeUnresolvableInterpolation because the
// per-unit cost would require dividing the residual by zero.
//
// book is the visitTxn-scoped closure that owns prepareForEdit,
// bookOne, the success-side dispatch to pr.promote*, and the
// rollback/recordUnknownFailed sequence on failure.
func (r *Reducer) resolveResidualGroups(
	groups []residualGroup,
	txnDate time.Time,
	book func(orig, candidate *ast.Posting, currency string) []Error,
) []ast.Amount {
	var freeResiduals []ast.Amount
	for _, g := range groups {
		switch {
		case len(g.unknown) == 0:
			if g.residual.Number.Sign() != 0 {
				freeResiduals = append(freeResiduals, g.residual)
			}
			continue
		case len(g.unknown) > 1:
			r.flagAmbiguousUnknowns(g.unknown)
			continue
		}

		p := g.unknown[0]
		if p.Amount.Number.Sign() == 0 {
			r.errs = append(r.errs, Error{
				Code:    CodeUnresolvableInterpolation,
				Span:    p.Span,
				Account: p.Account,
				Message: "deferred cost cannot be interpolated: posting has zero units",
			})
			continue
		}
		candidate := *p
		candidate.Cost = synthesizeCostSpec(p.Cost, g.residual, txnDate)
		if errs := book(p, &candidate, g.residual.Currency); len(errs) > 0 {
			r.errs = append(r.errs, errs...)
		}
	}
	return freeResiduals
}

// resolveFreeResiduals handles Pass 2's free bucket: unknown postings
// whose candidate currency was not pinned by a cost-spec currency or
// price annotation. With more than one free entry the case is
// ambiguous; with exactly one, the unknown absorbs the unique
// remaining residual currency, either as a synthesized Amount
// (auto-posting) or a synthesized Cost (deferred posting with empty
// cost spec). A residual currency that was Pass-1-dropped is handled
// by finalize: book stamps the unknownDesc with the dropped currency,
// the resulting BookedPosting joins the dropped group, and both are
// reversed and excluded.
func (r *Reducer) resolveFreeResiduals(
	free []*ast.Posting,
	freeResiduals []ast.Amount,
	txnDate time.Time,
	book func(orig, candidate *ast.Posting, currency string) []Error,
) {
	switch {
	case len(free) == 0:
		return
	case len(free) > 1:
		r.flagAmbiguousUnknowns(free)
		return
	}

	p := free[0]
	switch {
	case len(freeResiduals) == 0:
		msg := "deferred cost cannot be interpolated: every currency already balances"
		if p.Amount == nil {
			msg = "auto-balanced posting has no residual to absorb; every currency already balances"
		}
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    p.Span,
			Account: p.Account,
			Message: msg,
		})
		return
	case len(freeResiduals) > 1:
		currencies := make([]string, len(freeResiduals))
		for i, a := range freeResiduals {
			currencies[i] = a.Currency
		}
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    p.Span,
			Account: p.Account,
			Message: fmt.Sprintf("residual spans %d currencies %v but a single unknown can only absorb one", len(currencies), currencies),
		})
		return
	}

	res := freeResiduals[0]
	if p.Amount == nil {
		candidate := *p
		candidate.Amount = &res
		if errs := book(p, &candidate, res.Currency); len(errs) > 0 {
			r.errs = append(r.errs, errs...)
		}
		return
	}
	if p.Amount.Number.Sign() == 0 {
		r.errs = append(r.errs, Error{
			Code:    CodeUnresolvableInterpolation,
			Span:    p.Span,
			Account: p.Account,
			Message: "deferred cost cannot be interpolated: posting has zero units",
		})
		return
	}
	candidate := *p
	candidate.Cost = synthesizeCostSpec(p.Cost, res, txnDate)
	if errs := book(p, &candidate, res.Currency); len(errs) > 0 {
		r.errs = append(r.errs, errs...)
	}
}

// synthesizeCostSpec builds a parse-tier *ast.CostSpec carrying the
// Pass 2 residual as the total cost. Per-unit Number is derived
// downstream by [ResolveCost] when bookOne runs against it
// ({{T CUR}} → T / |units|). Date and Label inherit from existing
// when it is a *ast.CostSpec; Date falls back to txnDate.
func synthesizeCostSpec(existing ast.CostHolder, residual ast.Amount, txnDate time.Time) *ast.CostSpec {
	date := txnDate
	var label string
	if spec, ok := existing.(*ast.CostSpec); ok && spec != nil {
		if spec.Date != nil {
			date = *spec.Date
		}
		label = spec.Label
	}
	return &ast.CostSpec{
		Total:    ast.CloneDecimal(&residual.Number),
		Currency: residual.Currency,
		Date:     &date,
		Label:    label,
	}
}

// unknownDescIndex returns the offset in pr.unknownDesc whose
// posting address matches p, or -1 if absent. It is an internal
// pr helper used by the [postingResolution.promote*] family and by
// [postingResolution.recordUnknownFailed] to stamp the resolved
// currency back onto the descriptor after Pass 2 binds it.
func (pr *postingResolution) unknownDescIndex(p *ast.Posting) int {
	for i, ref := range pr.unknownDesc {
		if &pr.postings[ref.postingAt] == p {
			return i
		}
	}
	return -1
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
