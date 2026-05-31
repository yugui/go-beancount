package types

import "strconv"

// Type identifies a BQL value kind. The zero value is [Invalid] and never
// names a constructible value.
//
// The integer values of the Type constants are a stable, documented part
// of the comparison contract: when [Value.Compare] is given two non-null
// operands of different kinds, it breaks the tie by ascending Type ordinal
// (see compare.go). Reorder the constants only with that contract in mind.
type Type int

// Type ordinals. Their relative order is the cross-type tiebreak used by
// [Value.Compare]; their absolute values are not otherwise meaningful.
//
// SetType and DictType carry the "Type" suffix because the bare names
// [Set] and [Dict] denote the corresponding container value types in this
// package; the suffix keeps the kind constant distinct from the type.
const (
	Invalid Type = iota
	Bool
	Int
	Decimal
	String
	Date
	Amount
	Position
	Inventory
	// Interval is a calendar offset of (years, months, days).
	Interval
	SetType
	DictType
	// Entry is reserved for a future directive-as-value variant. It holds
	// a stable ordinal and a String form but is not constructed in the
	// lean engine.
	Entry

	// Any is a parameter-only marker: it never tags a constructed value,
	// only a function overload's input slot that accepts an argument of
	// any kind (including an untyped NULL literal). The overload resolver
	// treats an Any slot as a last-resort match, losing to any exact or
	// widening candidate. It mirrors upstream beanquery's types.Any.
	Any
)

// String returns the lowercase kind name for diagnostics. It returns
// "invalid" for the zero Type and "type(N)" for an unrecognized value.
func (t Type) String() string {
	switch t {
	case Invalid:
		return "invalid"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Decimal:
		return "decimal"
	case String:
		return "string"
	case Date:
		return "date"
	case Amount:
		return "amount"
	case Position:
		return "position"
	case Inventory:
		return "inventory"
	case Interval:
		return "interval"
	case SetType:
		return "set"
	case DictType:
		return "dict"
	case Entry:
		return "entry"
	case Any:
		return "any"
	default:
		return "type(" + strconv.Itoa(int(t)) + ")"
	}
}
