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
// type lives in [pkg/ast]; this alias preserves the inventory.Cost
// spelling at existing call sites while the AST is the single source
// of truth for the type definition, its methods, and its place in the
// [ast.CostHolder] sealed union. Code that wants the "lot" spelling
// should reference [Lot] (an alias for the same underlying type).
type Cost = ast.Cost

// Lot is the augmentation-flavoured alias for [Cost]. It points at the
// same underlying [ast.Cost] type; the dual spelling matches the booking
// vocabulary where an augmenting posting "adds a lot" while a reducing
// posting "matches a lot".
type Lot = ast.Lot

// ResolveCost turns an [ast.CostHolder] on an augmenting posting into
// a concrete [Cost]. The two CostHolder variants are handled
// uniformly so callers (bookAugment, the reducer's terminal pass) do
// not have to type-switch:
//
//   - c is nil                       -> (nil, nil). No cost lot.
//   - c is *[ast.Cost]               -> c.Clone(). Already resolved;
//     the reducer is re-entering its own output and the canonical
//     [ast.Cost.Number] is preserved as-is.
//   - c is *[ast.CostSpec] with no PerUnit and no Total (empty "{}"
//     on an augmentation) -> [Error] with
//     [CodeAugmentationRequiresCost]. Reductions never call
//     ResolveCost for this case; they build a [CostMatcher] instead.
//   - per-unit only ({X CUR})                     -> Number = X
//   - total only      ({{T CUR}})                 -> Number = T / |units|
//   - combined form   ({X # T CUR})               -> Number = X + T/|units|
//
// For the CostSpec branches, the returned [ast.Cost] also retains the
// user's PerUnit / Total literals so the printer round-trips the
// surcharge form. The computed Number is always positive (cost is a
// magnitude). The Date defaults to txnDate when spec.Date is unset;
// Label is copied verbatim.
func ResolveCost(c ast.CostHolder, units ast.Amount, txnDate time.Time) (*Cost, error) {
	if c == nil {
		return nil, nil
	}
	if cost, ok := c.(*ast.Cost); ok {
		return cost.Clone(), nil
	}
	spec, ok := c.(*ast.CostSpec)
	if !ok {
		// Unreachable under the sealed CostHolder union: only the
		// two variants above satisfy isCostHolder. The check is
		// defensive against a future extension that forgets to
		// update this dispatch.
		return nil, Error{
			Code:    CodeInternalError,
			Message: "ResolveCost: unknown CostHolder concrete type",
		}
	}
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
	// Retain the user's syntactic form so the printer round-trips
	// surcharge / total-only / per-unit-only after booking.
	out.PerUnit = spec.GetPerUnit()
	out.Total = spec.GetTotal()

	// |units| is used as the denominator for the total-to-per-unit
	// division. Compute it once.
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
		// Combined form: per + total / |units|.
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
		// {{T CUR}} -> T / |units|
		if _, err := quoContext.Quo(&out.Number, spec.Total, absUnits); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "divide total by units: " + err.Error(),
			}
		}
	default:
		// Per-unit only: copy verbatim.
		out.Number = *ast.CloneDecimal(spec.PerUnit)
	}

	return out, nil
}
