package inventory

import (
	"errors"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// specIsEmpty reports whether c is nil or structurally empty (no
// per-unit, no total, no date, no label) — the cases for which the
// price-currency hint rule applies. A booked [*ast.Cost] is never
// empty.
func specIsEmpty(c ast.CostHolder) bool {
	if c == nil {
		return true
	}
	if c.GetPerUnit() != nil || c.GetTotal() != nil {
		return false
	}
	if _, ok := c.GetDate(); ok {
		return false
	}
	return c.GetLabel() == ""
}

// costNumberMissing reports whether c is a [*ast.CostSpec] with
// neither PerUnit nor Total — a deferred cost the booking layer must
// fill in. nil and booked [*ast.Cost] return false.
func costNumberMissing(c ast.CostHolder) bool {
	if c == nil {
		return false
	}
	if c.IsBooked() {
		return false
	}
	return c.GetPerUnit() == nil && c.GetTotal() == nil
}

// BookedPosting is the per-posting outcome the reducer publishes to
// its visitors. An augmenting posting carries a Lot; a reducing
// posting carries a Reduction; a cash / no-cost posting carries
// neither. Multi-lot reductions are expanded into one BookedPosting
// per matched lot.
type BookedPosting struct {
	// Source aliases the rebuilt posting in the transaction (never a
	// copy), so callers may index back into txn.Postings via Source
	// without a reverse lookup.
	Source *ast.Posting
	// Account is the account the posting was recorded against.
	Account ast.Account
	// Units is the signed amount routed through the inventory. For
	// auto-postings it is the residual inferred by the reducer (see
	// InferredAuto).
	Units ast.Amount
	// Lot is the resolved cost lot for an augmentation; nil for cash
	// augmentations and reductions.
	Lot *Lot
	// Reduction is the per-lot reduction record; nil unless the
	// posting was classified as a reduction.
	Reduction *ReductionStep
	// InferredAuto is true when Units was inferred from the residual
	// of the transaction's other postings.
	InferredAuto bool
}

// kind tags the routing decision made by [classify].
type kind int

const (
	kindAugment kind = iota
	kindReduce
)

// classify decides whether a posting augments or reduces inv under
// booking method m. BookingNone always augments — a sign-opposite
// posting creates a short position rather than reducing an existing
// lot.
//
// The cost-currency hint, used to filter candidate lots before the
// sign check, is p.Cost.GetCurrency() when non-empty; otherwise, for a
// structurally-empty cost spec paired with a price annotation, the
// price's currency (matching the matcher-side fallback). With no
// hint, every position for the commodity is considered.
//
// A zero-sign posting and an empty candidate set both classify as
// augmentation. Otherwise the posting reduces iff its sign opposes
// the first candidate's.
//
// inv may be nil (treated as empty). p.Amount must be non-nil; auto-
// postings are resolved upstream by the reducer.
func classify(inv *Inventory, p *ast.Posting, m ast.BookingMethod) kind {
	if m == ast.BookingNone {
		return kindAugment
	}
	if p == nil || p.Amount == nil {
		// invariant: auto-postings resolved upstream.
		panic("inventory.classify: posting has nil Amount (auto-postings must be resolved upstream)")
	}
	if inv == nil {
		return kindAugment
	}

	hintCcy := ""
	if p.Cost != nil {
		hintCcy = p.Cost.GetCurrency()
	}
	if hintCcy == "" && specIsEmpty(p.Cost) && p.Price != nil {
		hintCcy = p.Price.Amount.Currency
	}

	existing := inv.Get(p.Amount.Currency)
	if hintCcy != "" {
		filtered := existing[:0:0]
		for _, pos := range existing {
			if pos.Cost != nil && pos.Cost.Currency == hintCcy {
				filtered = append(filtered, pos)
			}
		}
		existing = filtered
	}
	if len(existing) == 0 {
		return kindAugment
	}
	if p.Amount.Number.Sign() == 0 {
		return kindAugment
	}
	if existing[0].Units.Number.Sign() == p.Amount.Number.Sign() {
		return kindAugment
	}
	return kindReduce
}

// bookOne routes a single posting through inv (mutating it) and
// returns the booking outcome the reducer needs to assemble a
// [BookedPosting]: a resolved lot for an augmentation, or per-lot
// reduction steps for a reduction. bookOne does not mutate the
// backing ast.Posting and does not construct the BookedPosting — the
// CostSpec → Cost install and the BookedPosting shape live next to
// the txn.Postings rewrite in the reducer.
//
// txnDate is the default acquisition date for augmentations whose
// cost spec omits one. method is the booking method for the
// posting's account.
//
// Preconditions: p.Amount must be non-nil (a nil amount returns
// [CodeInternalError] rather than panicking, so the reducer can
// surface it). The reducer rejects auto+cost/price combinations
// upstream with [CodeInvalidAutoPosting]; bookOne does not enforce
// it.
func bookOne(
	inv *Inventory,
	p *ast.Posting,
	method ast.BookingMethod,
	txnDate time.Time,
) (lot *Lot, steps []ReductionStep, errs []Error) {
	if p == nil {
		return nil, nil, []Error{{
			Code:    CodeInternalError,
			Message: "bookOne called with a nil posting",
		}}
	}
	if p.Amount == nil {
		return nil, nil, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "bookOne called with a nil Amount; auto-postings must be resolved before booking",
		}}
	}

	switch classify(inv, p, method) {
	case kindAugment:
		lot, errs = bookAugment(inv, p, txnDate)
		return lot, nil, errs
	case kindReduce:
		steps, errs = bookReduce(inv, p, method)
		return nil, steps, errs
	default:
		return nil, nil, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "bookOne: classify returned an unknown kind",
		}}
	}
}

// bookAugment handles the augmentation path of bookOne: resolve the
// cost spec, build a Position, and Add it to the inventory. Returns
// the resolved lot (nil iff p carried no cost spec — a cash
// augmentation).
func bookAugment(
	inv *Inventory,
	p *ast.Posting,
	txnDate time.Time,
) (*Lot, []Error) {
	lot, err := ResolveCost(p.Cost, *p.Amount, txnDate)
	if err != nil {
		// enrich with span/account.
		var invErr Error
		if errors.As(err, &invErr) {
			if invErr.Span == (ast.Span{}) {
				invErr.Span = p.Span
			}
			if invErr.Account == "" {
				invErr.Account = p.Account
			}
			return nil, []Error{invErr}
		}
		return nil, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "resolve cost: " + err.Error(),
		}}
	}

	pos := Position{
		Units: *p.Amount.Clone(),
		Cost:  lot,
	}

	if inv != nil {
		if err := inv.Add(pos); err != nil {
			var invErr Error
			if errors.As(err, &invErr) {
				if invErr.Span == (ast.Span{}) {
					invErr.Span = p.Span
				}
				if invErr.Account == "" {
					invErr.Account = p.Account
				}
				return nil, []Error{invErr}
			}
			return nil, []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "inventory add: " + err.Error(),
			}}
		}
	}

	return lot, nil
}

// bookReduce is the reduction path of bookOne: build a matcher, call
// [Inventory.Reduce], then enrich each step with sale price and
// realized gain from p.Price.
func bookReduce(
	inv *Inventory,
	p *ast.Posting,
	method ast.BookingMethod,
) ([]ReductionStep, []Error) {
	priceCcy := ""
	if specIsEmpty(p.Cost) && p.Price != nil {
		priceCcy = p.Price.Amount.Currency
	}
	matcher := NewCostMatcher(p.Cost, priceCcy, p.Amount)

	steps, err := inv.Reduce(*p.Amount, matcher, method)
	if err != nil {
		var invErr Error
		if errors.As(err, &invErr) {
			if invErr.Span == (ast.Span{}) {
				invErr.Span = p.Span
			}
			if invErr.Account == "" {
				invErr.Account = p.Account
			}
			return nil, []Error{invErr}
		}
		return nil, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "inventory reduce: " + err.Error(),
		}}
	}

	if p.Price != nil && p.Price.Amount.Currency != "" {
		if errs := fillRealizedGain(steps, p); len(errs) > 0 {
			return nil, errs
		}
	}

	return steps, nil
}

// fillRealizedGain populates SalePricePer, RealizedGain, and
// GainCurrency on each step from p.Price. salePricePer is
// p.Price.Amount.Number for `@` (per-unit) and p.Price.Amount.Number /
// |p.Amount.Number| (via [quoContext]) for `@@` (total). RealizedGain
// is (salePricePer - step.Lot.Number) * step.Units.
func fillRealizedGain(steps []ReductionStep, p *ast.Posting) []Error {
	var salePricePer *apd.Decimal
	if p.Price.IsTotal {
		absUnits := new(apd.Decimal)
		unitsNum := p.Amount.Number
		if _, err := apd.BaseContext.Abs(absUnits, &unitsNum); err != nil {
			return []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "fill realized gain: abs units: " + err.Error(),
			}}
		}
		if absUnits.Sign() == 0 {
			// avoid div-by-zero
			return nil
		}
		salePricePer = new(apd.Decimal)
		total := p.Price.Amount.Number
		if _, err := quoContext.Quo(salePricePer, &total, absUnits); err != nil {
			return []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "fill realized gain: divide total price: " + err.Error(),
			}}
		}
	} else {
		salePricePer = ast.CloneDecimal(&p.Price.Amount.Number)
	}

	currency := p.Price.Amount.Currency
	for i := range steps {
		step := &steps[i]

		// avoid aliasing across steps.
		step.SalePricePer = ast.CloneDecimal(salePricePer)

		diff := new(apd.Decimal)
		lotNum := step.Lot.Number
		if _, err := apd.BaseContext.Sub(diff, salePricePer, &lotNum); err != nil {
			return []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "fill realized gain: sub lot cost: " + err.Error(),
			}}
		}
		gain := new(apd.Decimal)
		units := step.Units
		if _, err := apd.BaseContext.Mul(gain, diff, &units); err != nil {
			return []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "fill realized gain: mul units: " + err.Error(),
			}}
		}
		step.RealizedGain = gain
		step.GainCurrency = currency
	}
	return nil
}
