package types

import (
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// compare implements the total order documented on [Value.Compare].
//
// The order is ASCENDING. Its contract, in priority order:
//
//  1. NULL sorts LAST. A non-null value is less than any NULL; two NULLs
//     are equal (the carried [Type] does not break the tie).
//  2. Two non-null values of DIFFERENT [Type] are ordered by ascending
//     Type ordinal (the documented enum order in type.go). Real columns
//     are single-typed, so this only fixes the order for the mixed inputs
//     ORDER BY/min/max must still totally order.
//  3. Two non-null values of the SAME Type use that type's natural order:
//     Bool false<true; Int and Decimal numeric (Decimal via apd, exact);
//     String lexicographic by bytes; Date chronological; Amount by
//     (Currency, then Number); Position by (Commodity, then lot identity,
//     then Units); Inventory by length then position-wise; Interval
//     structurally by (years, months, days) — see the note below; Set and
//     Dict per set.go and dict.go.
//
// Entry has no magnitude order, but is totally ordered by directive identity
// so DISTINCT, GROUP BY, and ORDER BY behave deterministically: two Entry
// values are ranked by source span (filename, line, column), then by canonical
// [EntryID]. Two Entry values are equal iff they denote the same directive;
// distinct directives sort in a stable but not semantically meaningful order.
//
// Interval's order is STRUCTURAL, not a duration order. It distinguishes
// distinct (years, months, days) tuples — so equality, DISTINCT, and GROUP BY
// behave correctly — and is stable, but it does NOT rank calendar durations:
// interval "700 days" sorts below interval "1 year" though it is longer.
// Because no meaningful total duration order exists, the ordering operators
// (< <= > >=), ORDER BY, and the min/max/first/last aggregates reject Interval
// at compile time (see exec/compile.go and env/std/aggregate.go). Only = and
// != reach this function for Interval operands, and only the zero/non-zero
// result is observed.
func compare(a, b Value) int {
	an, bn := a.IsNull(), b.IsNull()
	if an || bn {
		switch {
		case an && bn:
			return 0
		case an:
			return 1
		default:
			return -1
		}
	}
	if at, bt := a.Type(), b.Type(); at != bt {
		return cmpInt(int(at), int(bt))
	}
	switch x := a.(type) {
	case boolValue:
		return cmpBool(bool(x), bool(b.(boolValue)))
	case intValue:
		return cmpInt64(int64(x), int64(b.(intValue)))
	case decimalValue:
		y := b.(decimalValue)
		return x.d.Cmp(&y.d)
	case stringValue:
		return strings.Compare(string(x), string(b.(stringValue)))
	case dateValue:
		return time.Time(x).Compare(time.Time(b.(dateValue)))
	case amountValue:
		return cmpAmount(x.a, b.(amountValue).a)
	case positionValue:
		return cmpPosition(x.p, b.(positionValue).p)
	case inventoryValue:
		return cmpInventory(x.inv, b.(inventoryValue).inv)
	case intervalValue:
		y := b.(intervalValue)
		if c := cmpInt(x.years, y.years); c != 0 {
			return c
		}
		if c := cmpInt(x.months, y.months); c != 0 {
			return c
		}
		return cmpInt(x.days, y.days)
	case Set:
		return x.compareTo(b.(Set))
	case Dict:
		return x.compareTo(b.(Dict))
	case entryValue:
		return cmpEntry(x, b.(entryValue))
	default:
		return 0
	}
}

// cmpEntry orders two entries by source span, then by canonical id, so equal
// directives compare 0 and distinct ones order deterministically.
func cmpEntry(a, b entryValue) int {
	if c := cmpSpan(a.d.DirSpan(), b.d.DirSpan()); c != 0 {
		return c
	}
	return strings.Compare(a.id, b.id)
}

func cmpSpan(a, b ast.Span) int {
	if c := strings.Compare(a.Start.Filename, b.Start.Filename); c != 0 {
		return c
	}
	if c := cmpInt(a.Start.Line, b.Start.Line); c != 0 {
		return c
	}
	return cmpInt(a.Start.Column, b.Start.Column)
}

func cmpAmount(a, b ast.Amount) int {
	if c := strings.Compare(a.Currency, b.Currency); c != 0 {
		return c
	}
	return a.Number.Cmp(&b.Number)
}

func cmpPosition(a, b inventory.Position) int {
	if c := strings.Compare(a.Commodity(), b.Commodity()); c != 0 {
		return c
	}
	if c := cmpLot(a.Cost, b.Cost); c != 0 {
		return c
	}
	return a.Units.Number.Cmp(&b.Units.Number)
}

// cmpLot orders lots so a cash position (nil lot) sorts before any
// cost-held lot, then by (Currency, Number, Date, Label).
func cmpLot(a, b *inventory.Lot) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	if c := strings.Compare(a.Currency, b.Currency); c != 0 {
		return c
	}
	if c := a.Number.Cmp(&b.Number); c != 0 {
		return c
	}
	if c := a.Date.Compare(b.Date); c != 0 {
		return c
	}
	return strings.Compare(a.Label, b.Label)
}

// cmpInventory orders inventories by length, then position-wise in
// insertion order via cmpPosition. Equal-length inventories with equal
// positions in order compare 0, consistent with inventory.Inventory.Equal.
func cmpInventory(a, b *inventory.Inventory) int {
	la, lb := lenInventory(a), lenInventory(b)
	if la != lb {
		return cmpInt(la, lb)
	}
	pa, pb := positionsOf(a), positionsOf(b)
	for i := range pa {
		if c := cmpPosition(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	return 0
}

func lenInventory(inv *inventory.Inventory) int {
	if inv == nil {
		return 0
	}
	return inv.Len()
}

func positionsOf(inv *inventory.Inventory) []inventory.Position {
	if inv == nil {
		return nil
	}
	out := make([]inventory.Position, 0, inv.Len())
	for p := range inv.All() {
		out = append(out, p)
	}
	return out
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	default:
		return 1
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
