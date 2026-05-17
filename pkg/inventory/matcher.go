package inventory

import (
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// CostMatcher filters lot [Cost]s for reduction. The zero value is an
// empty matcher and matches any Cost, including a zero-value (cash)
// Cost. See [NewCostMatcher] for how the fields are derived from a
// posting's cost spec and price annotation.
type CostMatcher struct {
	// HasPerUnit, when true, constrains the lot's [Cost.Number] to
	// equal PerUnit.
	HasPerUnit bool
	PerUnit    apd.Decimal
	// Currency constrains the lot's cost currency; "" matches any.
	Currency string
	// HasDate, when true, constrains the lot's [Cost.Date].
	HasDate bool
	Date    time.Time
	// HasLabel, when true, constrains the lot's [Cost.Label].
	HasLabel bool
	Label    string
}

// NewCostMatcher builds a matcher from a reducing posting's cost
// holder c, an optional priceCurrency hint, and the posting's units
// (used to derive a per-unit constraint from total-form specs).
//
//   - *[ast.Cost]: tight matcher on (Number, Currency, Date, Label),
//     reproducing the exact lot identity recorded on the first run;
//     priceCurrency is ignored.
//   - *[ast.CostSpec]: per-unit constraint when the spec is per-unit-
//     only or when total-form derivation succeeds (mirroring
//     [ResolveCost] via [quoContext], so a matcher finds the lot
//     ResolveCost just produced from an equivalent spec). Date and
//     Label constraints follow the spec.
//   - nil c: empty matcher, or Currency = priceCurrency when given
//     (the bare `@ price` reduction case).
//
// priceCurrency is only consulted as a fallback when the spec carries
// neither a number nor a currency of its own.
func NewCostMatcher(c ast.CostHolder, priceCurrency string, units *ast.Amount) CostMatcher {
	var m CostMatcher
	if c == nil {
		if priceCurrency != "" {
			m.Currency = priceCurrency
		}
		return m
	}
	if cost, ok := c.(*ast.Cost); ok {
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
	spec := c.(*ast.CostSpec)

	switch {
	case spec.PerUnit != nil && spec.Total == nil:
		m.HasPerUnit = true
		m.PerUnit = *ast.CloneDecimal(spec.PerUnit)
		m.Currency = spec.Currency
	case spec.Total != nil:
		m.Currency = spec.Currency
		if derived, ok := derivePerUnitFromTotal(spec, units); ok {
			m.HasPerUnit = true
			m.PerUnit = derived
		}
	default:
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
// total-form spec, mirroring [ResolveCost]. Returns ok=false when
// units is nil or zero (the caller then falls back to currency-only
// matching).
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
