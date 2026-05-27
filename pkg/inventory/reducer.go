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
// [BookedPosting] records via a visitor. [Reducer.Walk] is the
// primary entry point; [Reducer.Run] is the visitorless batch form;
// [Reducer.Inspect] reconstructs a single transaction's view.
//
// A Reducer is not safe for concurrent use. It is reusable: each
// [Reducer.Walk] resets internal state at entry and re-iterates the
// directives, so repeated calls produce identical results.
//
// Walk does not mutate its input. Transactions the booking pass
// would otherwise edit (auto-balanced amount, deferred cost,
// multi-lot reduction) are deep-cloned; other transactions and all
// non-Transaction directives pass through by reference.
type Reducer struct {
	directives iter.Seq2[int, ast.Directive]
	opts       *ast.OptionValues
	booking    map[ast.Account]ast.BookingMethod
	state      map[ast.Account]*Inventory
	diags      []ast.Diagnostic
}

// NewReducer returns a Reducer that iterates directives on each
// [Reducer.Walk] call. The sequence MUST be replayable
// (e.g. [ast.Ledger.All]) and must not be mutated between calls.
// Option-dependent behavior (e.g. the "booking_method" fallback for
// Open directives without an explicit keyword) uses the registry
// defaults. To supply per-ledger option values, use
// [NewReducerWithOptions].
func NewReducer(directives iter.Seq2[int, ast.Directive]) *Reducer {
	return &Reducer{directives: directives}
}

// NewReducerWithOptions is like [NewReducer] but consults opts for any
// option-dependent behavior the reducer needs. Passing nil opts is
// equivalent to calling [NewReducer].
//
// Diagnostics from option resolution are silently consumed; the canonical
// emitter is the validations plugin via accountstate.Build.
func NewReducerWithOptions(directives iter.Seq2[int, ast.Directive], opts *ast.OptionValues) *Reducer {
	return &Reducer{directives: directives, opts: opts}
}

// VisitFunc is called once per [ast.Transaction] during [Reducer.Walk].
//
// Pointer contract: txn is the caller's original input pointer (so
// [Reducer.Inspect] and identity-based lookups work). Each
// [BookedPosting.Source] points into the reducer's working copy
// (clone or original, depending on whether the reducer needed to
// mutate); reading via Source observes the post-booking interpolation
// and any postings created by multi-lot expansion.
//
// before and after contain only accounts touched by the transaction.
// before maps a not-yet-seen account to a nil *Inventory; after's
// values are always non-nil deep-copied snapshots. Both maps' values
// are fresh clones the callback may retain.
//
// Returning false terminates iteration early.
type VisitFunc func(
	txn *ast.Transaction,
	before map[ast.Account]*Inventory,
	after map[ast.Account]*Inventory,
	booked []BookedPosting,
) bool

// Walk iterates the directives sequence, applying per-transaction
// booking and invoking visit for each transaction that touched at
// least one account. Balance, Pad, and Price checks are not
// re-evaluated here — [pkg/validation] handles them in its own pass.
//
// The returned directive slice contains the booking outcome: any
// transaction the reducer needed to mutate appears as a clone with
// the mutations applied (inferred Amount, resolved deferred cost,
// expanded multi-lot reduction); other transactions and non-
// Transaction directives are returned by reference.
//
// The diagnostics slice carries every input-data finding (e.g.
// [CodeAmbiguousLotMatch], [CodeUnresolvableInterpolation]) and is a
// fresh copy the caller may retain; findings do not stop iteration
// unless the visitor returns false.
//
// The error return is reserved for implementation bugs in the
// booking pass — invariant violations and apd arithmetic failures
// from inputs the grammar cannot produce. When non-nil it halts
// further iteration; the directives slice contains the prefix
// processed so far and diagnostics carries every finding collected
// before the halt. The booking adapter in pkg/loader/booking
// translates this error into a [ast.Diagnostic] with
// [CodeInternalError] so the rest of the load pipeline can surface
// it alongside other findings.
//
// Walk is reusable: each call resets state at entry and pays O(N) to
// re-iterate and re-clone.
func (r *Reducer) Walk(visit VisitFunc) ([]ast.Directive, []ast.Diagnostic, error) {
	r.state = map[ast.Account]*Inventory{}
	r.booking = map[ast.Account]ast.BookingMethod{}
	r.diags = nil

	var out []ast.Directive
	for _, d := range r.directives {
		switch d := d.(type) {
		case *ast.Open:
			method, _ := ast.ResolveBookingMethod(d, r.opts)
			r.booking[d.Account] = method
			out = append(out, d)
		case *ast.Close:
			out = append(out, d)
		case *ast.Transaction:
			booked := d
			if needsBookingClone(d) {
				booked = d.Clone()
			}
			before, after, bookedPostings, stop, err := r.visitTxn(booked)
			if err != nil {
				out = append(out, booked)
				return out, append([]ast.Diagnostic(nil), r.diags...), err
			}
			out = append(out, booked)
			if len(bookedPostings) == 0 && len(before) == 0 {
				// no bookable postings; skip visitor.
				continue
			}
			if visit != nil {
				if !visit(d, before, after, bookedPostings) {
					stop = true
				}
			}
			if stop {
				return out, append([]ast.Diagnostic(nil), r.diags...), nil
			}
		default:
			out = append(out, d)
		}
	}

	return out, append([]ast.Diagnostic(nil), r.diags...), nil
}

// needsBookingClone reports whether txn carries a posting the
// booking pass might mutate (auto-balanced Amount, or an unbooked
// *ast.CostSpec). A txn whose postings are all observationally
// unchanged by booking — already-booked Cost or no Cost at all —
// reuses its input pointer in Walk's output.
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

// groupRef pairs a posting index in [postingResolution.postings] with
// its weight-currency group key. For unknowns the currency is the
// Pass-1 candidate (see [unknownCandidateCurrency]); Pass 2
// overwrites it with the resolved currency.
type groupRef struct {
	currency  string
	postingAt int
}

// postingResolution is the per-transaction working state visitTxn
// builds during the two booking passes. It owns the rebuilt
// txn.Postings, the parallel []BookedPosting, and the set of dropped
// currency groups, exposing a small surface (add*/promote*/finalize)
// so the two passes share one invariant.
//
// Source pointers on pr.booked are bound only in [finalize], because
// append on pr.postings can reallocate its backing array and
// invalidate any &postings[k] taken earlier.
type postingResolution struct {
	postings []ast.Posting
	// booked and bookedDesc are parallel: bookedDesc[j] carries the
	// posting index and weight-currency group key for booked[j].
	booked     []BookedPosting
	bookedDesc []groupRef
	// unknownDesc carries the posting index and Pass-2 candidate
	// currency for each unknown (auto or deferred).
	unknownDesc []groupRef
	dropped     map[string]bool
}

// newPostingResolution pre-sizes the slices for the common
// no-expansion case. unknownDesc and dropped stay nil until first
// use.
func newPostingResolution(hint int) postingResolution {
	return postingResolution{
		postings:   make([]ast.Posting, 0, hint),
		booked:     make([]BookedPosting, 0, hint),
		bookedDesc: make([]groupRef, 0, hint),
	}
}

// addUnknown appends p (an auto-posting or a deferred-cost posting)
// for Pass 2 to resolve from the residual. The descriptor currency
// is the candidate from [unknownCandidateCurrency].
func (pr *postingResolution) addUnknown(p *ast.Posting) {
	pr.postings = append(pr.postings, *p)
	pr.unknownDesc = append(pr.unknownDesc, groupRef{
		currency:  unknownCandidateCurrency(p),
		postingAt: len(pr.postings) - 1,
	})
}

// markForDrop marks weightCurrency's group as failed; idempotent.
// Callers must also call trace.prepareForRollback for every account
// involved so the state-diff pass's exclusion rule applies even when
// the booking failed before mutating the inventory.
func (pr *postingResolution) markForDrop(weightCurrency string) {
	if pr.dropped == nil {
		pr.dropped = make(map[string]bool)
	}
	pr.dropped[weightCurrency] = true
}

// addAlreadyBooked records a posting whose Cost is already booked
// (the second-run fixed-point path). p.Cost is preserved pointer-
// identical so a reducer round-trip is byte-identical; lot / step
// are kept on the BookedPosting for downstream consumers.
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

// addLotAugmentation records a first-run augmentation with a cost
// spec. The rebuilt posting's Cost holder is converted to a booked
// [*ast.Cost] via [augmentCostFor] so [(*ast.Posting).TotalCost]
// keeps the user-written PerUnit/Total surcharge literal; the
// BookedPosting carries lot directly.
func (pr *postingResolution) addLotAugmentation(p *ast.Posting, lot *Lot, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	i := len(pr.postings) - 1
	pr.postings[i].Cost = augmentCostFor(p.Cost, lot)
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
		Lot:     lot,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: i})
}

// addCashAugmentation records an augmentation with no cost spec.
// The rebuilt posting's Cost holder is preserved verbatim.
func (pr *postingResolution) addCashAugmentation(p *ast.Posting, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	pr.booked = append(pr.booked, BookedPosting{
		Account: p.Account,
		Units:   *p.Amount.Clone(),
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: len(pr.postings) - 1})
}

// addSingleLotReduction records a reduction whose matcher selected
// exactly one lot. The rebuilt posting gets a provenance-free
// [*ast.Cost] from [Lot.ToCost] (see docs/architecture/cost-tier-separation.md),
// except for the cash-sentinel step (zero-value Lot) where p.Cost is
// left untouched. Unlike [addMultiLotReduction], an @@ total-form
// price needs no rewrite — the single child carries the parent's
// full |units|.
func (pr *postingResolution) addSingleLotReduction(p *ast.Posting, step ReductionStep, weightCurrency string) {
	pr.postings = append(pr.postings, *p)
	i := len(pr.postings) - 1
	if step.Lot.Currency != "" || step.Lot.Number.Sign() != 0 {
		pr.postings[i].Cost = step.Lot.ToCost()
	}
	pr.booked = append(pr.booked, BookedPosting{
		Account:   p.Account,
		Units:     *p.Amount.Clone(),
		Reduction: &step,
	})
	pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: weightCurrency, postingAt: i})
}

// addMultiLotReduction expands a multi-step reduction into one
// child posting per step. Each child is a deep clone of p with its
// own Amount (the step's signed magnitude) and Cost (step.Lot, which
// is always real — Inventory.Reduce never mixes cost-bearing and
// cash steps). Children may register under different weight
// currencies when the parent matched lots in different cost
// currencies.
//
// An @@ total-form price on p is rewritten per-unit on each child:
// the parent's total is bound to its full |units|, so reusing it on
// a child would overstate the price-side weight. The rewrite reuses
// step.SalePricePer so Reduction.SalePricePer and child.Price stay
// numerically equal by construction.
//
// Each child cost is installed via [Lot.ToCost]; see
// [addSingleLotReduction].
func (pr *postingResolution) addMultiLotReduction(p *ast.Posting, steps []ReductionStep) {
	rewriteTotalPrice := p.Price != nil && p.Price.IsTotal && p.Price.Amount.Currency != ""
	for _, step := range steps {
		pr.postings = append(pr.postings, p.Clone())
		i := len(pr.postings) - 1
		child := &pr.postings[i]
		child.Amount = &ast.Amount{
			Number:   signedMagnitude(&step.Units, p.Amount.Number.Negative),
			Currency: p.Amount.Currency,
		}
		child.Cost = step.Lot.ToCost()
		if rewriteTotalPrice && step.SalePricePer != nil {
			// synthetic child Price has no source-text Span.
			child.Price = &ast.PriceAnnotation{
				Amount: ast.Amount{
					Number:   *ast.CloneDecimal(step.SalePricePer),
					Currency: p.Price.Amount.Currency,
				},
				IsTotal: false,
			}
		}
		pr.booked = append(pr.booked, BookedPosting{
			Account:   p.Account,
			Units:     *child.Amount.Clone(),
			Reduction: &step,
		})
		pr.bookedDesc = append(pr.bookedDesc, groupRef{currency: step.Lot.Currency, postingAt: i})
	}
}

// residualGroup is one per-currency entry produced by
// [postingResolution.groupForResidual]:
//
//   - len(unknown) == 0: no bidder; residual goes to the free path.
//   - len(unknown) == 1: Pass 2 synthesizes Cost or Amount from
//     residual and books the unknown.
//   - len(unknown)  > 1: ambiguous; Pass 2 emits one diagnostic per
//     bidder.
//
// residual.Currency always equals currency; residual.Number may be
// zero.
type residualGroup struct {
	currency string
	unknown  []*ast.Posting
	residual ast.Amount
}

// groupForResidual partitions Pass 1's output into per-currency
// residual groups plus free unknowns. Output ordering is
// first-appearance per source list. Unknowns whose candidate
// currency is already in pr.dropped are silently skipped (finalize's
// drop filter handles their exclusion).
//
// err is non-nil only for internal apd arithmetic failures; the
// caller must not proceed with Pass 2 in that case.
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
		w, err := PostingWeight(p)
		if err != nil {
			return nil, nil, fmt.Errorf("inventory.groupForResidual: posting weight (account %s): %w", p.Account, err)
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
		if _, err := apd.BaseContext.Add(existing, existing, &w.Number); err != nil {
			return nil, nil, fmt.Errorf("inventory.groupForResidual: accumulate weight (account %s): %w", p.Account, err)
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
			// silent-join: finalize already excludes this.
			continue
		}
		if _, seen := bid[ref.currency]; !seen {
			bidOrder = append(bidOrder, ref.currency)
		}
		bid[ref.currency] = append(bid[ref.currency], p)
	}

	negate := func(span ast.Span, account ast.Account, s *apd.Decimal) (apd.Decimal, error) {
		var neg apd.Decimal
		if _, err := apd.BaseContext.Neg(&neg, s); err != nil {
			pos := span.Start
			return apd.Decimal{}, fmt.Errorf(
				"inventory.groupForResidual: negate residual at %s:%d:%d (account %s): %w",
				pos.Filename, pos.Line, pos.Column, account, err,
			)
		}
		return neg, nil
	}

	for _, cur := range bidOrder {
		residual := ast.Amount{Currency: cur}
		if s, ok := sums[cur]; ok {
			neg, err := negate(bid[cur][0].Span, bid[cur][0].Account, s)
			if err != nil {
				return nil, nil, err
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
		neg, err := negate(ast.Span{}, "", s)
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, residualGroup{
			currency: cur,
			residual: ast.Amount{Number: neg, Currency: cur},
		})
	}

	return groups, free, nil
}

// promoteLotAugmentation is the Pass 2 mirror of
// [addLotAugmentation]: the spec on p (which the caller has already
// staged from the synthesized candidate) is converted to a booked
// [*ast.Cost] via [augmentCostFor], so the residual-derived
// PerUnit/Total survives as round-trip provenance. InferredAuto
// stays false (only the cost was inferred).
func (pr *postingResolution) promoteLotAugmentation(p *ast.Posting, lot *Lot, currency string) {
	p.Cost = augmentCostFor(p.Cost, lot)
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

// promoteCashAugmentation is the Pass 2 mirror of
// [addCashAugmentation]: Amount is written from the residual, no
// Cost installed. InferredAuto is true.
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

// promoteSingleLotReduction is the Pass 2 mirror of
// [addSingleLotReduction] for an auto-posting whose residual
// resolved to a single-lot reduction. InferredAuto is true.
func (pr *postingResolution) promoteSingleLotReduction(p *ast.Posting, step ReductionStep, amt ast.Amount, currency string) {
	a := amt
	p.Amount = &a
	if step.Lot.Currency != "" || step.Lot.Number.Sign() != 0 {
		p.Cost = step.Lot.ToCost()
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

// recordUnknownFailed marks currency for drop and stamps the
// unknownDesc entry so finalize excludes unknownP.
func (pr *postingResolution) recordUnknownFailed(unknownP *ast.Posting, currency string) {
	pr.markForDrop(currency)
	descIdx := pr.unknownDescIndex(unknownP)
	pr.unknownDesc[descIdx].currency = currency
}

// unknownCandidateCurrency returns the Pass-2 candidate currency p
// will absorb, or "" when none is pinned. Cost currency takes
// precedence over price currency.
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

// finalize rebuilds pr.postings to exclude every currency group in
// pr.dropped, binds Source on the surviving BookedPostings, and
// applies inverse bookings (via trace.prepareForRollback) for
// dropped entries. After it returns, the caller assigns
// txn.Postings = pr.postings; pr is not used further.
//
// reverseBooking on a dropped entry can only fail with a system
// error (apd arithmetic is unreachable from valid input); finalize
// returns the first such error so the booking pass can halt rather
// than report a misleading user diagnostic.
func (pr *postingResolution) finalize(trace *stateTrace) ([]BookedPosting, error) {
	if len(pr.dropped) == 0 {
		booked := pr.booked
		for i, ref := range pr.bookedDesc {
			booked[i].Source = &pr.postings[ref.postingAt]
		}
		return booked, nil
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
				return nil, err
			}
			continue
		}
		bp := pr.booked[j]
		bp.Source = &pr.postings[newIdx[ref.postingAt]]
		out = append(out, bp)
	}
	return out, nil
}

// reverseBooking undoes bp's effect on inv via an inverse
// [Inventory.Add]: augmentation units are negated; a reduction
// restores its consumed magnitude to the same lot (the cash sentinel
// maps to a nil Cost so the cash slot merges). inv must be the live
// inventory obtained via trace.prepareForRollback so [diff] records
// the rollback.
func reverseBooking(inv *Inventory, bp BookedPosting) error {
	if bp.Reduction == nil {
		var neg apd.Decimal
		if _, err := apd.BaseContext.Neg(&neg, &bp.Units.Number); err != nil {
			return fmt.Errorf("inventory.reverseBooking: negate augmentation units (account %s): %w", bp.Account, err)
		}
		return inv.Add(Position{
			Units: ast.Amount{Number: neg, Currency: bp.Units.Currency},
			Cost:  bp.Lot,
		})
	}
	var lot *Lot
	if bp.Reduction.Lot.Currency != "" || bp.Reduction.Lot.Number.Sign() != 0 {
		// avoid aliasing step.Lot.
		lotCopy := bp.Reduction.Lot
		lot = lotCopy.Clone()
	}
	return inv.Add(Position{
		Units: ast.Amount{
			Number:   *ast.CloneDecimal(&bp.Reduction.Units),
			Currency: bp.Units.Currency,
		},
		Cost: lot,
	})
}

// visitTxn books a single transaction in two passes. Pass 1 books
// every explicit posting and stamps each unknown (auto, or deferred
// cost) with a Pass-2 candidate currency. Pass 2 resolves each
// candidate against its per-currency residual; a single free
// unknown absorbs the unique remaining currency. Two bidders on the
// same currency or two free unknowns are ambiguous.
//
// On posting failure, the whole weight-currency group is dropped:
// its postings are removed from txn.Postings and BookedPostings,
// and the inventory mutations are rolled back. Other groups in the
// same transaction are unaffected. A failure caused only by a
// deferred cost number is retried in Pass 2, not dropped.
//
// A structurally-invalid transaction is rejected with a diagnostic
// and leaves inventory untouched. stop is reserved for future use.
func (r *Reducer) visitTxn(txn *ast.Transaction) (
	before map[ast.Account]*Inventory,
	after map[ast.Account]*Inventory,
	booked []BookedPosting,
	stop bool,
	err error,
) {
	if !r.validateStructure(txn) {
		return map[ast.Account]*Inventory{}, nil, nil, false, nil
	}

	trace := newStateTrace(r.state)
	pr := newPostingResolution(len(txn.Postings))

	// pass 1
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount == nil {
			pr.addUnknown(p)
			continue
		}

		inv := trace.prepareForEdit(p.Account)
		method := r.booking[p.Account]
		lot, steps, finding, err := bookOne(inv, p, method, txn.Date)
		if err != nil {
			return nil, nil, nil, false, err
		}
		if finding != nil {
			if finding.Code == CodeAugmentationRequiresCost && costNumberMissing(p.Cost) {
				// deferred: retry in Pass 2.
				pr.addUnknown(p)
				continue
			}
			r.diags = append(r.diags, *finding)
			pr.markForDrop(weightCurrencyFallback(p))
			trace.prepareForRollback(p.Account)
			continue
		}
		switch {
		case p.Cost != nil && p.Cost.IsBooked():
			key := weightCurrencyFallback(p)
			if len(steps) > 1 {
				// invariant: tight matcher → ≤1 step.
				pos := p.Span.Start
				return nil, nil, nil, false, fmt.Errorf(
					"inventory.visitTxn: already-booked posting at %s:%d:%d (account %s) produced a multi-lot reduction; tight-matcher invariant violated",
					pos.Filename, pos.Line, pos.Column, p.Account,
				)
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

	// pass 2
	groups, free, err := pr.groupForResidual()
	if err != nil {
		// groupForResidual only returns system errors (apd weight
		// arithmetic that cannot fail from valid input); propagate.
		return nil, nil, nil, false, err
	}

	book := func(orig, candidate *ast.Posting, currency string) (*ast.Diagnostic, error) {
		inv := trace.prepareForEdit(orig.Account)
		lot, steps, finding, err := bookOne(inv, candidate, r.booking[orig.Account], txn.Date)
		if err != nil {
			return nil, err
		}
		if finding != nil {
			trace.prepareForRollback(orig.Account)
			pr.recordUnknownFailed(orig, currency)
			return finding, nil
		}
		switch {
		case lot != nil:
			// Hand the synthesized spec off to the installer so its
			// residual-derived PerUnit/Total survives as round-trip
			// provenance on the booked Cost.
			orig.Cost = candidate.Cost
			pr.promoteLotAugmentation(orig, lot, currency)
		case len(steps) == 0:
			pr.promoteCashAugmentation(orig, *candidate.Amount, currency)
		case len(steps) == 1:
			pr.promoteSingleLotReduction(orig, steps[0], *candidate.Amount, currency)
		default:
			trace.prepareForRollback(orig.Account)
			pr.recordUnknownFailed(orig, currency)
			pos := orig.Span.Start
			return nil, fmt.Errorf(
				"inventory.visitTxn: residual booking at %s:%d:%d (account %s) produced a multi-lot reduction",
				pos.Filename, pos.Line, pos.Column, orig.Account,
			)
		}
		return nil, nil
	}

	freeResiduals, failures, err := resolveResidualGroups(groups, txn.Date, book)
	if err != nil {
		return nil, nil, nil, false, err
	}
	for _, f := range failures {
		r.diags = append(r.diags, f.diag)
		pr.recordUnknownFailed(f.posting, f.currency)
	}

	freeDiags, err := resolveFreeResiduals(free, freeResiduals, txn.Date, book)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if len(freeDiags) > 0 {
		r.diags = append(r.diags, freeDiags...)
		for _, p := range free {
			pr.recordUnknownFailed(p, "")
		}
	}

	booked, err = pr.finalize(trace)
	if err != nil {
		return nil, nil, nil, false, err
	}
	txn.Postings = pr.postings
	before, after = trace.diff()
	return before, after, booked, false, nil
}

// signedMagnitude returns a clone of magnitude with its Negative
// flag set to negative. The clone is required so the caller's
// decimal does not alias step.Units, which the booking layer keeps
// reading.
func signedMagnitude(magnitude *apd.Decimal, negative bool) apd.Decimal {
	n := *ast.CloneDecimal(magnitude)
	if negative {
		n.Negative = true
	}
	return n
}

// firstStepOrNil returns &steps[0] when len(steps) == 1, else nil.
// Callers guard `len(steps) > 1` separately.
func firstStepOrNil(steps []ReductionStep) *ReductionStep {
	if len(steps) != 1 {
		return nil
	}
	return &steps[0]
}

// weightCurrencyFallback returns p's weight currency via
// [PostingWeight], falling back to p.Amount.Currency on error.
// Callers must pass a posting with non-nil Amount.
func weightCurrencyFallback(p *ast.Posting) string {
	w, err := PostingWeight(p)
	if err != nil {
		return p.Amount.Currency
	}
	return w.Currency
}

// cashGroupKey returns the weight currency for a cash-augmentation
// posting: price annotation takes precedence over amount currency,
// matching [PostingWeight].
func cashGroupKey(p *ast.Posting) string {
	if p.Price != nil {
		return p.Price.Amount.Currency
	}
	return p.Amount.Currency
}

// reductionGroupKey returns the weight currency for a single-lot
// reduction: the lot currency for a real lot, or [cashGroupKey] for
// the cash sentinel.
func reductionGroupKey(p *ast.Posting, step ReductionStep) string {
	if step.Lot.Currency != "" || step.Lot.Number.Sign() != 0 {
		return step.Lot.Currency
	}
	return cashGroupKey(p)
}

// validateStructure rejects transactions whose auto-posting structure
// is invalid (auto posting with cost/price, or more than one auto
// posting) and appends the diagnostic. Returns false on violation;
// the caller must abort before touching account state.
func (r *Reducer) validateStructure(txn *ast.Transaction) bool {
	seenAuto := false
	for i := range txn.Postings {
		p := &txn.Postings[i]
		if p.Amount != nil {
			continue
		}
		if p.Cost != nil || p.Price != nil {
			r.diags = append(r.diags, newDiag(
				CodeInvalidAutoPosting,
				p.Span,
				p.Account,
				"auto-balanced posting must not carry cost or price",
			))
			return false
		}
		if seenAuto {
			r.diags = append(r.diags, newDiag(
				CodeMultipleAutoPostings,
				p.Span,
				p.Account,
				"transaction has more than one auto-balanced posting",
			))
			return false
		}
		seenAuto = true
	}
	return true
}

// stateTrace records per-account inventory edits within a single
// transaction. The state map is the long-lived per-Reducer one
// (mutated in place); before is the trace-scoped snapshot map
// populated lazily on first touch (a nil before-value signals "newly
// touched account" to the visitor); rolledBack lists accounts whose
// currency group was inverse-booked during finalize, so [diff] can
// suppress no-op entries.
type stateTrace struct {
	state      map[ast.Account]*Inventory
	before     map[ast.Account]*Inventory
	rolledBack map[ast.Account]struct{}
}

// newStateTrace begins recording edits against state.
func newStateTrace(state map[ast.Account]*Inventory) *stateTrace {
	return &stateTrace{
		state:  state,
		before: map[ast.Account]*Inventory{},
	}
}

// prepareForEdit returns the inventory to mutate for acct. The first
// call for a given acct in this trace snapshots before (nil if the
// account had no inventory) and lazily installs an inventory.
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

// prepareForRollback marks acct as rolled back and returns its live
// inventory (with [prepareForEdit] semantics on first touch).
func (st *stateTrace) prepareForRollback(acct ast.Account) *Inventory {
	if st.rolledBack == nil {
		st.rolledBack = make(map[ast.Account]struct{})
	}
	st.rolledBack[acct] = struct{}{}
	return st.prepareForEdit(acct)
}

// diff returns the (before, after) pair for the visitor callback.
// Ownership of before is transferred to the caller (the trace is
// discarded immediately after). An account in rolledBack whose state
// equals its before-snapshot is excluded from both maps; otherwise
// after holds a freshly-cloned snapshot of the current state.
func (st *stateTrace) diff() (before, after map[ast.Account]*Inventory) {
	after = make(map[ast.Account]*Inventory, len(st.before))
	for acct := range st.before {
		if _, rolled := st.rolledBack[acct]; rolled && st.state[acct].Equal(st.before[acct]) {
			delete(st.before, acct)
			continue
		}
		after[acct] = st.state[acct].Clone()
	}
	return st.before, after
}

// ambiguousUnknownDiags returns one [CodeUnresolvableInterpolation]
// per unknown, with wording that distinguishes auto-postings (amount
// unresolved) from deferred cost specs (cost unresolved).
func ambiguousUnknownDiags(unknowns []*ast.Posting) []ast.Diagnostic {
	diags := make([]ast.Diagnostic, 0, len(unknowns))
	for _, p := range unknowns {
		msg := "cannot interpolate cost: transaction has multiple unknown posting values"
		if p.Amount == nil {
			msg = "cannot interpolate amount: transaction has multiple unknown posting values"
		}
		diags = append(diags, newDiag(CodeUnresolvableInterpolation, p.Span, p.Account, msg))
	}
	return diags
}

// unknownFailure pairs a Pass 2 unknown with its diagnostic and the
// currency the caller must drop it under.
type unknownFailure struct {
	posting  *ast.Posting
	currency string
	diag     ast.Diagnostic
}

// resolveResidualGroups walks per-currency residual groups and
// returns the residuals to forward to the free pass plus one
// failure per unresolvable bidder. The caller appends each
// failure.diag to r.diags and calls [recordUnknownFailed] with
// failure.currency. book is the visitTxn-scoped closure that runs
// bookOne and dispatches to pr.promote* on success.
func resolveResidualGroups(
	groups []residualGroup,
	txnDate time.Time,
	book func(orig, candidate *ast.Posting, currency string) (*ast.Diagnostic, error),
) (free []ast.Amount, failures []unknownFailure, err error) {
	for _, g := range groups {
		switch {
		case len(g.unknown) == 0:
			if g.residual.Number.Sign() != 0 {
				free = append(free, g.residual)
			}
			continue
		case len(g.unknown) > 1:
			ambDiags := ambiguousUnknownDiags(g.unknown)
			for i, p := range g.unknown {
				failures = append(failures, unknownFailure{
					posting:  p,
					currency: g.residual.Currency,
					diag:     ambDiags[i],
				})
			}
			continue
		}

		p := g.unknown[0]
		if p.Amount.Number.Sign() == 0 {
			failures = append(failures, unknownFailure{
				posting:  p,
				currency: g.residual.Currency,
				diag: newDiag(
					CodeUnresolvableInterpolation,
					p.Span,
					p.Account,
					"deferred cost cannot be interpolated: posting has zero units",
				),
			})
			continue
		}
		candidate := *p
		candidate.Cost = synthesizeCostSpec(p.Cost, g.residual, txnDate)
		finding, err := book(p, &candidate, g.residual.Currency)
		if err != nil {
			return nil, nil, err
		}
		if finding != nil {
			failures = append(failures, unknownFailure{
				posting:  p,
				currency: g.residual.Currency,
				diag:     *finding,
			})
		}
	}
	return free, failures, nil
}

// resolveFreeResiduals handles Pass 2's free bucket: zero free
// unknowns is a no-op, exactly one absorbs the unique residual,
// more than one is ambiguous. On a non-empty error return the
// caller must call [recordUnknownFailed] on every entry in free
// with the "" sentinel currency.
func resolveFreeResiduals(
	free []*ast.Posting,
	freeResiduals []ast.Amount,
	txnDate time.Time,
	book func(orig, candidate *ast.Posting, currency string) (*ast.Diagnostic, error),
) ([]ast.Diagnostic, error) {
	switch {
	case len(free) == 0:
		return nil, nil
	case len(free) > 1:
		return ambiguousUnknownDiags(free), nil
	}

	p := free[0]
	switch {
	case len(freeResiduals) == 0:
		msg := "deferred cost cannot be interpolated: every currency already balances"
		if p.Amount == nil {
			msg = "auto-balanced posting has no residual to absorb; every currency already balances"
		}
		return []ast.Diagnostic{newDiag(CodeUnresolvableInterpolation, p.Span, p.Account, msg)}, nil
	case len(freeResiduals) > 1:
		currencies := make([]string, len(freeResiduals))
		for i, a := range freeResiduals {
			currencies[i] = a.Currency
		}
		return []ast.Diagnostic{newDiag(
			CodeUnresolvableInterpolation,
			p.Span,
			p.Account,
			fmt.Sprintf("residual spans %d currencies %v but a single unknown can only absorb one", len(currencies), currencies))}, nil
	}

	res := freeResiduals[0]
	if p.Amount == nil {
		candidate := *p
		candidate.Amount = &res
		finding, err := book(p, &candidate, res.Currency)
		if err != nil {
			return nil, err
		}
		if finding != nil {
			return []ast.Diagnostic{*finding}, nil
		}
		return nil, nil
	}
	if p.Amount.Number.Sign() == 0 {
		return []ast.Diagnostic{newDiag(
			CodeUnresolvableInterpolation,
			p.Span,
			p.Account,
			"deferred cost cannot be interpolated: posting has zero units",
		)}, nil
	}
	candidate := *p
	candidate.Cost = synthesizeCostSpec(p.Cost, res, txnDate)
	finding, err := book(p, &candidate, res.Currency)
	if err != nil {
		return nil, err
	}
	if finding != nil {
		return []ast.Diagnostic{*finding}, nil
	}
	return nil, nil
}

// synthesizeCostSpec builds a {{T CUR}} CostSpec from a Pass 2
// residual. Per-unit Number is derived downstream by [ResolveLot].
// Date and Label inherit from existing when it is a *ast.CostSpec;
// Date falls back to txnDate.
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

// unknownDescIndex returns the unknownDesc offset whose posting
// address matches p, or -1.
func (pr *postingResolution) unknownDescIndex(p *ast.Posting) int {
	for i, ref := range pr.unknownDesc {
		if &pr.postings[ref.postingAt] == p {
			return i
		}
	}
	return -1
}

// Run walks the directives without a visitor, returning the booked
// directive output and any collected diagnostics. It is equivalent to
// calling [Reducer.Walk] with a nil visitor; see Walk for the
// system-error contract on the third return value.
func (r *Reducer) Run() ([]ast.Directive, []ast.Diagnostic, error) {
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

// Errors returns the diagnostics collected by the most recent
// [Reducer.Run] or [Reducer.Walk]. The returned slice is a fresh copy;
// callers may retain it and mutate it without affecting the reducer.
func (r *Reducer) Errors() []ast.Diagnostic {
	return append([]ast.Diagnostic(nil), r.diags...)
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

// Inspect reconstructs a single transaction's view by re-walking
// the directives sequence up to txn. txn is matched by pointer
// identity against the transactions yielded by the sequence; an
// equivalent-but-not-identical pointer will not match.
//
// Each call costs O(N) and is intended for bean-doctor-style
// trouble-shooting; for repeated inspections prefer [Reducer.Walk]
// with a visitor. The returned diagnostics slice covers everything
// up to (and including) the stopping point. The error return is
// reserved for booking-pass implementation bugs (see [Reducer.Walk]).
//
// Returns (nil, diags, err) when txn is not found. The reducer's
// internal state is reset on every subsequent [Reducer.Walk] or
// [Reducer.Run], so the mid-walk state Inspect leaves behind does
// not affect later calls.
func (r *Reducer) Inspect(txn *ast.Transaction) (*Inspection, []ast.Diagnostic, error) {
	if txn == nil {
		return nil, nil, nil
	}
	var hit *Inspection
	_, diags, err := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
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
		return nil, diags, err
	}
	return hit, diags, err
}

// cloneInventoryMap duplicates the map spine; values are already
// deep-cloned by Walk and remain safe to retain.
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
