package inventory_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

func TestLot_Equal(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	base := inventory.Lot{
		Number:   mustDecimal(t, "100.5"),
		Currency: "USD",
		Date:     date,
		Label:    "lot-a",
	}

	tests := []struct {
		name string
		a, b inventory.Lot
		want bool
	}{
		{
			name: "identical",
			a:    base,
			b: inventory.Lot{
				Number: mustDecimal(t, "100.5"), Currency: "USD", Date: date, Label: "lot-a",
			},
			want: true,
		},
		{
			name: "different number",
			a:    base,
			b: inventory.Lot{
				Number: mustDecimal(t, "100.6"), Currency: "USD", Date: date, Label: "lot-a",
			},
			want: false,
		},
		{
			name: "different currency",
			a:    base,
			b: inventory.Lot{
				Number: mustDecimal(t, "100.5"), Currency: "EUR", Date: date, Label: "lot-a",
			},
			want: false,
		},
		{
			name: "different date",
			a:    base,
			b: inventory.Lot{
				Number: mustDecimal(t, "100.5"), Currency: "USD",
				Date:  time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
				Label: "lot-a",
			},
			want: false,
		},
		{
			name: "different label",
			a:    base,
			b: inventory.Lot{
				Number: mustDecimal(t, "100.5"), Currency: "USD", Date: date, Label: "lot-b",
			},
			want: false,
		},
		{
			name: "same instant different location",
			a: inventory.Lot{
				Number: mustDecimal(t, "100.5"), Currency: "USD",
				Date:  time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
				Label: "lot-a",
			},
			b: inventory.Lot{
				Number: mustDecimal(t, "100.5"), Currency: "USD",
				Date:  time.Date(2024, 1, 15, 12, 0, 0, 0, time.FixedZone("X", 0)),
				Label: "lot-a",
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(&tc.b); got != tc.want {
				t.Errorf("(%+v).Equal(%+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := tc.b.Equal(&tc.a); got != tc.want {
				t.Errorf("(%+v).Equal(%+v) = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}

	t.Run("nil and nil", func(t *testing.T) {
		var a, b *inventory.Lot
		if !a.Equal(b) {
			t.Errorf("Lot.Equal(nil, nil) = false, want true")
		}
	})

	t.Run("nil and non-nil", func(t *testing.T) {
		var a *inventory.Lot
		b := &inventory.Lot{Currency: "USD"}
		if a.Equal(b) {
			t.Errorf("Lot.Equal(nil, non-nil) = true, want false")
		}
		if b.Equal(a) {
			t.Errorf("Lot.Equal(non-nil, nil) = true, want false")
		}
	})
}

func TestLot_Clone(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var l *inventory.Lot
		if got := l.Clone(); got != nil {
			t.Errorf("(*Lot)(nil).Clone() = %v, want nil", got)
		}
	})

	t.Run("deep copy", func(t *testing.T) {
		orig := &inventory.Lot{
			Number:   mustDecimal(t, "42.5"),
			Currency: "USD",
			Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-1",
		}
		clone := orig.Clone()
		if clone == orig {
			t.Fatal("Lot.Clone() returned the same pointer")
		}
		if !clone.Equal(orig) {
			t.Errorf("Lot.Clone() = %+v, want equal to %+v", clone, orig)
		}

		clone.Label = "lot-2"
		newNum := mustDecimal(t, "99.9")
		clone.Number.Set(&newNum)

		if orig.Label != "lot-1" {
			t.Errorf("after mutating clone, orig.Label = %q, want %q", orig.Label, "lot-1")
		}
		if got := orig.Number.String(); got != "42.5" {
			t.Errorf("after mutating clone, orig.Number = %q, want %q", got, "42.5")
		}
		if got := clone.Number.String(); got != "99.9" {
			t.Errorf("after mutation, clone.Number = %q, want %q", got, "99.9")
		}
	})
}

func TestLotFromCost(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		if got := inventory.LotFromCost(nil); got != nil {
			t.Errorf("LotFromCost(nil) = %+v, want nil", got)
		}
	})

	t.Run("transfers identity fields and drops provenance", func(t *testing.T) {
		date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		cost := &ast.Cost{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Date:     date,
			Label:    "lot-a",
			PerUnit:  &ast.Amount{Number: mustDecimal(t, "100"), Currency: "USD"},
			Total:    &ast.Amount{Number: mustDecimal(t, "200"), Currency: "USD"},
		}
		got := inventory.LotFromCost(cost)
		if got == nil {
			t.Fatal("LotFromCost returned nil for non-nil input")
		}
		want := &inventory.Lot{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Date:     date,
			Label:    "lot-a",
		}
		if !got.Equal(want) {
			t.Errorf("LotFromCost lot identity = %+v, want %+v", got, want)
		}
	})

	t.Run("deep copies Number", func(t *testing.T) {
		cost := &ast.Cost{
			Number:   mustDecimal(t, "42"),
			Currency: "USD",
		}
		got := inventory.LotFromCost(cost)
		newNum := mustDecimal(t, "99")
		got.Number.Set(&newNum)
		if cost.Number.String() != "42" {
			t.Errorf("LotFromCost: after mutating result, input Cost.Number = %s, want 42", cost.Number.String())
		}
	})
}

func TestLot_ToCost(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var l *inventory.Lot
		if got := l.ToCost(); got != nil {
			t.Errorf("(*Lot).ToCost: (*Lot)(nil).ToCost() = %+v, want nil", got)
		}
	})

	t.Run("returns provenance-free *ast.Cost", func(t *testing.T) {
		date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		lot := &inventory.Lot{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Date:     date,
			Label:    "lot-a",
		}
		got := lot.ToCost()
		want := &ast.Cost{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Date:     date,
			Label:    "lot-a",
		}
		if diff := cmp.Diff(want, got, invCmpOpts...); diff != "" {
			t.Errorf("(*Lot).ToCost: (-want +got)\n%s", diff)
		}
	})

	t.Run("deep copies Number", func(t *testing.T) {
		lot := &inventory.Lot{Number: mustDecimal(t, "42"), Currency: "USD"}
		got := lot.ToCost()
		newNum := mustDecimal(t, "99")
		got.Number.Set(&newNum)
		if lot.Number.String() != "42" {
			t.Errorf("(*Lot).ToCost: after mutating result, lot.Number = %s, want 42", lot.Number.String())
		}
	})
}
