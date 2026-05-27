package inventory

import (
	"fmt"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Cost is the booked, fully-resolved cost of a posting. The canonical
// type lives in [pkg/ast]; this alias is the inventory-side spelling.
type Cost = ast.Cost

// ResolveLot turns an [ast.CostHolder] on an augmenting posting into
// a provenance-free [Lot]:
//
//   - nil c: returns (nil, nil, nil) — a cash augmentation.
//   - *[ast.Cost]: returns a fresh [Lot] carrying only the booked
//     identity (Number/Currency/Date/Label); PerUnit/Total are
//     discarded and txnDate is ignored (the booked Date is preserved
//     verbatim). The reducer is re-entering its own output.
//   - *[ast.CostSpec] with PerUnit and Total both nil: returns a
//     [CodeAugmentationRequiresCost] finding. Reductions take the
//     [CostMatcher] path instead.
//   - *[ast.CostSpec] with a non-nil Total but |units| == 0: returns a
//     [CodeZeroUnitsCostTotal] finding. Per-unit cost (Total/units)
//     is undefined.
//   - *[ast.CostSpec] otherwise: derives Number from the spec — X for
//     per-unit-only, T/|units| for total-only, X + T/|units| for the
//     combined form. Number is always positive. Date defaults to
//     txnDate; Label is copied verbatim.
//
// The returned [Lot] never carries presentation provenance. Augmenting
// installers route the spec separately to construct the AST-tier
// [ast.Cost] that round-trips surcharge syntax.
//
// At most one of the second (user finding) and third (system error)
// returns is non-nil. The error return is reserved for implementation
// bugs — apd.BaseContext arithmetic failures from inputs the grammar
// cannot produce.
func ResolveLot(c ast.CostHolder, units ast.Amount, txnDate time.Time) (*Lot, *ast.Diagnostic, error) {
	if c == nil {
		return nil, nil, nil
	}
	if cost, ok := c.(*ast.Cost); ok {
		return LotFromCost(cost), nil, nil
	}
	spec := c.(*ast.CostSpec)
	if spec.PerUnit == nil && spec.Total == nil {
		return nil, &ast.Diagnostic{
			Code:    CodeAugmentationRequiresCost,
			Span:    spec.Span,
			Message: "augmenting posting has an empty cost spec; a concrete cost is required",
		}, nil
	}

	out := &Lot{Currency: spec.Currency, Label: spec.Label}
	if spec.Date != nil && !spec.Date.IsZero() {
		out.Date = *spec.Date
	} else {
		out.Date = txnDate
	}

	// Diagnostic-level zero-units check before PerUnitCost, which would
	// otherwise surface this as a plain arithmetic error.
	if spec.Total != nil {
		absUnits := new(apd.Decimal)
		unitsNum := units.Number
		if _, err := apd.BaseContext.Abs(absUnits, &unitsNum); err != nil {
			return nil, nil, fmt.Errorf("inventory.ResolveLot: abs units: %w", err)
		}
		if absUnits.Sign() == 0 {
			return nil, &ast.Diagnostic{
				Code:    CodeZeroUnitsCostTotal,
				Span:    spec.Span,
				Message: "augmenting posting with total cost has zero units; per-unit cost is undefined",
			}, nil
		}
	}

	perUnit, err := ast.PerUnitCost(spec, &units)
	if err != nil {
		return nil, nil, fmt.Errorf("inventory.ResolveLot: %w", err)
	}
	out.Number = *ast.CloneDecimal(&perUnit.Number)

	return out, nil, nil
}
