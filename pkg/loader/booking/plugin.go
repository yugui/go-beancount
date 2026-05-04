// Package booking implements the loader-level booking pass that
// resolves incomplete cost specs (date-only, label-only, empty) on
// reducing postings into concrete CostSpec values before user plugins
// and the validation pipeline see the AST.
//
// This plugin is the Go equivalent of upstream beancount's
// `booking.book` step in `loader._load`: it runs the inventory
// reducer over the ledger, then writes the booked lot information
// back into each posting's CostSpec so downstream consumers
// (transaction-balance validation, BQL, the printer) observe a fully
// resolved AST.
//
// Behavior per posting:
//
//   - Augmenting postings whose CostSpec lacks a concrete per-unit or
//     total amount get a synthesized CostSpec.PerUnit from the resolved
//     lot. Date and Label fields on the original spec are preserved.
//   - Reducing postings — those whose lot was matched against existing
//     inventory — get a synthesized CostSpec.Total equal to
//     Σ |step.Units| × step.Lot.Number across all matched lots. PerUnit
//     is cleared so the validation layer's weightFromTotal branch
//     produces the correct cost-currency weight regardless of how many
//     lots were consumed. Date and Label on the original spec are
//     preserved as matching constraints.
//   - Auto-balanced postings (nil Amount) have their Amount filled in
//     place by the reducer; no CostSpec edits are needed.
//   - Postings without a CostSpec are left untouched.
//
// The plugin is idempotent: re-running it on a ledger that has already
// been booked produces the same AST and the same diagnostics, because
// (a) the reducer is deterministic given the same input, and (b) Total
// aggregation reads from the booked lots, not from the prior CostSpec.
package booking

import (
	"context"
	"fmt"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// Apply runs the booking pass over the input directives. It deep-clones
// every transaction so the reducer's in-place mutations to auto-posting
// Amount fields and our per-posting CostSpec writes do not touch the
// caller's directives. All non-Transaction directives are forwarded by
// reference; they have no fields the reducer could disturb.
//
// Reducer-emitted errors are surfaced as ast.Diagnostic entries on the
// returned Result so the load pipeline can continue and surface them
// alongside other validation findings, mirroring the contract used by
// the pad and balance plugins.
func Apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// Materialize directives and deep-clone every Transaction so the
	// reducer can mutate without touching the caller's AST.
	var cloned []ast.Directive
	for _, d := range in.Directives {
		if txn, ok := d.(*ast.Transaction); ok {
			cloned = append(cloned, txn.Clone())
			continue
		}
		cloned = append(cloned, d)
	}

	// Build a fresh Ledger over the cloned directives. The reducer
	// walks via Ledger.All(), so a temporary ledger is the simplest
	// adapter from the plugin's iter.Seq2 input.
	work := &ast.Ledger{}
	work.InsertAll(cloned)

	// visit writes booked lot information back into each Transaction's
	// CostSpecs. The reducer passes BookedPosting.Source as a pointer
	// directly into the cloned transaction's Postings slice, so we
	// write through Source without needing to look up by index.
	visit := func(_ *ast.Transaction, _, _ map[ast.Account]*inventory.Inventory, booked []inventory.BookedPosting) bool {
		for i := range booked {
			writeBackCost(&booked[i])
		}
		return true
	}

	errs := inventory.NewReducer(work).Walk(visit)

	diags := make([]ast.Diagnostic, 0, len(errs))
	for _, e := range errs {
		diags = append(diags, ast.Diagnostic{
			Code:    diagnosticCode(e.Code),
			Span:    e.Span,
			Message: bookingMessage(e),
		})
	}

	return api.Result{Directives: cloned, Diagnostics: diags}, nil
}

// writeBackCost folds the booking decision recorded in bp into the AST
// CostSpec on bp.Source. Postings that were neither augmented nor
// reduced (e.g. cash, no-cost) are left untouched.
func writeBackCost(bp *inventory.BookedPosting) {
	p := bp.Source
	if p == nil {
		return
	}
	switch {
	case len(bp.Reductions) > 0:
		writeReductionCost(p, bp.Reductions)
	case bp.Lot != nil:
		writeAugmentationCost(p, bp.Lot)
	}
}

// writeAugmentationCost ensures p.Cost reflects the concrete per-unit
// lot resolved during augmentation. Date and Label on the original
// spec — which the user wrote and which downstream tooling (printer,
// BQL filters) treats as authoritative — are preserved. Total is
// cleared because the resolved Cost is canonically stored as a per-
// unit amount; keeping a stale Total alongside PerUnit would imply the
// combined "{X # Y CUR}" form which is not what augmentation produces.
func writeAugmentationCost(p *ast.Posting, lot *inventory.Cost) {
	if p.Cost == nil {
		// The user wrote no cost spec at all; nothing to augment back
		// into. The reducer's lot is implicit cash-with-no-cost from
		// some upstream perspective, but our AST has nowhere to put a
		// PerUnit without inventing a CostSpec. Leave the AST alone.
		return
	}
	num := apd.Decimal{}
	num.Set(&lot.Number)
	p.Cost.PerUnit = &ast.Amount{
		Number:   num,
		Currency: lot.Currency,
	}
	p.Cost.Total = nil
}

// writeReductionCost synthesizes p.Cost.Total from the per-step
// magnitudes returned by the reducer:
//
//	total = Σ |step.Units| × step.Lot.Number
//
// All steps share the same cost currency by construction (the matcher
// filtered candidates by currency hint; mixed-currency reductions
// would have been flagged earlier). PerUnit is cleared so the
// validation layer's weightFromTotal branch — which returns
// sign(units) × |total| — produces a cost-currency weight that
// matches upstream beancount's check_balance regardless of how many
// lots the reduction consumed.
func writeReductionCost(p *ast.Posting, steps []inventory.ReductionStep) {
	if p.Cost == nil || len(steps) == 0 {
		return
	}
	var total apd.Decimal
	currency := steps[0].Lot.Currency
	for i := range steps {
		s := &steps[i]
		var part apd.Decimal
		// step.Units is already non-negative magnitude per the reducer
		// contract, so part = step.Units * step.Lot.Number is positive
		// (assuming a normal long lot with positive Number).
		if _, err := apd.BaseContext.Mul(&part, &s.Units, &s.Lot.Number); err != nil {
			// Arithmetic on apd.BaseContext fails only on
			// pathological exponents that the parser would never
			// admit. Bail without writing back rather than corrupt
			// the AST with a partial sum.
			return
		}
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &total, &part); err != nil {
			// Same rationale as the Mul branch above: the partial sum
			// is local and never reaches p.Cost, so an early return
			// leaves the AST untouched.
			return
		}
		total.Set(&sum)
	}
	p.Cost.Total = &ast.Amount{
		Number:   total,
		Currency: currency,
	}
	p.Cost.PerUnit = nil
}

// diagnosticCode maps an inventory error code to the diagnostic Code
// string surfaced through the ledger's diagnostics channel. The shape
// matches the validation package's existing codes where one exists,
// so users see a consistent vocabulary.
func diagnosticCode(c inventory.Code) string {
	switch c {
	case inventory.CodeAmbiguousLotMatch:
		return "ambiguous-lot-match"
	case inventory.CodeNoMatchingLot:
		return "no-matching-lot"
	case inventory.CodeReductionExceedsInventory:
		return "reduction-exceeds-inventory"
	case inventory.CodeAugmentationRequiresCost:
		return "augmentation-requires-cost"
	case inventory.CodeMultipleAutoPostings:
		return "multiple-auto-postings"
	case inventory.CodeUnresolvableAutoPosting:
		return "unresolvable-auto-posting"
	case inventory.CodeInvalidAutoPosting:
		return "invalid-auto-posting"
	case inventory.CodeMixedInventory:
		return "mixed-inventory"
	case inventory.CodeInvalidBookingMethod:
		return "invalid-booking-method"
	default:
		return "internal-error"
	}
}

// bookingMessage formats the inventory error's message with the
// account prefix folded in, matching the convention used by
// validation.FromInventoryError. The Span on the diagnostic carries
// the source location, so we deliberately omit filename:line:col from
// the message text.
func bookingMessage(e inventory.Error) string {
	if e.Account != "" {
		return fmt.Sprintf("%s: %s", e.Account, e.Message)
	}
	return e.Message
}
