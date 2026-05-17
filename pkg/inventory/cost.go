package inventory

import (
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// quoContext is the apd context used for per-unit cost division. The
// package-wide [apd.BaseContext] has Precision=0, which only works for
// exact operations (Add/Sub/Mul/Neg/Abs). Division (Quo) needs a
// positive precision; 34 digits matches IEEE-754 decimal128 and is
// well above the practical ledger use case.
var quoContext = apd.BaseContext.WithPrecision(34)

// Cost is the booked, fully-resolved cost of a posting. The canonical
// type lives in [pkg/ast]; this alias is the inventory-side spelling.
type Cost = ast.Cost

// Lot is the augmentation-flavoured alias for [Cost]: an augmenting
// posting "adds a lot" while a reducing posting "matches a lot".
type Lot = ast.Lot

// ResolveCost turns an [ast.CostHolder] on an augmenting posting into
// a concrete [Cost]:
//
//   - nil c: returns (nil, nil) — a cash augmentation.
//   - *[ast.Cost]: returns a clone — the reducer is re-entering its
//     own output.
//   - *[ast.CostSpec] with PerUnit and Total both nil: returns
//     [CodeAugmentationRequiresCost]. Reductions take the
//     [CostMatcher] path instead.
//   - *[ast.CostSpec] otherwise: derives Number from the spec — X for
//     per-unit-only, T/|units| for total-only, X + T/|units| for the
//     combined form.
//
// On the CostSpec path the returned [ast.Cost] retains the spec's
// PerUnit / Total literals so the printer round-trips the surcharge
// form. Number is always positive. Date defaults to txnDate; Label is
// copied verbatim.
func ResolveCost(c ast.CostHolder, units ast.Amount, txnDate time.Time) (*Cost, error) {
	if c == nil {
		return nil, nil
	}
	if cost, ok := c.(*ast.Cost); ok {
		return cost.Clone(), nil
	}
	spec := c.(*ast.CostSpec)
	if spec.PerUnit == nil && spec.Total == nil {
		return nil, Error{
			Code:    CodeAugmentationRequiresCost,
			Span:    spec.Span,
			Message: "augmenting posting has an empty cost spec; a concrete cost is required",
		}
	}

	out := &Cost{Currency: spec.Currency}
	if spec.Date != nil && !spec.Date.IsZero() {
		out.Date = *spec.Date
	} else {
		out.Date = txnDate
	}
	out.Label = spec.Label
	out.PerUnit = spec.GetPerUnit()
	out.Total = spec.GetTotal()

	absUnits := new(apd.Decimal)
	unitsNum := units.Number
	if _, err := apd.BaseContext.Abs(absUnits, &unitsNum); err != nil {
		return nil, Error{
			Code:    CodeInternalError,
			Span:    spec.Span,
			Message: "abs units: " + err.Error(),
		}
	}

	switch {
	case spec.PerUnit != nil && spec.Total != nil:
		quo := new(apd.Decimal)
		if _, err := quoContext.Quo(quo, spec.Total, absUnits); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "divide total by units: " + err.Error(),
			}
		}
		if _, err := apd.BaseContext.Add(&out.Number, spec.PerUnit, quo); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "add per-unit and residual: " + err.Error(),
			}
		}
	case spec.Total != nil:
		if _, err := quoContext.Quo(&out.Number, spec.Total, absUnits); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "divide total by units: " + err.Error(),
			}
		}
	default:
		out.Number = *ast.CloneDecimal(spec.PerUnit)
	}

	return out, nil
}
