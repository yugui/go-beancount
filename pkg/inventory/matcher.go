package inventory

import (
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// CostMatcher filters lot [Cost]s for reduction. It is built from the
// reducing posting's cost spec together with an optional cost-currency
// hint derived from a price annotation on the posting: when a reducing
// posting carries no explicit cost currency but does carry a price
// annotation (for example `-5 AAPL {} @ 190 USD`), the price's currency
// is taken as the cost currency to match against. The zero value
// matches any Cost including a zero-value Cost (cash / no-cost lot).
//
// Semantics of the fields:
//
//   - HasPerUnit / PerUnit: when HasPerUnit is true only lots whose
//     [Cost.Number] equals PerUnit qualify. Total-only specs
//     ({{total CUR}}) and combined-form specs ({per # total CUR})
//     derive an implicit per-unit constraint when [NewCostMatcher] is
//     given the reducing posting's units (Total/|units| or
//     per + Total/|units|, mirroring [ResolveCost]); without units
//     they fall back to a currency-only matcher.
//   - Currency: the cost currency to constrain on. The empty string means
//     "match any currency". It is populated from an explicit cost spec
//     when the spec carries a currency, or from the price-annotation
//     currency hint when the spec is nil or empty.
//   - HasDate / Date: when HasDate is true only lots with a
//     [Cost.Date.Equal] match qualify.
//   - HasLabel / Label: when HasLabel is true only lots whose Label
//     equals Label qualify.
type CostMatcher struct {
	HasPerUnit bool
	PerUnit    apd.Decimal
	Currency   string
	HasDate    bool
	Date       time.Time
	HasLabel   bool
	Label      string
}

// NewCostMatcher builds a matcher from the reducing posting's cost
// holder, an optional cost-currency hint derived from a price
// annotation, and the reducing posting's units (used to derive a
// per-unit constraint from total-form cost specs). The two
// [ast.CostHolder] variants are handled uniformly:
//
//   - c is nil && priceCurrency == "": empty matcher (matches any lot).
//   - c is nil && priceCurrency != "": Currency = priceCurrency (the
//     bare "@ price" reduction case).
//   - c is *[ast.Cost]: tight matcher constrained on Number / Currency /
//     Date / Label. The reducer is re-entering its own output and
//     must re-match the exact lot identity recorded on the first run.
//     priceCurrency is intentionally ignored in this branch — the
//     booked Cost carries the authoritative Currency.
//   - c is *[ast.CostSpec]: existing parse-tier rules apply.
//
// CostSpec dispatch:
//
//   - Per-unit-only form {X CUR} (spec.PerUnit != nil && spec.Total ==
//     nil): HasPerUnit is set and both PerUnit and Currency are
//     populated from spec.PerUnit — X is a real lot-selection
//     constraint.
//   - Total-only form {{total CUR}} (spec.PerUnit == nil &&
//     spec.Total != nil): when units is non-nil and non-zero,
//     HasPerUnit is set and PerUnit = Total/|units|, the same value
//     [ResolveCost] stores on a lot augmented with this spec. Without
//     units the matcher falls back to currency-only.
//   - Combined form {per # total CUR} (spec.PerUnit != nil &&
//     spec.Total != nil): when units is non-nil and non-zero,
//     HasPerUnit is set and PerUnit = per + Total/|units|, matching
//     [ResolveCost]'s storage. Without units the matcher falls back
//     to currency-only.
//   - spec.Date != nil && !spec.Date.IsZero(): HasDate / Date set.
//   - spec.Label != "": HasLabel / Label set.
//
// priceCurrency is only used as a fallback when the cost spec does not
// itself supply a currency (i.e. both PerUnit and Total are nil).
//
// The derived per-unit value is computed with [quoContext], the same
// 34-digit context [ResolveCost] uses, so a matcher built from a
// total-form spec finds the lot [ResolveCost] just created from an
// equivalent spec.
func NewCostMatcher(c ast.CostHolder, priceCurrency string, units *ast.Amount) CostMatcher {
	var m CostMatcher
	if c == nil {
		if priceCurrency != "" {
			m.Currency = priceCurrency
		}
		return m
	}
	if cost, ok := c.(*ast.Cost); ok {
		// Booked input: re-match the exact lot. Number / Currency /
		// Date / Label together are the lot identity (see
		// [ast.Cost.Equal]), so constraining on all four reproduces
		// the original lot selection on a re-run.
		m.HasPerUnit = true
		m.PerUnit = *ast.CloneDecimal(&cost.Number)
		m.Currency = cost.Currency
		if !cost.Date.IsZero() {
			m.HasDate = true
			m.Date = cost.Date
		}
		if cost.Label != "" {
			m.HasLabel = true
			m.Label = cost.Label
		}
		return m
	}
	spec, ok := c.(*ast.CostSpec)
	if !ok {
		// Unreachable under the sealed CostHolder union; defensive
		// fallthrough yields an empty matcher.
		return m
	}

	switch {
	case spec.PerUnit != nil && spec.Total == nil:
		// Per-unit-only form `{X CUR}`: X is a real selection constraint.
		m.HasPerUnit = true
		m.PerUnit = *ast.CloneDecimal(spec.PerUnit)
		m.Currency = spec.Currency
	case spec.Total != nil:
		// Total-only `{{T CUR}}` or combined `{per # T CUR}`. When
		// units are known, derive the per-unit constraint that
		// matches what ResolveCost would store on an augmentation
		// from the same spec; otherwise leave HasPerUnit unset and
		// rely on currency-only filtering.
		m.Currency = spec.Currency
		if derived, ok := derivePerUnitFromTotal(spec, units); ok {
			m.HasPerUnit = true
			m.PerUnit = derived
		}
	default:
		// No per-unit and no total. Use the spec's currency if it
		// carries one (the currency-only `{ CUR }` form), otherwise
		// fall back to the price-currency hint.
		switch {
		case spec.Currency != "":
			m.Currency = spec.Currency
		case priceCurrency != "":
			m.Currency = priceCurrency
		}
	}

	if spec.Date != nil && !spec.Date.IsZero() {
		m.HasDate = true
		m.Date = *spec.Date
	}
	if spec.Label != "" {
		m.HasLabel = true
		m.Label = spec.Label
	}
	return m
}

// derivePerUnitFromTotal returns the per-unit cost implied by a
// total-form cost spec given the reducing posting's units, mirroring
// [ResolveCost]: T/|units| for total-only and per + T/|units| for the
// combined form. Returns ok=false when units is nil or its Number is
// zero, so the caller falls back to currency-only matching.
func derivePerUnitFromTotal(spec *ast.CostSpec, units *ast.Amount) (apd.Decimal, bool) {
	if units == nil || units.Number.Sign() == 0 {
		return apd.Decimal{}, false
	}
	var absUnits apd.Decimal
	if _, err := apd.BaseContext.Abs(&absUnits, &units.Number); err != nil {
		return apd.Decimal{}, false
	}
	var quo apd.Decimal
	if _, err := quoContext.Quo(&quo, spec.Total, &absUnits); err != nil {
		return apd.Decimal{}, false
	}
	if spec.PerUnit == nil {
		return quo, true
	}
	var sum apd.Decimal
	if _, err := apd.BaseContext.Add(&sum, spec.PerUnit, &quo); err != nil {
		return apd.Decimal{}, false
	}
	return sum, true
}

// IsEmpty reports whether the matcher has no constraints at all. An empty
// matcher matches every Cost, including a zero-value (cash) Cost.
func (m CostMatcher) IsEmpty() bool {
	return !m.HasPerUnit && m.Currency == "" && !m.HasDate && !m.HasLabel
}

// Matches reports whether the lot Cost c satisfies every constraint that
// m has. A matcher with no constraints (IsEmpty) matches any Cost.
func (m CostMatcher) Matches(c Cost) bool {
	if m.HasPerUnit {
		if c.Number.Cmp(&m.PerUnit) != 0 {
			return false
		}
	}
	if m.Currency != "" && c.Currency != m.Currency {
		return false
	}
	if m.HasDate && !c.Date.Equal(m.Date) {
		return false
	}
	if m.HasLabel && c.Label != m.Label {
		return false
	}
	return true
}
