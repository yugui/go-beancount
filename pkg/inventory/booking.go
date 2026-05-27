package inventory

import (
	"fmt"
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
	// Lot is the booked cost for an augmentation; nil for cash
	// augmentations and reductions.
	Lot *Cost
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
// At most one of the third (user finding) and fourth (system error)
// returns is non-nil. The error slot is reserved for booking-pass
// implementation bugs and invariant violations (nil posting, nil
// Amount, unknown classify kind, apd arithmetic from inputs the
// grammar cannot produce) — the caller must halt the booking pass on
// non-nil err. The reducer rejects auto+cost/price combinations
// upstream with [CodeInvalidAutoPosting]; bookOne does not enforce
// it.
func bookOne(
	inv *Inventory,
	p *ast.Posting,
	method ast.BookingMethod,
	txnDate time.Time,
) (*Cost, []ReductionStep, *ast.Diagnostic, error) {
	if p == nil {
		return nil, nil, nil, fmt.Errorf("inventory.bookOne: nil posting")
	}
	if p.Amount == nil {
		return nil, nil, nil, fmt.Errorf("inventory.bookOne: nil Amount; auto-postings must be resolved before booking")
	}

	switch classify(inv, p, method) {
	case kindAugment:
		lot, finding, err := bookAugment(inv, p, txnDate)
		return lot, nil, finding, err
	case kindReduce:
		steps, finding, err := bookReduce(inv, p, method)
		return nil, steps, finding, err
	default:
		return nil, nil, nil, fmt.Errorf("inventory.bookOne: classify returned an unknown kind")
	}
}

// bookAugment handles the augmentation path of bookOne: resolve the
// cost spec, build a Position, and Add it to the inventory. Returns
// the resolved lot (nil iff p carried no cost spec — a cash
// augmentation). See [bookOne] for the user-finding / system-error
// split.
func bookAugment(inv *Inventory, p *ast.Posting, txnDate time.Time) (*Cost, *ast.Diagnostic, error) {
	lot, finding, err := ResolveCost(p.Cost, *p.Amount, txnDate)
	if err != nil {
		return nil, nil, wrapSystemErr(err, p)
	}
	if finding != nil {
		return nil, enrichDiagnostic(finding, p), nil
	}

	pos := Position{
		Units: *p.Amount.Clone(),
		Cost:  lot,
	}

	if inv != nil {
		if err := inv.Add(pos); err != nil {
			// Inventory.Add only returns system errors.
			return nil, nil, wrapSystemErr(err, p)
		}
	}

	return lot, nil, nil
}

// bookReduce is the reduction path of bookOne: build a matcher, call
// [Inventory.Reduce], then enrich each step with sale price and
// realized gain from p.Price. See [bookOne] for the user-finding /
// system-error split.
func bookReduce(
	inv *Inventory,
	p *ast.Posting,
	method ast.BookingMethod,
) ([]ReductionStep, *ast.Diagnostic, error) {
	priceCcy := ""
	if specIsEmpty(p.Cost) && p.Price != nil {
		priceCcy = p.Price.Amount.Currency
	}
	matcher := NewCostMatcher(p.Cost, priceCcy, p.Amount)

	steps, finding, err := inv.Reduce(*p.Amount, matcher, method)
	if err != nil {
		return nil, nil, wrapSystemErr(err, p)
	}
	if finding != nil {
		return nil, enrichDiagnostic(finding, p), nil
	}

	if p.Price != nil && p.Price.Amount.Currency != "" {
		if err := fillRealizedGain(steps, p); err != nil {
			return nil, nil, err
		}
	}

	return steps, nil, nil
}

// fillRealizedGain populates SalePricePer, RealizedGain, and
// GainCurrency on each step from p.Price. salePricePer is
// p.Price.Amount.Number for `@` (per-unit) and p.Price.Amount.Number /
// |p.Amount.Number| (via [quoContext]) for `@@` (total). RealizedGain
// is (salePricePer - step.Lot.Number) * step.Units.
//
// All apd.BaseContext failures here are unreachable from valid grammar
// inputs, so the error return is always a system error rather than a
// Diagnostic.
func fillRealizedGain(steps []ReductionStep, p *ast.Posting) error {
	var salePricePer *apd.Decimal
	if p.Price.IsTotal {
		absUnits := new(apd.Decimal)
		unitsNum := p.Amount.Number
		if _, err := apd.BaseContext.Abs(absUnits, &unitsNum); err != nil {
			return fmt.Errorf("inventory.fillRealizedGain: abs units: %w", err)
		}
		if absUnits.Sign() == 0 {
			// avoid div-by-zero
			return nil
		}
		salePricePer = new(apd.Decimal)
		total := p.Price.Amount.Number
		if _, err := quoContext.Quo(salePricePer, &total, absUnits); err != nil {
			return fmt.Errorf("inventory.fillRealizedGain: divide total price: %w", err)
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
			return fmt.Errorf("inventory.fillRealizedGain: sub lot cost: %w", err)
		}
		gain := new(apd.Decimal)
		units := step.Units
		if _, err := apd.BaseContext.Mul(gain, diff, &units); err != nil {
			return fmt.Errorf("inventory.fillRealizedGain: mul units: %w", err)
		}
		step.RealizedGain = gain
		step.GainCurrency = currency
	}
	return nil
}
