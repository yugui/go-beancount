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

// Cost is a resolved lot cost: a per-unit number, a currency, an
// acquisition date, and an optional lot label. It is the resolved
// counterpart to the raw [ast.CostSpec] — where a CostSpec captures what
// the user wrote (any of per-unit/total/date/label may be nil), a Cost
// always has a concrete per-unit number and currency and is safe to
// compare for lot equality.
//
// Cost is a value type; the Number field is stored inline (not a
// pointer) so a zero Cost has Number == 0 rather than a nil decimal.
type Cost struct {
	Number   apd.Decimal
	Currency string
	Date     time.Time
	Label    string
}

// Lot is an alias for [Cost], preserved for documentation clarity at
// call sites where "lot" reads more naturally than "cost".
type Lot = Cost

// Equal reports whether two costs describe the same lot: same per-unit
// number (by value), currency, acquisition date, and label.
func (c Cost) Equal(o Cost) bool {
	if c.Currency != o.Currency || c.Label != o.Label {
		return false
	}
	if !c.Date.Equal(o.Date) {
		return false
	}
	// apd.Decimal.Cmp returns 0 when the two decimals have the same value.
	return c.Number.Cmp(&o.Number) == 0
}

// Clone returns a deep copy of c. It is nil-safe: calling Clone on a
// nil receiver returns nil, which matches the convention used by
// Position for the optional Cost field.
func (c *Cost) Clone() *Cost {
	if c == nil {
		return nil
	}
	return &Cost{
		Number:   *ast.CloneDecimal(&c.Number),
		Currency: c.Currency,
		Date:     c.Date,
		Label:    c.Label,
	}
}

// ResolveCost turns an [ast.CostSpec] on an augmenting posting into a
// concrete [Cost]. It implements the following cost-resolution rules:
//
//   - spec == nil       -> (nil, nil). The posting has no cost lot.
//   - spec with no PerUnit and no Total (empty "{}" on an augmentation)
//     returns an [Error] with [CodeAugmentationRequiresCost]. Reductions
//     never call ResolveCost; they build a [CostMatcher] instead.
//   - per-unit only ({X CUR})                     -> Number = X
//   - total only      ({{T CUR}})                 -> Number = T / |units|
//   - combined form   ({X # T CUR}, {X # T CUR})  -> Number = X + T/|units|
//
// The computed Number is always positive (cost is a magnitude). The
// Date defaults to txnDate when spec.Date is unset; Label is copied
// verbatim. When both PerUnit and Total are present ResolveCost
// defensively verifies that their currencies agree; a mismatch is
// reported as [CodeInternalError] because earlier parse/lower stages
// should have caught it.
func ResolveCost(spec *ast.CostSpec, units ast.Amount, txnDate time.Time) (*Cost, error) {
	if spec == nil {
		return nil, nil
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
