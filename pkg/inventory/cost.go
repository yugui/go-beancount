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
// should reference [ast.Lot].
type Cost = ast.Cost

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
//   - combined form   ({X # T CUR}, {X # T CUR})  -> Number = X + T/|units|
//
// For the CostSpec branches, the returned [ast.Cost] also retains the
// user's PerUnit / Total literals so the printer round-trips the
// surcharge form. The computed Number is always positive (cost is a
// magnitude). The Date defaults to txnDate when spec.Date is unset;
// Label is copied verbatim. When both PerUnit and Total are present
// the function defensively verifies that their currencies agree; a
// mismatch is reported as [CodeInternalError] because earlier
// parse/lower stages should have caught it.
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

	out := &Cost{}
	if spec.Date != nil && !spec.Date.IsZero() {
		out.Date = *spec.Date
	} else {
		out.Date = txnDate
	}
	out.Label = spec.Label
	// Retain the user's syntactic form so the printer round-trips
	// surcharge / total-only / per-unit-only after booking. The
	// retained Amounts are cloned (not aliased) so later AST edits
	// on the spec do not propagate to the booked Cost.
	out.PerUnit = spec.PerUnit.Clone()
	out.Total = spec.Total.Clone()

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
		// Combined form. Currency must agree between the two parts.
		if spec.PerUnit.Currency != spec.Total.Currency {
			return nil, Error{
				Code: CodeInternalError,
				Span: spec.Span,
				Message: "combined cost spec has mismatched currencies: " +
					spec.PerUnit.Currency + " vs " + spec.Total.Currency,
			}
		}
		out.Currency = spec.PerUnit.Currency
		// per + total / |units|
		perNum := spec.PerUnit.Number
		totalNum := spec.Total.Number
		quo := new(apd.Decimal)
		if _, err := quoContext.Quo(quo, &totalNum, absUnits); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "divide total by units: " + err.Error(),
			}
		}
		if _, err := apd.BaseContext.Add(&out.Number, &perNum, quo); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "add per-unit and residual: " + err.Error(),
			}
		}
	case spec.Total != nil:
		// {{T CUR}} -> T / |units|
		out.Currency = spec.Total.Currency
		totalNum := spec.Total.Number
		if _, err := quoContext.Quo(&out.Number, &totalNum, absUnits); err != nil {
			return nil, Error{
				Code:    CodeInternalError,
				Span:    spec.Span,
				Message: "divide total by units: " + err.Error(),
			}
		}
	default:
		// Per-unit only: copy verbatim.
		out.Currency = spec.PerUnit.Currency
		out.Number = *ast.CloneDecimal(&spec.PerUnit.Number)
	}

	return out, nil
}
