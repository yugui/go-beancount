package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestMarshalTreeScalars(t *testing.T) {
	cases := []struct {
		name string
		v    types.Value
		want any
	}{
		{"bool true", types.NewBool(true), true},
		{"bool false", types.NewBool(false), false},
		{"int positive", types.NewInt(42), int64(42)},
		{"int negative", types.NewInt(-7), int64(-7)},
		{"int zero", types.NewInt(0), int64(0)},
		{"decimal fractional", types.NewDecimal(dec(t, "1.23")), "1.23"},
		{"decimal trailing zero", types.NewDecimal(dec(t, "0.10")), "0.10"},
		{"string", types.NewString("hello"), "hello"},
		{"string empty", types.NewString(""), ""},
		{"date", types.NewDate(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)), "2024-03-15"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := types.MarshalTree(tc.v)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("MarshalTree mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMarshalTreeNull(t *testing.T) {
	nullKinds := []types.Type{
		types.Bool, types.Int, types.Decimal, types.String, types.Date,
		types.Amount, types.Position, types.Inventory, types.Interval,
		types.SetType, types.DictType, types.Entry,
	}
	for _, ty := range nullKinds {
		t.Run(ty.String(), func(t *testing.T) {
			got := types.MarshalTree(types.Null(ty))
			if got != nil {
				t.Errorf("MarshalTree(Null(%v)) = %v, want nil", ty, got)
			}
		})
	}
}

func TestMarshalTreeAmount(t *testing.T) {
	a := ast.Amount{Number: dec(t, "12.50"), Currency: "USD"}
	got := types.MarshalTree(types.NewAmount(a))
	want := map[string]any{
		"number":   "12.50",
		"currency": "USD",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Amount) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreePositionNoCost(t *testing.T) {
	p := inventory.Position{Units: ast.Amount{Number: dec(t, "5"), Currency: "EUR"}}
	got := types.MarshalTree(types.NewPosition(p))
	want := map[string]any{
		"units": map[string]any{"number": "5", "currency": "EUR"},
		"cost":  nil,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Position no cost) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreePositionWithCost(t *testing.T) {
	lot := &inventory.Lot{
		Number:   dec(t, "100.00"),
		Currency: "USD",
		Date:     time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
		Label:    "lot-a",
	}
	p := inventory.Position{
		Units: ast.Amount{Number: dec(t, "10"), Currency: "AAPL"},
		Cost:  lot,
	}
	got := types.MarshalTree(types.NewPosition(p))
	want := map[string]any{
		"units": map[string]any{"number": "10", "currency": "AAPL"},
		"cost": map[string]any{
			"number":   "100.00",
			"currency": "USD",
			"date":     "2023-06-01",
			"label":    "lot-a",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Position with cost) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeCostZeroDate(t *testing.T) {
	lot := &inventory.Lot{
		Number:   dec(t, "50"),
		Currency: "USD",
		Date:     time.Time{}, // zero → "date" must be nil
		Label:    "",          // empty → "label" must be ""
	}
	p := inventory.Position{
		Units: ast.Amount{Number: dec(t, "1"), Currency: "BTC"},
		Cost:  lot,
	}
	got := types.MarshalTree(types.NewPosition(p))
	want := map[string]any{
		"units": map[string]any{"number": "1", "currency": "BTC"},
		"cost": map[string]any{
			"number":   "50",
			"currency": "USD",
			"date":     nil,
			"label":    "",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Position cost zero date) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeInventoryEmpty(t *testing.T) {
	got := types.MarshalTree(types.NewInventory(nil))
	want := []any{}
	// cmp.Diff distinguishes nil from empty slice, so this also asserts non-nil.
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(empty Inventory) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeInventoryMultiple(t *testing.T) {
	inv := inventory.NewInventory()
	p1 := inventory.Position{Units: ast.Amount{Number: dec(t, "10"), Currency: "USD"}}
	p2 := inventory.Position{
		Units: ast.Amount{Number: dec(t, "5"), Currency: "AAPL"},
		Cost: &inventory.Lot{
			Number:   dec(t, "150"),
			Currency: "USD",
			Date:     time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			Label:    "",
		},
	}
	if err := inv.Add(p1); err != nil {
		t.Fatalf("inv.Add(p1): %v", err)
	}
	if err := inv.Add(p2); err != nil {
		t.Fatalf("inv.Add(p2): %v", err)
	}

	// inventory.Inventory.All() yields positions in insertion order, so p1 then p2.
	got := types.MarshalTree(types.NewInventory(inv))
	want := []any{
		map[string]any{
			"units": map[string]any{"number": "10", "currency": "USD"},
			"cost":  nil,
		},
		map[string]any{
			"units": map[string]any{"number": "5", "currency": "AAPL"},
			"cost": map[string]any{
				"number":   "150",
				"currency": "USD",
				"date":     "2022-01-01",
				"label":    "",
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Inventory multiple) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeSetEmpty(t *testing.T) {
	got := types.MarshalTree(types.NewSet())
	want := []any{}
	// cmp.Diff distinguishes nil from empty slice, so this also asserts non-nil.
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(empty Set) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeSetElements(t *testing.T) {
	// Input is unsorted; output must be in ascending order.
	got := types.MarshalTree(types.NewSet("c", "a", "b"))
	want := []any{"a", "b", "c"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Set) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeDictEmpty(t *testing.T) {
	got := types.MarshalTree(types.NewDict(nil))
	want := map[string]any{}
	// cmp.Diff distinguishes nil from empty map, so this also asserts non-nil.
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(empty Dict) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeDictMixed(t *testing.T) {
	inner := types.NewDict(map[string]types.Value{
		"nested": types.NewString("val"),
	})
	d := types.NewDict(map[string]types.Value{
		"num":   types.NewInt(3),
		"nullv": types.Null(types.String),
		"sub":   inner,
	})
	got := types.MarshalTree(d)
	want := map[string]any{
		"num":   int64(3),
		"nullv": nil,
		"sub":   map[string]any{"nested": "val"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Dict mixed) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeDictWithComposite(t *testing.T) {
	// Dict whose value is a Set; verifies that recursion marshals composite children.
	d := types.NewDict(map[string]types.Value{
		"tags": types.NewSet("foo", "bar"),
	})
	got := types.MarshalTree(d)
	want := map[string]any{
		"tags": []any{"bar", "foo"}, // Set sorts ascending
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Dict with Set value) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeInterval(t *testing.T) {
	got := types.MarshalTree(types.NewInterval(1, 2, 3))
	want := map[string]any{
		"years":  int64(1),
		"months": int64(2),
		"days":   int64(3),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Interval) mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalTreeIntervalZero(t *testing.T) {
	got := types.MarshalTree(types.NewInterval(0, 0, 0))
	want := map[string]any{
		"years":  int64(0),
		"months": int64(0),
		"days":   int64(0),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("MarshalTree(Interval zero) mismatch (-want +got):\n%s", diff)
	}
}
