package inventory

import (
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// CostMatcher filters lot [Lot]s for reduction. The zero value is an
// empty matcher and matches any Lot, including a zero-value (cash)
// Lot. See [NewCostMatcher] for how the fields are derived from a
// posting's cost spec and price annotation.
type CostMatcher struct {
	// HasPerUnit, when true, constrains the lot's [Lot.Number] to
	// equal PerUnit.
	HasPerUnit bool
	PerUnit    apd.Decimal
	// Currency constrains the lot's cost currency; "" matches any.
	Currency string
	// HasDate, when true, constrains the lot's [Lot.Date].
	HasDate bool
	Date    time.Time
	// HasLabel, when true, constrains the lot's [Lot.Label].
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
//     only or when total-form derivation succeeds (delegated to
//     [ast.PerUnitCost], the same resolver [ResolveLot] uses, so a
//     matcher finds the lot ResolveLot just produced from an
//     equivalent spec). Date and Label constraints follow the spec.
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
	case spec.PerUnit != nil || spec.Total != nil:
		m.Currency = spec.Currency
		// Total-form derivation needs units; missing or zero units
		// silently drops the per-unit constraint so the caller falls
		// back to currency-only matching, matching the previous
		// derivePerUnitFromTotal contract.
		if amt, err := ast.PerUnitCost(spec, units); err == nil && amt != nil {
			m.HasPerUnit = true
			m.PerUnit = amt.Number
		}
	case spec.Currency != "":
		m.Currency = spec.Currency
	case priceCurrency != "":
		m.Currency = priceCurrency
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

// IsEmpty reports whether the matcher has no constraints at all. An empty
// matcher matches every Lot, including a zero-value (cash) Lot.
func (m CostMatcher) IsEmpty() bool {
	return !m.HasPerUnit && m.Currency == "" && !m.HasDate && !m.HasLabel
}

// Matches reports whether lot satisfies every constraint that m has. A
// matcher with no constraints (IsEmpty) matches any Lot.
func (m CostMatcher) Matches(lot Lot) bool {
	if m.HasPerUnit {
		if lot.Number.Cmp(&m.PerUnit) != 0 {
			return false
		}
	}
	if m.Currency != "" && lot.Currency != m.Currency {
		return false
	}
	if m.HasDate && !lot.Date.Equal(m.Date) {
		return false
	}
	if m.HasLabel && lot.Label != m.Label {
		return false
	}
	return true
}
