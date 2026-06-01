package types_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func sgn(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// mixed returns one representative non-null value of each kind plus a few
// typed NULLs, ordered as Compare should sort them ascending.
func mixed(t *testing.T) []types.Value {
	t.Helper()
	inv := inventory.NewInventory()
	if err := inv.Add(inventory.Position{Units: amount(t, "1", "USD")}); err != nil {
		t.Fatalf("inv.Add: %v", err)
	}
	return []types.Value{
		types.NewBool(false),
		types.NewBool(true),
		types.NewInt(-1),
		types.NewInt(100),
		types.NewDecimal(dec(t, "3.14")),
		types.NewString("alpha"),
		types.NewDate(time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)),
		types.NewAmount(amount(t, "1", "USD")),
		types.NewPosition(inventory.Position{Units: amount(t, "2", "EUR")}),
		types.NewInventory(inv),
		types.NewInterval(1, 0, 0),
		types.NewSet("x"),
		types.NewDict(map[string]types.Value{"k": types.NewInt(1)}),
		types.NewEntry(sampleTxn(t)),
		types.Null(types.Int),
		types.Null(types.String),
	}
}

func TestCompareReflexive(t *testing.T) {
	for _, v := range mixed(t) {
		if got := v.Compare(v); got != 0 {
			t.Errorf("%s.Compare(self) = %d, want 0", v.Format(), got)
		}
	}
}

func TestCompareAntisymmetric(t *testing.T) {
	vs := mixed(t)
	for _, a := range vs {
		for _, b := range vs {
			if got, want := sgn(a.Compare(b)), -sgn(b.Compare(a)); got != want {
				t.Errorf("sgn(%s.Compare(%s))=%d, -sgn(reverse)=%d", a.Format(), b.Format(), got, want)
			}
		}
	}
}

func TestCompareTransitivitySorted(t *testing.T) {
	vs := mixed(t)
	for i := 0; i < len(vs); i++ {
		for j := i; j < len(vs); j++ {
			for k := j; k < len(vs); k++ {
				ij := sgn(vs[i].Compare(vs[j]))
				jk := sgn(vs[j].Compare(vs[k]))
				ik := sgn(vs[i].Compare(vs[k]))
				if ij <= 0 && jk <= 0 && ik > 0 {
					t.Errorf("transitivity violated at (%d,%d,%d): %d %d %d", i, j, k, ij, jk, ik)
				}
			}
		}
	}
}

func TestNullSortsLast(t *testing.T) {
	nonNull := types.NewInt(0)
	null := types.Null(types.Int)
	if nonNull.Compare(null) != -1 {
		t.Errorf("non-null vs null = %d, want -1", nonNull.Compare(null))
	}
	if null.Compare(nonNull) != 1 {
		t.Errorf("null vs non-null = %d, want 1", null.Compare(nonNull))
	}
	if null.Compare(types.Null(types.String)) != 0 {
		t.Error("null vs null (different carried type) != 0")
	}
	// non-null of a higher-ordinal type still sorts before any NULL.
	if types.NewSet("z").Compare(types.Null(types.Bool)) != -1 {
		t.Error("non-null set vs null(bool) != -1")
	}
}

func TestCompareDecimalNumeric(t *testing.T) {
	two := types.NewDecimal(dec(t, "2"))
	ten := types.NewDecimal(dec(t, "10"))
	if two.Compare(ten) != -1 {
		t.Errorf("2 vs 10 = %d, want -1 (numeric, not lexicographic)", two.Compare(ten))
	}
	if types.NewDecimal(dec(t, "1.0")).Compare(types.NewDecimal(dec(t, "1.00"))) != 0 {
		t.Error("1.0 vs 1.00 != 0 (trailing zeros must not matter)")
	}
	if types.NewDecimal(dec(t, "-5")).Compare(types.NewDecimal(dec(t, "5"))) != -1 {
		t.Error("-5 vs 5 != -1")
	}
}

func TestCompareIntNumeric(t *testing.T) {
	if types.NewInt(2).Compare(types.NewInt(10)) != -1 {
		t.Error("int 2 vs 10 != -1")
	}
}

func TestCompareBool(t *testing.T) {
	if types.NewBool(false).Compare(types.NewBool(true)) != -1 {
		t.Error("false < true expected")
	}
}

func TestCompareCrossTypeTiebreak(t *testing.T) {
	// Int ordinal < Decimal ordinal < String ordinal, regardless of contents.
	if types.NewInt(999).Compare(types.NewDecimal(dec(t, "0"))) != -1 {
		t.Error("Int must sort before Decimal by ordinal")
	}
	if types.NewDecimal(dec(t, "999")).Compare(types.NewString("")) != -1 {
		t.Error("Decimal must sort before String by ordinal")
	}
	// determinism: same pair always same answer.
	a := types.NewBool(true)
	b := types.NewInt(0)
	if a.Compare(b) != b.Compare(a)*-1 {
		t.Error("cross-type compare not antisymmetric")
	}
}

func TestCompareAmount(t *testing.T) {
	usd5 := types.NewAmount(amount(t, "5", "USD"))
	usd10 := types.NewAmount(amount(t, "10", "USD"))
	jpy1 := types.NewAmount(amount(t, "1", "JPY"))
	if usd5.Compare(usd10) != -1 {
		t.Error("same currency: 5 < 10 expected")
	}
	if jpy1.Compare(usd5) != -1 {
		t.Error("JPY must sort before USD by currency")
	}
}

func TestComparePositionLotOrder(t *testing.T) {
	cash := types.NewPosition(inventory.Position{Units: amount(t, "1", "AAA")})
	lot := types.NewPosition(inventory.Position{
		Units: amount(t, "1", "AAA"),
		Cost:  &inventory.Lot{Number: dec(t, "100"), Currency: "USD", Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	})
	if cash.Compare(lot) != -1 {
		t.Error("cash position must sort before a cost-held lot")
	}
}

func TestCompareInventoryByLength(t *testing.T) {
	empty := types.NewInventory(inventory.NewInventory())
	one := inventory.NewInventory()
	if err := one.Add(inventory.Position{Units: amount(t, "1", "USD")}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if empty.Compare(types.NewInventory(one)) != -1 {
		t.Error("shorter inventory must sort first")
	}
}

// TestCompareIntervalStructural locks that Interval ordering is STRUCTURAL —
// lexicographic on the (years, months, days) tuple — and explicitly NOT a
// duration order. The ascending chain exercises negative, mixed-sign, and zero
// components.
func TestCompareIntervalStructural(t *testing.T) {
	// Strictly ascending under lexicographic (years, months, days).
	ascending := []types.Value{
		types.NewInterval(-1, 0, 0), // negative year
		types.NewInterval(0, -2, 0), // negative month
		types.NewInterval(0, -1, 0),
		types.NewInterval(0, 0, -1), // negative day
		types.NewInterval(0, 0, 0),  // zero
		types.NewInterval(0, 0, 1),
		types.NewInterval(0, 1, -5), // mixed sign: month dominates the negative day
		types.NewInterval(0, 1, 0),
		types.NewInterval(1, -3, 0), // mixed sign: year dominates the negative month
		types.NewInterval(1, 0, 0),
		types.NewInterval(1, 2, 3),
		types.NewInterval(1, 2, 4),
	}
	for i := 0; i+1 < len(ascending); i++ {
		if ascending[i].Compare(ascending[i+1]) != -1 {
			t.Errorf("%s must sort before %s", ascending[i].Format(), ascending[i+1].Format())
		}
		if ascending[i+1].Compare(ascending[i]) != 1 {
			t.Errorf("%s must sort after %s", ascending[i+1].Format(), ascending[i].Format())
		}
	}

	// Equality is structural: equal only when every component matches.
	if got := types.NewInterval(0, 0, 0).Compare(types.NewInterval(0, 0, 0)); got != 0 {
		t.Errorf("zero interval not equal to itself: Compare = %d", got)
	}
	if got := types.NewInterval(1, 2, 3).Compare(types.NewInterval(1, 2, 3)); got != 0 {
		t.Errorf("equal tuples must compare 0, got %d", got)
	}
	// 12 months and 1 year are the same duration but distinct tuples, so they
	// are NOT equal — confirming the order is structural, not duration-based.
	if got := types.NewInterval(0, 12, 0).Compare(types.NewInterval(1, 0, 0)); got == 0 {
		t.Error("interval(12 months) must not structurally equal interval(1 year)")
	}
	// And not a duration order: 700 days is longer than 1 year yet sorts below
	// it, because the years component dominates.
	if got := types.NewInterval(0, 0, 700).Compare(types.NewInterval(1, 0, 0)); got != -1 {
		t.Errorf("structural order: (0,0,700) must sort below (1,0,0), got %d", got)
	}
}

func TestCompareDateChronological(t *testing.T) {
	early := types.NewDate(time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC))
	late := types.NewDate(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	if early.Compare(late) != -1 {
		t.Error("earlier date must sort first")
	}
}
