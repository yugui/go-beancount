package inventory

import (
	"errors"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// specIsEmpty reports whether a cost spec is structurally empty, i.e.
// it carries no per-unit, no total, no date, and no label. A nil spec
// and an empty "{}" spec both count as empty for the purpose of the
// cost-currency hint rule: in both cases the posting has no
// cost-selection constraint of its own, so when a price annotation is
// present its currency is used as the cost currency to match against.
func specIsEmpty(c *ast.CostSpec) bool {
	return c == nil || (c.PerUnit == nil && c.Total == nil && c.Date == nil && c.Label == "")
}

// costNumberMissing reports whether a cost spec has neither a per-unit
// nor a total cost number, signalling that the user wants a lot tracked
// but expects the booking layer to fill in the cost from context. A
// non-nil spec with at least one of PerUnit or Total set is concrete
// enough for [ResolveCost] to handle on its own and is therefore not
// "missing" in this sense; a nil spec means no cost was requested at
// all (cash/no-lot augmentation) and is also not "missing". Date and
// Label are ignored: both `{}` (lot-tracked, cost TBD) and
// `{2025-01-01, "label"}` (lot identity stated, cost TBD) qualify.
func costNumberMissing(c *ast.CostSpec) bool {
	return c != nil && c.PerUnit == nil && c.Total == nil
}

// BookedPosting is the result of routing a single [ast.Posting] through
// an inventory. Augmenting postings carry a Lot; reducing postings carry
// a list of [ReductionStep] entries; cash / no-cost postings carry
// neither. Source aliases into the originating transaction — bookOne
// never copies the posting — so callers may index back into the
// transaction via Source without a reverse lookup.
type BookedPosting struct {
	// Source is the posting this record was booked from. It is an
	// alias into the originating ast.Transaction; it is never a copy.
	Source *ast.Posting
	// Account is the account the posting was recorded against.
	Account ast.Account
	// Units is the signed unit amount routed through the inventory.
	// For explicit postings this mirrors Source.Amount; for auto-
	// postings it is the residual amount inferred by the reducer
	// (see InferredAuto).
	Units ast.Amount
	// Lot is the resolved cost lot, set iff this was an augmentation
	// whose posting carried a cost spec. For cash augmentations and
	// reductions this is nil.
	Lot *Cost
	// Reductions is the per-lot breakdown of a reducing posting. It
	// is non-nil iff the posting was classified as a reduction.
	Reductions []ReductionStep
	// InferredAuto is true when Units was inferred from the residual
	// of a transaction's other postings rather than read directly
	// from the posting's Amount field.
	InferredAuto bool
}

// kind tags the routing decision made by [classify]: either the
// posting augments the inventory with a new (or merged) lot, or it
// reduces existing lots via the account's booking method.
type kind int

const (
	kindAugment kind = iota
	kindReduce
)

// classify decides whether a posting augments or reduces the given
// inventory under booking method m. BookingNone always augments: a
// sign-opposite posting under NONE creates a negative-units lot — i.e.
// a short position — rather than reducing an existing lot.
//
// The cost-currency hint is computed from the posting:
//
//   - explicit p.Cost.PerUnit.Currency or p.Cost.Total.Currency (the
//     lowerer guarantees they agree when both are present);
//   - fallback: a structurally-empty cost spec (absent or "{}") paired
//     with a price annotation uses p.Price.Amount.Currency. This keeps
//     classify's hint in sync with the matcher's empty-spec fallback in
//     matcher.go, which also falls back to the price currency;
//   - otherwise "" (commodity-only classification).
//
// If a hint is available the candidate lots are filtered to that cost
// currency before the sign check. Otherwise the check uses every
// position for the commodity.
//
// If no existing positions remain after filtering, the posting is
// classified as an augmentation. If existing positions have the SAME
// sign as the posting's amount, it is an augmentation; the OPPOSITE
// sign is a reduction. A zero-sign posting is treated as an
// augmentation because "same sign" cannot be established.
//
// inv may be nil, which is treated as an empty inventory (all postings
// are augmentations). p.Amount must be non-nil; auto-postings are
// handled by the reducer in Pass 2 before this function is reached.
func classify(inv *Inventory, p *ast.Posting, m ast.BookingMethod) kind {
	if m == ast.BookingNone {
		return kindAugment
	}
	if p == nil || p.Amount == nil {
		// Auto-postings must be resolved before reaching classify.
		// Defend against the invariant violation with a descriptive
		// panic rather than silently returning augmentation: a nil
		// amount here means the reducer missed a pass.
		panic("inventory.classify: posting has nil Amount (auto-postings must be resolved upstream)")
	}
	if inv == nil {
		return kindAugment
	}

	// Compute the cost-currency hint from the posting. A structurally-
	// empty cost spec ({}) is equivalent to no spec for hinting
	// purposes: the price annotation supplies the currency so the
	// matcher's empty-{} fallback is reachable from bookOne.
	hintCcy := ""
	switch {
	case p.Cost != nil && p.Cost.PerUnit != nil:
		hintCcy = p.Cost.PerUnit.Currency
	case p.Cost != nil && p.Cost.Total != nil:
		hintCcy = p.Cost.Total.Currency
	case specIsEmpty(p.Cost) && p.Price != nil:
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
	postingSign := p.Amount.Number.Sign()
	if postingSign == 0 {
		// Zero-unit posting: no opposite direction to detect, treat
		// as augmentation. bookOne decides whether to emit a lot or
		// reject it.
		return kindAugment
	}
	// Existing positions may contain both signs under exotic ledgers;
	// use the first candidate's sign as the reference, matching the
	// plan pseudocode.
	existingSign := existing[0].Units.Number.Sign()
	if existingSign == postingSign {
		return kindAugment
	}
	return kindReduce
}

// bookOne routes a single posting through inv, mutating inv as needed,
// and returns the [BookedPosting] record plus any inventory errors.
// bookOne does NOT mutate the backing ast.Posting.
//
// Augmentation path: resolves the cost spec via [ResolveCost], creates
// a [Position], and calls [Inventory.Add]. The returned record has Lot
// set iff a cost was resolved.
//
// Reduction path: builds a [CostMatcher] from the spec and, when the
// spec is structurally empty but a price annotation is present, from
// that price's currency (the cost-currency hint rule). It then calls
// [Inventory.Reduce] under method and fills in SalePricePer,
// RealizedGain, and GainCurrency on each [ReductionStep] from the
// posting's price annotation when present.
//
// txnDate is used as the default acquisition date for augmentations
// whose cost spec omits an explicit date. method is the caller-
// resolved booking method for the posting's account; the reducer
// tracks per-account state and passes the right one in. inferred
// populates the BookedPosting's InferredAuto field verbatim.
//
// Preconditions:
//
//   - p.Amount must be non-nil. Auto-postings are resolved by the
//     reducer (Pass 2) before bookOne is called. A nil amount returns
//     a [CodeInternalError] rather than panicking so the reducer can
//     report it through the normal diagnostics channel.
//   - An auto-posting that also carries a cost or price spec is
//     structurally invalid and must be rejected upstream by the reducer
//     with [CodeInvalidAutoPosting]; bookOne does not enforce it.
func bookOne(
	inv *Inventory,
	p *ast.Posting,
	method ast.BookingMethod,
	txnDate time.Time,
	inferred bool,
) (BookedPosting, []Error) {
	if p == nil {
		return BookedPosting{}, []Error{{
			Code:    CodeInternalError,
			Message: "bookOne called with a nil posting",
		}}
	}
	if p.Amount == nil {
		return BookedPosting{}, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "bookOne called with a nil Amount; auto-postings must be resolved before booking",
		}}
	}

	booked := BookedPosting{
		Source:       p,
		Account:      p.Account,
		Units:        *p.Amount.Clone(),
		InferredAuto: inferred,
	}

	switch classify(inv, p, method) {
	case kindAugment:
		return bookAugment(inv, p, booked, txnDate)
	case kindReduce:
		return bookReduce(inv, p, booked, method)
	default:
		return BookedPosting{}, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "bookOne: classify returned an unknown kind",
		}}
	}
}

// bookAugment handles the augmentation path of bookOne: resolve the
// cost spec, build a Position, and Add it to the inventory.
func bookAugment(
	inv *Inventory,
	p *ast.Posting,
	booked BookedPosting,
	txnDate time.Time,
) (BookedPosting, []Error) {
	lot, err := ResolveCost(p.Cost, *p.Amount, txnDate)
	if err != nil {
		// ResolveCost returns inventory.Error values already; enrich
		// them with the posting's span and account which ResolveCost
		// does not know about. errors.As is used (rather than a
		// direct type assertion) so wrapped Errors are still matched
		// even though nothing wraps them today.
		var invErr Error
		if errors.As(err, &invErr) {
			if invErr.Span == (ast.Span{}) {
				invErr.Span = p.Span
			}
			if invErr.Account == "" {
				invErr.Account = p.Account
			}
			return BookedPosting{}, []Error{invErr}
		}
		return BookedPosting{}, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "resolve cost: " + err.Error(),
		}}
	}

	// Inventory.Add clones the position on append, so the value copy
	// of booked.Units here is safe: the stored Position will not alias
	// the BookedPosting's coefficient buffer.
	pos := Position{
		Units: booked.Units,
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
				return BookedPosting{}, []Error{invErr}
			}
			return BookedPosting{}, []Error{{
				Code:    CodeInternalError,
				Span:    p.Span,
				Account: p.Account,
				Message: "inventory add: " + err.Error(),
			}}
		}
	}

	booked.Lot = lot
	return booked, nil
}

// bookReduce handles the reduction path of bookOne: build a matcher,
// call Inventory.Reduce, then enrich each step with sale price and
// realized gain from the posting's price annotation.
//
// The price-currency hint is forwarded to the matcher whenever the
// cost spec is structurally empty (absent or "{}"). This mirrors the
// hint gate in classify so that the matcher's empty-spec fallback in
// matcher.go — which picks the cost currency from the price annotation
// when no explicit cost currency is given — is actually reachable from
// bookOne.
func bookReduce(
	inv *Inventory,
	p *ast.Posting,
	booked BookedPosting,
	method ast.BookingMethod,
) (BookedPosting, []Error) {
	priceCcy := ""
	if specIsEmpty(p.Cost) && p.Price != nil {
		priceCcy = p.Price.Amount.Currency
	}
	matcher := NewCostMatcher(p.Cost, priceCcy)

	if inv == nil {
		// A reduction against a nil inventory is structurally
		// impossible — classify would have routed this to augment —
		// but defend the invariant rather than crash.
		return BookedPosting{}, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "bookReduce called with a nil inventory",
		}}
	}

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
			return BookedPosting{}, []Error{invErr}
		}
		return BookedPosting{}, []Error{{
			Code:    CodeInternalError,
			Span:    p.Span,
			Account: p.Account,
			Message: "inventory reduce: " + err.Error(),
		}}
	}

	// Enrich each step with sale price and realized gain, if the
	// posting carries a usable price annotation.
	if p.Price != nil && p.Price.Amount.Currency != "" {
		if errs := fillRealizedGain(steps, p); len(errs) > 0 {
			return BookedPosting{}, errs
		}
	}

	booked.Reductions = steps
	return booked, nil
}

// fillRealizedGain computes SalePricePer, RealizedGain, and
// GainCurrency on each ReductionStep in steps, using the posting's
// price annotation. salePricePer is the per-unit sale price:
//
//   - p.Price.Amount.Number when p.Price.IsTotal is false (@ per-unit);
//   - p.Price.Amount.Number / |p.Amount.Number| when p.Price.IsTotal is
//     true (@@ total). The division uses quoContext (34-digit
//     precision) so the quotient is well-defined for any reasonable
//     ledger input.
//
// For each step, RealizedGain = (salePricePer - step.Lot.Number) *
// step.Units, computed with apd.BaseContext (exact Sub/Mul on value-
// stored decimals). GainCurrency is p.Price.Amount.Currency.
func fillRealizedGain(steps []ReductionStep, p *ast.Posting) []Error {
	var salePricePer *apd.Decimal
	if p.Price.IsTotal {
		// @@ total: salePricePer = total / |units|.
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
			// Cannot divide by zero. Leave gain fields unset.
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

		// SalePricePer: per-step copy so later edits on one step do
		// not alias another step's decimal.
		step.SalePricePer = ast.CloneDecimal(salePricePer)

		// RealizedGain = (salePricePer - lot.Number) * step.Units.
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
