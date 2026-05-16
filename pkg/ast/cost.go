package ast

import (
	"fmt"
	"time"

	"github.com/cockroachdb/apd/v3"
)

// CostHolder is the sealed union of cost representations carried on a
// [Posting]: [*CostSpec] for the parse-tier form (any of PerUnit /
// Total / Date may be nil) and [*Cost] for the booked, fully-resolved
// form. The interface lets call sites that only need read access stay
// booking-status agnostic; sites that need write access keep their
// concrete [*CostSpec] type via a type assertion.
//
// The union is sealed via the unexported isCostHolder marker method:
// external packages cannot extend it. Only [*CostSpec] and [*Cost],
// both defined in this package, satisfy CostHolder.
//
// The Get* prefix is used on the read methods because [CostSpec] (and
// [Cost]) already expose fields named PerUnit / Total / Date / Label;
// a method that shadows a field of the same name is a compile error,
// so the prefix is necessary to disambiguate. Direct field access on
// the concrete struct remains available and is the preferred form when
// the concrete type is already known.
type CostHolder interface {
	// isCostHolder is the sealed-union marker. Only [*CostSpec] and
	// [*Cost], both defined in this package, satisfy it.
	isCostHolder()

	// GetPerUnit returns the user-written per-unit literal as an
	// [Amount], or nil if none was specified.
	GetPerUnit() *Amount
	// GetTotal returns the user-written surcharge total as an
	// [Amount], or nil if none was specified.
	GetTotal() *Amount
	// GetCurrency returns the cost currency, or "" if unspecified.
	// Callers may rely on this being a constant-time, allocation-free
	// read; safe to call in hot paths.
	GetCurrency() string
	// GetDate returns the acquisition date and a boolean indicating
	// whether the date is set. The comma-ok return unifies the two
	// "unset" sentinels — a nil Date pointer on [*CostSpec] and a
	// zero-value Date on a freshly constructed [*Cost] — so callers
	// do not need to distinguish them.
	GetDate() (time.Time, bool)
	// GetLabel returns the lot label, "" if not specified.
	GetLabel() string
	// IsBooked reports whether this is the booked, fully-resolved
	// form ([*Cost]) versus the parse-tier form ([*CostSpec]).
	IsBooked() bool
}

// Compile-time interface satisfaction.
var (
	_ CostHolder = (*CostSpec)(nil)
	_ CostHolder = (*Cost)(nil)
)

// CostSpec represents a cost specification on a posting.
//
// PerUnit and Total carry the per-unit and total / surcharge cost
// numbers; Currency is their shared currency. There is no disambiguation
// flag; the mapping from source syntax is:
//
//	{X CUR}            -> PerUnit=X,    Total=nil,  Currency=CUR
//	{{X CUR}}          -> PerUnit=nil,  Total=X,    Currency=CUR
//	{X # Y CUR}        -> PerUnit=X,    Total=Y,    Currency=CUR
//	{ CUR }            -> PerUnit=nil,  Total=nil,  Currency=CUR
//	{} or {{}}         -> PerUnit=nil,  Total=nil,  Currency=""
//
// Currency carries the cost currency for every shape that has one;
// reading it is the single source of truth and avoids re-deriving it
// from PerUnit / Total. The empty form is normalized to "{}" on print;
// "{{}}" does not round-trip byte-for-byte.
type CostSpec struct {
	Span     Span
	PerUnit  *apd.Decimal // per-unit cost number; nil if absent
	Total    *apd.Decimal // total / surcharge cost number; nil if absent
	Currency string       // shared cost currency; empty if unspecified
	Date     *time.Time   // optional acquisition date
	Label    string       // optional lot label; empty if not specified
}

func (*CostSpec) isCostHolder() {}

// GetPerUnit returns the per-unit number as an [Amount], or nil if
// either the number or currency is unset.
func (c *CostSpec) GetPerUnit() *Amount {
	if c.PerUnit == nil || c.Currency == "" {
		return nil
	}
	return &Amount{Number: *c.PerUnit, Currency: c.Currency}
}

// GetTotal returns the total number as an [Amount], or nil if either
// the number or currency is unset.
func (c *CostSpec) GetTotal() *Amount {
	if c.Total == nil || c.Currency == "" {
		return nil
	}
	return &Amount{Number: *c.Total, Currency: c.Currency}
}

// GetCurrency returns the cost currency, or "" if unspecified.
func (c *CostSpec) GetCurrency() string { return c.Currency }

// GetDate returns the user-written acquisition date and whether it
// was set; the boolean is false iff the Date field is nil.
func (c *CostSpec) GetDate() (time.Time, bool) {
	if c.Date == nil {
		return time.Time{}, false
	}
	return *c.Date, true
}

// GetLabel returns the lot label.
func (c *CostSpec) GetLabel() string { return c.Label }

// IsBooked reports false: a CostSpec is the parse-tier form.
func (*CostSpec) IsBooked() bool { return false }

// Cost is a resolved lot cost: a per-unit number, a currency, an
// acquisition date, and an optional lot label. It is the booked
// counterpart to [CostSpec] — where a CostSpec captures what the user
// wrote (any of PerUnit/Total/Date may be nil), a Cost always has a
// concrete per-unit number, currency, and date, and is safe to compare
// for lot equality.
//
// Cost additionally retains the user's original PerUnit/Total form as
// optional fields so the printer can round-trip surcharge syntax
// (`{X CUR, # CUR}`) after booking. Number is the canonical resolved
// per-unit value used for inventory matching and equality; PerUnit and
// Total carry presentation provenance only and are not consulted by
// [Cost.Equal].
//
// Cost is a value type; the Number field is stored inline (not a
// pointer) so a zero Cost has Number == 0 rather than a nil decimal.
type Cost struct {
	Number   apd.Decimal
	Currency string
	Date     time.Time
	Label    string

	// PerUnit, when non-nil, records the user's per-unit literal.
	// Set by the reducer when converting a CostSpec whose PerUnit was
	// written ({X CUR} or {X CUR, # CUR} forms), and by lot-driven
	// reductions to the matched lot's canonical per-unit so the
	// printer renders the resolved form.
	PerUnit *Amount
	// Total, when non-nil, records the user's surcharge literal.
	// Set by the reducer for the {{Y CUR}} and {X CUR, # CUR} forms;
	// nil otherwise.
	Total *Amount
}

// Lot is an alias for [Cost], preserved for documentation clarity at
// call sites where "lot" reads more naturally than "cost".
type Lot = Cost

// Equal reports whether two costs describe the same lot: same per-unit
// Number (by value), Currency, Date, and Label. PerUnit and Total are
// presentation provenance and are intentionally excluded from the
// comparison so two postings booked through different syntactic forms
// still match the same lot.
//
// Equal is nil-safe at both arguments: two nil costs compare equal,
// and a nil paired with a non-nil compares unequal.
func (c *Cost) Equal(o *Cost) bool {
	if c == nil || o == nil {
		return c == o
	}
	if c.Currency != o.Currency || c.Label != o.Label {
		return false
	}
	if !c.Date.Equal(o.Date) {
		return false
	}
	// apd.Decimal.Cmp returns 0 when the two decimals have the same value.
	return c.Number.Cmp(&o.Number) == 0
}

func (*Cost) isCostHolder() {}

// GetPerUnit returns the retained per-unit literal, or nil if the user
// wrote a Total-only form and the reducer has not populated PerUnit
// from a matched lot.
func (c *Cost) GetPerUnit() *Amount { return c.PerUnit }

// GetTotal returns the retained surcharge total, or nil.
func (c *Cost) GetTotal() *Amount { return c.Total }

// GetCurrency returns the cost currency, always set after booking.
func (c *Cost) GetCurrency() string { return c.Currency }

// GetDate returns the acquisition date and whether it is set. The
// booked Cost normally has Date filled by the reducer, but a
// freshly zero-valued [Cost] (e.g. the cash-position sentinel used by
// [pkg/inventory.ReductionStep]) reports (time.Time{}, false) so
// callers do not see a non-nil "date" pointing at 0001-01-01.
func (c *Cost) GetDate() (time.Time, bool) {
	if c.Date.IsZero() {
		return time.Time{}, false
	}
	return c.Date, true
}

// GetLabel returns the lot label.
func (c *Cost) GetLabel() string { return c.Label }

// IsBooked reports true: a Cost is the booked form.
func (*Cost) IsBooked() bool { return true }

// signedAbs returns sign(units) * |val| as a freshly allocated
// decimal. Used by both the Total-only and combined branches of
// (*Posting).TotalCost so the same exact (division-free) formulation
// reaches both code paths. The same name and shape live in
// pkg/inventory for the price-side equivalent.
func signedAbs(units, val *apd.Decimal) (*apd.Decimal, error) {
	abs := new(apd.Decimal)
	if _, err := apd.BaseContext.Abs(abs, val); err != nil {
		return nil, err
	}
	if !units.Negative {
		return abs, nil
	}
	out := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(out, abs); err != nil {
		return nil, err
	}
	return out, nil
}

// TotalCost computes the cost-currency contribution of this posting,
// resolving the CostSpec's per-unit and total numbers uniformly so
// callers do not need to branch on which field is populated.
//
// The result is signed: the sign of p.Amount.Number propagates so the
// returned weight cancels against the per-currency totals of a balanced
// transaction. The mapping is:
//
//   - p.Amount == nil (auto-posting): (nil, nil).
//   - p.Cost == nil, or both [CostHolder.GetPerUnit] and
//     [CostHolder.GetTotal] return nil: (nil, nil). Callers treat this
//     as "no cost contribution" and fall back to units in the posting's
//     commodity currency.
//   - PerUnit only: units * PerUnit, in the cost currency.
//   - Total only: sign(units) * |Total|, in the cost currency. Mirrors
//     the "{{T CUR}}" balance rule; the formulation is exact (no
//     division) so values like {{1 JPY}} on 3 STOCK round-trip without
//     precision loss.
//   - Both set ({X # T CUR}): units*PerUnit + sign(units)*|Total|, in
//     the shared cost currency.
//
// The returned Amount is freshly allocated; the caller may mutate its
// fields without affecting the receiver.
func (p *Posting) TotalCost() (*Amount, error) {
	if p == nil || p.Amount == nil || p.Cost == nil {
		return nil, nil
	}
	perUnit := p.Cost.GetPerUnit()
	total := p.Cost.GetTotal()
	units := &p.Amount.Number
	switch {
	case perUnit != nil && total != nil:
		if perUnit.Currency != total.Currency {
			return nil, fmt.Errorf(
				"combined cost currencies differ: %q vs %q",
				perUnit.Currency, total.Currency)
		}
		perPart := new(apd.Decimal)
		if _, err := apd.BaseContext.Mul(perPart, units, &perUnit.Number); err != nil {
			return nil, err
		}
		totalPart, err := signedAbs(units, &total.Number)
		if err != nil {
			return nil, err
		}
		out := Amount{Currency: perUnit.Currency}
		if _, err := apd.BaseContext.Add(&out.Number, perPart, totalPart); err != nil {
			return nil, err
		}
		return &out, nil
	case perUnit != nil:
		out := Amount{Currency: perUnit.Currency}
		if _, err := apd.BaseContext.Mul(&out.Number, units, &perUnit.Number); err != nil {
			return nil, err
		}
		return &out, nil
	case total != nil:
		signed, err := signedAbs(units, &total.Number)
		if err != nil {
			return nil, err
		}
		return &Amount{Number: *CloneDecimal(signed), Currency: total.Currency}, nil
	default:
		return nil, nil
	}
}
