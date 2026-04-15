package inventory

import (
	"time"

	apd "github.com/cockroachdb/apd/v3"

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
//     [Cost.Number] equals PerUnit qualify. Combined-form cost specs
//     ({per # total CUR}) and total-only specs ({{total CUR}}) do NOT
//     set HasPerUnit — their Total is informational for realized-gain
//     calculation, not a lot-selection constraint.
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

// NewCostMatcher builds a matcher from the reducing posting's cost spec
// and an optional cost-currency hint derived from a price annotation on
// the posting. The hint covers the case where a reducing posting has no
// explicit cost currency but does carry a price annotation (for example
// `-5 AAPL {} @ 190 USD`): the matcher then takes the cost currency
// from the price annotation.
//
// Behaviour summary:
//
//   - spec == nil && priceCurrency == "": empty matcher (matches any lot).
//   - spec == nil && priceCurrency != "": a matcher whose only
//     constraint is Currency = priceCurrency (the bare "@ price"
//     reduction case).
//   - Per-unit-only form {X CUR} (spec.PerUnit != nil && spec.Total ==
//     nil): HasPerUnit is set and both PerUnit and Currency are
//     populated from spec.PerUnit — X is a real lot-selection
//     constraint.
//   - Combined form {per # total CUR} (spec.PerUnit != nil &&
//     spec.Total != nil) and total-only form {{total CUR}}
//     (spec.Total != nil): Currency is populated from spec.Total, but
//     HasPerUnit is NOT set. [ResolveCost] stores the lot's Number as
//     per + total/|units| (not per alone) for the combined form, so a
//     matcher built from the same spec cannot constrain Number and
//     still find the lot it just created. The Total is informational
//     for realized-gain calculation, handled by the booking layer.
//   - spec.Date != nil && !spec.Date.IsZero(): HasDate / Date set.
//   - spec.Label != "": HasLabel / Label set.
//
// priceCurrency is only used as a fallback when the cost spec does not
// itself supply a currency (i.e. both PerUnit and Total are nil).
func NewCostMatcher(spec *ast.CostSpec, priceCurrency string) CostMatcher {
	var m CostMatcher
	if spec == nil {
		if priceCurrency != "" {
			m.Currency = priceCurrency
		}
		return m
	}

	switch {
	case spec.PerUnit != nil && spec.Total == nil:
		// Per-unit-only form `{X CUR}`: X is a real selection constraint.
		m.HasPerUnit = true
		// Copy the decimal value rather than aliasing its internal
		// coefficient buffer.
		m.PerUnit.Set(&spec.PerUnit.Number)
		m.Currency = spec.PerUnit.Currency
	case spec.Total != nil:
		// Combined form `{per # total CUR}` or total-only `{{total CUR}}`:
		// the Total is informational for realized-gain calculation, not for
		// lot selection, so do not set HasPerUnit. Currency comes from Total
		// (which carries the authoritative currency in both forms).
		m.Currency = spec.Total.Currency
	default:
		// Empty "{}" spec: no per-unit and no total. Fall back to the
		// price-currency hint if one is available.
		if priceCurrency != "" {
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
