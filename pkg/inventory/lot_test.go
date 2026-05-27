package inventory_test

import (
	"testing"
	"time"

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
