package types

import (
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// Value is a single BQL value. The interface is sealed: every variant is
// defined in this package, so a type switch over the concrete variants is
// exhaustive.
//
// A Value is immutable after construction and safe to share across
// goroutines for read.
//
// Type reports the value's kind. For a NULL it reports the kind the NULL
// stands in for. IsNull reports whether the value is NULL. Compare defines
// the total order documented in compare.go. Format renders the value for
// human-facing display (CLI, result tables); String renders it for Go
// debugging and may coincide with Format.
type Value interface {
	Type() Type
	IsNull() bool
	Compare(Value) int
	Format() string
	String() string

	sealedValue()
}

// NewBool returns a non-null Bool value.
func NewBool(b bool) Value { return boolValue(b) }

// NewInt returns a non-null Int value.
func NewInt(n int64) Value { return intValue(n) }

// NewDecimal returns a non-null Decimal value holding an exact copy of d.
// The copy is independent of the caller's argument.
func NewDecimal(d apd.Decimal) Value {
	return decimalValue{d: *ast.CloneDecimal(&d)}
}

// NewString returns a non-null String value.
func NewString(s string) Value { return stringValue(s) }

// NewDate returns a non-null Date value.
func NewDate(t time.Time) Value { return dateValue(t) }

// NewAmount returns a non-null Amount value holding an exact, independent
// copy of a (its decimal coefficient is cloned).
func NewAmount(a ast.Amount) Value {
	return amountValue{a: ast.Amount{Number: *ast.CloneDecimal(&a.Number), Currency: a.Currency}}
}

// NewPosition returns a non-null Position value holding a deep copy of p,
// so later mutation of p (or its lot) is not observable through the Value.
func NewPosition(p inventory.Position) Value {
	return positionValue{p: p.Clone()}
}

// NewInventory returns a non-null Inventory value holding a deep copy of
// inv, taken at construction time. A nil inv is treated as an empty
// inventory. Because the copy is private, the caller may continue to
// mutate inv without affecting the returned Value.
func NewInventory(inv *inventory.Inventory) Value {
	if inv == nil {
		return inventoryValue{inv: inventory.NewInventory()}
	}
	return inventoryValue{inv: inv.Clone()}
}

// NewInterval returns a non-null Interval value holding the given calendar
// offset.
func NewInterval(years, months, days int) Value {
	return intervalValue{years: years, months: months, days: days}
}

// Null returns a NULL value that remembers t as its kind. IsNull reports
// true, Type reports t, and every typed accessor reports ok=false. NULL is
// a single conceptual notion; the carried type only keeps ordering and
// typing stable for an all-NULL column.
func Null(t Type) Value { return nullValue{t: t} }

type boolValue bool

func (boolValue) Type() Type            { return Bool }
func (boolValue) IsNull() bool          { return false }
func (boolValue) sealedValue()          {}
func (v boolValue) Format() string      { return strconv.FormatBool(bool(v)) }
func (v boolValue) String() string      { return v.Format() }
func (v boolValue) Compare(o Value) int { return compare(v, o) }

type intValue int64

func (intValue) Type() Type            { return Int }
func (intValue) IsNull() bool          { return false }
func (intValue) sealedValue()          {}
func (v intValue) Format() string      { return strconv.FormatInt(int64(v), 10) }
func (v intValue) String() string      { return v.Format() }
func (v intValue) Compare(o Value) int { return compare(v, o) }

type decimalValue struct{ d apd.Decimal }

func (decimalValue) Type() Type            { return Decimal }
func (decimalValue) IsNull() bool          { return false }
func (decimalValue) sealedValue()          {}
func (v decimalValue) Format() string      { return v.d.Text('f') }
func (v decimalValue) String() string      { return v.d.String() }
func (v decimalValue) Compare(o Value) int { return compare(v, o) }

type stringValue string

func (stringValue) Type() Type            { return String }
func (stringValue) IsNull() bool          { return false }
func (stringValue) sealedValue()          {}
func (v stringValue) Format() string      { return string(v) }
func (v stringValue) String() string      { return strconv.Quote(string(v)) }
func (v stringValue) Compare(o Value) int { return compare(v, o) }

type dateValue time.Time

func (dateValue) Type() Type            { return Date }
func (dateValue) IsNull() bool          { return false }
func (dateValue) sealedValue()          {}
func (v dateValue) Format() string      { return time.Time(v).Format("2006-01-02") }
func (v dateValue) String() string      { return v.Format() }
func (v dateValue) Compare(o Value) int { return compare(v, o) }

type amountValue struct{ a ast.Amount }

func (amountValue) Type() Type            { return Amount }
func (amountValue) IsNull() bool          { return false }
func (amountValue) sealedValue()          {}
func (v amountValue) Format() string      { return v.a.Number.Text('f') + " " + v.a.Currency }
func (v amountValue) String() string      { return v.Format() }
func (v amountValue) Compare(o Value) int { return compare(v, o) }

type positionValue struct{ p inventory.Position }

func (positionValue) Type() Type            { return Position }
func (positionValue) IsNull() bool          { return false }
func (positionValue) sealedValue()          {}
func (v positionValue) Format() string      { return formatPosition(v.p) }
func (v positionValue) String() string      { return v.Format() }
func (v positionValue) Compare(o Value) int { return compare(v, o) }

type inventoryValue struct{ inv *inventory.Inventory }

func (inventoryValue) Type() Type            { return Inventory }
func (inventoryValue) IsNull() bool          { return false }
func (inventoryValue) sealedValue()          {}
func (v inventoryValue) Format() string      { return formatInventory(v.inv) }
func (v inventoryValue) String() string      { return v.Format() }
func (v inventoryValue) Compare(o Value) int { return compare(v, o) }

type intervalValue struct{ years, months, days int }

func (intervalValue) Type() Type            { return Interval }
func (intervalValue) IsNull() bool          { return false }
func (intervalValue) sealedValue()          {}
func (v intervalValue) Format() string      { return formatInterval(v.years, v.months, v.days) }
func (v intervalValue) String() string      { return v.Format() }
func (v intervalValue) Compare(o Value) int { return compare(v, o) }

type nullValue struct{ t Type }

func (v nullValue) Type() Type          { return v.t }
func (nullValue) IsNull() bool          { return true }
func (nullValue) sealedValue()          {}
func (nullValue) Format() string        { return "NULL" }
func (nullValue) String() string        { return "NULL" }
func (v nullValue) Compare(o Value) int { return compare(v, o) }

// AsBool returns the underlying bool. ok is false when v is NULL or not a
// Bool.
func AsBool(v Value) (bool, bool) {
	b, ok := v.(boolValue)
	return bool(b), ok
}

// AsInt returns the underlying int64. ok is false when v is NULL or not an
// Int.
func AsInt(v Value) (int64, bool) {
	n, ok := v.(intValue)
	return int64(n), ok
}

// AsDecimal returns an exact copy of the underlying decimal. ok is false
// when v is NULL or not a Decimal.
func AsDecimal(v Value) (apd.Decimal, bool) {
	d, ok := v.(decimalValue)
	if !ok {
		return apd.Decimal{}, false
	}
	return *ast.CloneDecimal(&d.d), true
}

// AsString returns the underlying string. ok is false when v is NULL or
// not a String.
func AsString(v Value) (string, bool) {
	s, ok := v.(stringValue)
	return string(s), ok
}

// AsDate returns the underlying time. ok is false when v is NULL or not a
// Date.
func AsDate(v Value) (time.Time, bool) {
	d, ok := v.(dateValue)
	return time.Time(d), ok
}

// AsAmount returns an exact copy of the underlying amount. ok is false
// when v is NULL or not an Amount.
func AsAmount(v Value) (ast.Amount, bool) {
	a, ok := v.(amountValue)
	if !ok {
		return ast.Amount{}, false
	}
	return ast.Amount{Number: *ast.CloneDecimal(&a.a.Number), Currency: a.a.Currency}, true
}

// AsPosition returns a deep copy of the underlying position. ok is false
// when v is NULL or not a Position.
func AsPosition(v Value) (inventory.Position, bool) {
	p, ok := v.(positionValue)
	if !ok {
		return inventory.Position{}, false
	}
	return p.p.Clone(), true
}

// AsInventory returns a deep copy of the underlying inventory, so the
// caller may mutate the result freely. ok is false when v is NULL or not
// an Inventory.
func AsInventory(v Value) (*inventory.Inventory, bool) {
	i, ok := v.(inventoryValue)
	if !ok {
		return nil, false
	}
	return i.inv.Clone(), true
}

// AsInterval returns the underlying calendar offset. ok is false when v is
// NULL or not an Interval.
func AsInterval(v Value) (years, months, days int, ok bool) {
	i, ok := v.(intervalValue)
	return i.years, i.months, i.days, ok
}

// AsSet returns the underlying Set. ok is false when v is NULL or not a
// Set.
func AsSet(v Value) (Set, bool) {
	s, ok := v.(Set)
	return s, ok
}

// AsDict returns the underlying Dict. ok is false when v is NULL or not a
// Dict.
func AsDict(v Value) (Dict, bool) {
	d, ok := v.(Dict)
	return d, ok
}

func formatInterval(years, months, days int) string {
	var parts []string
	for _, c := range []struct {
		n    int
		unit string
	}{{years, "year"}, {months, "month"}, {days, "day"}} {
		if c.n == 0 {
			continue
		}
		unit := c.unit
		if c.n != 1 && c.n != -1 {
			unit += "s"
		}
		parts = append(parts, strconv.Itoa(c.n)+" "+unit)
	}
	if len(parts) == 0 {
		return "0 days"
	}
	return strings.Join(parts, ", ")
}

func formatPosition(p inventory.Position) string {
	s := p.Units.Number.Text('f') + " " + p.Units.Currency
	if p.Cost != nil {
		s += " {" + p.Cost.Number.Text('f') + " " + p.Cost.Currency + "}"
	}
	return s
}

func formatInventory(inv *inventory.Inventory) string {
	if inv == nil || inv.Len() == 0 {
		return "()"
	}
	var b []byte
	b = append(b, '(')
	first := true
	for p := range inv.All() {
		if !first {
			b = append(b, ',', ' ')
		}
		first = false
		b = append(b, formatPosition(p)...)
	}
	b = append(b, ')')
	return string(b)
}
