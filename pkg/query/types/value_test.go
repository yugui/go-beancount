package types_test

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

func amount(t *testing.T, n, cur string) ast.Amount {
	t.Helper()
	return ast.Amount{Number: dec(t, n), Currency: cur}
}

func TestConstructorsTypeAndNotNull(t *testing.T) {
	inv := inventory.NewInventory()
	if err := inv.Add(inventory.Position{Units: amount(t, "10", "USD")}); err != nil {
		t.Fatalf("inv.Add: %v", err)
	}
	cases := []struct {
		name string
		v    types.Value
		want types.Type
	}{
		{"bool", types.NewBool(true), types.Bool},
		{"int", types.NewInt(42), types.Int},
		{"decimal", types.NewDecimal(dec(t, "1.50")), types.Decimal},
		{"string", types.NewString("hi"), types.String},
		{"date", types.NewDate(time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)), types.Date},
		{"amount", types.NewAmount(amount(t, "5", "JPY")), types.Amount},
		{"position", types.NewPosition(inventory.Position{Units: amount(t, "3", "EUR")}), types.Position},
		{"inventory", types.NewInventory(inv), types.Inventory},
		{"interval", types.NewInterval(1, 2, 3), types.Interval},
		{"set", types.NewSet("a", "b"), types.SetType},
		{"dict", types.NewDict(map[string]types.Value{"k": types.NewInt(1)}), types.DictType},
		{"entry", types.NewEntry(sampleTxn(t)), types.Entry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.Type(); got != tc.want {
				t.Errorf("Type() = %v, want %v", got, tc.want)
			}
			if tc.v.IsNull() {
				t.Errorf("IsNull() = true, want false")
			}
			if tc.v.Format() == "" {
				t.Errorf("Format() empty")
			}
			if tc.v.String() == "" {
				t.Errorf("String() empty")
			}
		})
	}
}

func TestAccessorRoundTrips(t *testing.T) {
	if b, ok := types.AsBool(types.NewBool(true)); !ok || b != true {
		t.Errorf("AsBool = (%v,%v)", b, ok)
	}
	if n, ok := types.AsInt(types.NewInt(7)); !ok || n != 7 {
		t.Errorf("AsInt = (%v,%v)", n, ok)
	}
	if d, ok := types.AsDecimal(types.NewDecimal(dec(t, "2.25"))); !ok || d.Cmp(ptr(dec(t, "2.25"))) != 0 {
		t.Errorf("AsDecimal = (%v,%v)", d.String(), ok)
	}
	if s, ok := types.AsString(types.NewString("x")); !ok || s != "x" {
		t.Errorf("AsString = (%v,%v)", s, ok)
	}
	want := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	if d, ok := types.AsDate(types.NewDate(want)); !ok || !d.Equal(want) {
		t.Errorf("AsDate = (%v,%v)", d, ok)
	}
	if a, ok := types.AsAmount(types.NewAmount(amount(t, "9", "GBP"))); !ok || a.Currency != "GBP" || a.Number.Cmp(ptr(dec(t, "9"))) != 0 {
		t.Errorf("AsAmount = (%+v,%v)", a, ok)
	}
	if p, ok := types.AsPosition(types.NewPosition(inventory.Position{Units: amount(t, "4", "CAD")})); !ok || p.Commodity() != "CAD" {
		t.Errorf("AsPosition = (%+v,%v)", p, ok)
	}
	if s, ok := types.AsSet(types.NewSet("z", "a")); !ok || !s.Contains("a") {
		t.Errorf("AsSet = (%v,%v)", s, ok)
	}
	if dv, ok := types.AsDict(types.NewDict(map[string]types.Value{"k": types.NewInt(1)})); !ok || dv.Len() != 1 {
		t.Errorf("AsDict = (%v,%v)", dv, ok)
	}
}

func TestAccessorWrongKind(t *testing.T) {
	v := types.NewInt(1)
	if _, ok := types.AsBool(v); ok {
		t.Error("AsBool on Int: ok=true")
	}
	if _, ok := types.AsString(v); ok {
		t.Error("AsString on Int: ok=true")
	}
	if _, ok := types.AsSet(v); ok {
		t.Error("AsSet on Int: ok=true")
	}
	if _, ok := types.AsDict(v); ok {
		t.Error("AsDict on Int: ok=true")
	}
}

func TestNull(t *testing.T) {
	for _, ty := range []types.Type{types.Int, types.Decimal, types.String, types.SetType, types.DictType} {
		n := types.Null(ty)
		if !n.IsNull() {
			t.Errorf("Null(%v).IsNull() = false", ty)
		}
		if n.Type() != ty {
			t.Errorf("Null(%v).Type() = %v", ty, n.Type())
		}
		if n.Format() != "NULL" {
			t.Errorf("Null(%v).Format() = %q", ty, n.Format())
		}
	}
	if _, ok := types.AsInt(types.Null(types.Int)); ok {
		t.Error("AsInt on Null(Int): ok=true")
	}
	if _, ok := types.AsDecimal(types.Null(types.Decimal)); ok {
		t.Error("AsDecimal on Null(Decimal): ok=true")
	}
	if _, ok := types.AsSet(types.Null(types.SetType)); ok {
		t.Error("AsSet on Null(SetType): ok=true")
	}
}

func TestInterval(t *testing.T) {
	v := types.NewInterval(1, 2, 3)
	if v.Type() != types.Interval {
		t.Errorf("Type() = %v, want Interval", v.Type())
	}
	if v.IsNull() {
		t.Error("IsNull() = true, want false")
	}
	if y, m, d, ok := types.AsInterval(v); !ok || y != 1 || m != 2 || d != 3 {
		t.Errorf("AsInterval = (%d,%d,%d,%v), want (1,2,3,true)", y, m, d, ok)
	}

	if _, _, _, ok := types.AsInterval(types.NewInt(1)); ok {
		t.Error("AsInterval on Int: ok=true")
	}
	if _, _, _, ok := types.AsInterval(types.Null(types.Interval)); ok {
		t.Error("AsInterval on Null(Interval): ok=true")
	}

	null := types.Null(types.Interval)
	if !null.IsNull() || null.Type() != types.Interval {
		t.Errorf("Null(Interval): IsNull=%v Type=%v", null.IsNull(), null.Type())
	}
	if null.Format() != "NULL" {
		t.Errorf("Null(Interval).Format() = %q, want NULL", null.Format())
	}
}

func TestIntervalFormat(t *testing.T) {
	cases := []struct {
		years, months, days int
		want                string
	}{
		{1, 2, 3, "1 year, 2 months, 3 days"},
		{0, -1, 0, "-1 month"},
		{-2, 0, 0, "-2 years"},
		{0, 0, 1, "1 day"},
		{0, 0, -1, "-1 day"},
		{0, 0, 0, "0 days"},
		{2, 0, 5, "2 years, 5 days"},
	}
	for _, tc := range cases {
		v := types.NewInterval(tc.years, tc.months, tc.days)
		if got := v.Format(); got != tc.want {
			t.Errorf("NewInterval(%d,%d,%d).Format() = %q, want %q", tc.years, tc.months, tc.days, got, tc.want)
		}
		if got := v.String(); got != tc.want {
			t.Errorf("NewInterval(%d,%d,%d).String() = %q, want %q", tc.years, tc.months, tc.days, got, tc.want)
		}
	}
}

func TestNewInventoryNilIsEmpty(t *testing.T) {
	v := types.NewInventory(nil)
	if v.Type() != types.Inventory || v.IsNull() {
		t.Fatalf("NewInventory(nil): Type=%v IsNull=%v", v.Type(), v.IsNull())
	}
	got, ok := types.AsInventory(v)
	if !ok || got.Len() != 0 {
		t.Fatalf("AsInventory: (%v,%v)", got, ok)
	}
}

// NewInventory clones at construction, so the caller's later mutation is
// not observable through the Value.
func TestNewInventoryClonesInput(t *testing.T) {
	inv := inventory.NewInventory()
	if err := inv.Add(inventory.Position{Units: amount(t, "10", "USD")}); err != nil {
		t.Fatalf("inv.Add: %v", err)
	}
	v := types.NewInventory(inv)
	if err := inv.Add(inventory.Position{Units: amount(t, "5", "EUR")}); err != nil {
		t.Fatalf("inv.Add: %v", err)
	}
	got, _ := types.AsInventory(v)
	if got.Len() != 1 {
		t.Errorf("Value observed caller mutation: Len=%d, want 1", got.Len())
	}
}

func TestFormatRepresentative(t *testing.T) {
	cases := []struct {
		v    types.Value
		want string
	}{
		{types.NewBool(false), "false"},
		{types.NewInt(-3), "-3"},
		{types.NewDecimal(dec(t, "1.500")), "1.500"},
		{types.NewString("hello"), "hello"},
		{types.NewDate(time.Date(2020, 3, 4, 0, 0, 0, 0, time.UTC)), "2020-03-04"},
		{types.NewAmount(amount(t, "12.50", "USD")), "12.50 USD"},
		{types.NewSet("b", "a", "a"), "{a, b}"},
		{types.NewDict(map[string]types.Value{"k": types.NewInt(1)}), "{k: 1}"},
		{types.Null(types.Int), "NULL"},
	}
	for _, tc := range cases {
		if got := tc.v.Format(); got != tc.want {
			t.Errorf("Format() = %q, want %q", got, tc.want)
		}
	}
}

func ptr(d apd.Decimal) *apd.Decimal { return &d }
