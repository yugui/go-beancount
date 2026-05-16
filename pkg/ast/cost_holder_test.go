package ast

import (
	"testing"
	"time"
)

// TestCostHolder_CostSpec_Accessors exercises every CostHolder
// accessor across the matrix of (PerUnit / Total / Currency presence)
// × (Date set / unset) × (Label empty / set). The synthesis contract
// for GetPerUnit / GetTotal — return a freshly allocated Amount only
// when both the number pointer and Currency are set — is checked by
// asserting the synthesized Amount's value, not pointer identity.
// IsBooked is always false for *CostSpec.
func TestCostHolder_CostSpec_Accessors(t *testing.T) {
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	perTen := dec(t, "10")
	totTwo := dec(t, "2")
	totFifty := dec(t, "50")

	cases := []struct {
		name         string
		spec         *CostSpec
		wantPerUnit  *Amount
		wantTotal    *Amount
		wantCurrency string
		wantDate     time.Time
		wantHasDate  bool
		wantLabel    string
	}{
		{
			name: "empty",
			spec: &CostSpec{},
		},
		{
			name:      "label only",
			spec:      &CostSpec{Label: "lot-A"},
			wantLabel: "lot-A",
		},
		{
			name:        "date only",
			spec:        &CostSpec{Date: &date},
			wantDate:    date,
			wantHasDate: true,
		},
		{
			name:         "currency only",
			spec:         &CostSpec{Currency: "JPY"},
			wantCurrency: "JPY",
		},
		{
			name:         "per-unit only",
			spec:         &CostSpec{PerUnit: &perTen, Currency: "USD"},
			wantPerUnit:  amount(t, "10", "USD"),
			wantCurrency: "USD",
		},
		{
			name:         "per-unit with date and label",
			spec:         &CostSpec{PerUnit: &perTen, Currency: "USD", Date: &date, Label: "lot-B"},
			wantPerUnit:  amount(t, "10", "USD"),
			wantCurrency: "USD",
			wantDate:     date,
			wantHasDate:  true,
			wantLabel:    "lot-B",
		},
		{
			name:         "total only",
			spec:         &CostSpec{Total: &totFifty, Currency: "JPY"},
			wantTotal:    amount(t, "50", "JPY"),
			wantCurrency: "JPY",
		},
		{
			name:         "total only with date",
			spec:         &CostSpec{Total: &totFifty, Currency: "JPY", Date: &date},
			wantTotal:    amount(t, "50", "JPY"),
			wantCurrency: "JPY",
			wantDate:     date,
			wantHasDate:  true,
		},
		{
			name:         "surcharge",
			spec:         &CostSpec{PerUnit: &perTen, Total: &totTwo, Currency: "USD"},
			wantPerUnit:  amount(t, "10", "USD"),
			wantTotal:    amount(t, "2", "USD"),
			wantCurrency: "USD",
		},
		{
			// Per-unit number set without a currency: GetPerUnit
			// suppresses synthesis to avoid emitting a currency-less
			// Amount, which would violate the package invariant.
			name:         "per-unit without currency",
			spec:         &CostSpec{PerUnit: &perTen},
			wantCurrency: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h CostHolder = tc.spec
			gotPerUnit := h.GetPerUnit()
			if !amountEqual(gotPerUnit, tc.wantPerUnit) {
				t.Errorf("GetPerUnit = %v, want %v", gotPerUnit, tc.wantPerUnit)
			}
			gotTotal := h.GetTotal()
			if !amountEqual(gotTotal, tc.wantTotal) {
				t.Errorf("GetTotal = %v, want %v", gotTotal, tc.wantTotal)
			}
			if got := h.GetCurrency(); got != tc.wantCurrency {
				t.Errorf("GetCurrency = %q, want %q", got, tc.wantCurrency)
			}
			gotDate, gotOK := h.GetDate()
			if gotOK != tc.wantHasDate {
				t.Errorf("GetDate ok = %v, want %v", gotOK, tc.wantHasDate)
			}
			if gotOK && !gotDate.Equal(tc.wantDate) {
				t.Errorf("GetDate = %v, want %v", gotDate, tc.wantDate)
			}
			if got := h.GetLabel(); got != tc.wantLabel {
				t.Errorf("GetLabel = %q, want %q", got, tc.wantLabel)
			}
			if h.IsBooked() {
				t.Error("IsBooked = true, want false for *CostSpec")
			}
		})
	}
}

// amountEqual compares two *Amount by value (Number and Currency),
// treating two nils as equal. Used in the *CostSpec accessor tests
// because GetPerUnit / GetTotal synthesize a fresh Amount each call,
// so pointer identity is meaningless.
func amountEqual(a, b *Amount) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Currency != b.Currency {
		return false
	}
	return a.Number.Cmp(&b.Number) == 0
}

// TestCostSpec_GetPerUnit_AllocatesFresh asserts the synthesis
// contract: each call returns a freshly allocated Amount whose
// Decimal is independent of the spec's PerUnit field, so callers
// may mutate the returned Number without disturbing the spec.
func TestCostSpec_GetPerUnit_AllocatesFresh(t *testing.T) {
	num := dec(t, "12.5")
	spec := &CostSpec{PerUnit: &num, Currency: "USD"}

	got := spec.GetPerUnit()
	if got == nil {
		t.Fatal("GetPerUnit returned nil for populated spec")
	}
	mutated := dec(t, "999")
	got.Number.Set(&mutated)
	if spec.PerUnit.Cmp(&mutated) == 0 {
		t.Errorf("mutating GetPerUnit result leaked into spec.PerUnit: got %s, want 12.5", spec.PerUnit.String())
	}
}

// TestCostSpec_GetTotal_AllocatesFresh mirrors
// TestCostSpec_GetPerUnit_AllocatesFresh for the Total accessor: the
// synthesis contract is symmetric, and locking it independently
// catches a regression where one accessor's allocation behaviour
// drifts from the other's.
func TestCostSpec_GetTotal_AllocatesFresh(t *testing.T) {
	num := dec(t, "12.5")
	spec := &CostSpec{Total: &num, Currency: "USD"}

	got := spec.GetTotal()
	if got == nil {
		t.Fatal("GetTotal returned nil for populated spec")
	}
	mutated := dec(t, "999")
	got.Number.Set(&mutated)
	if spec.Total.Cmp(&mutated) == 0 {
		t.Errorf("mutating GetTotal result leaked into spec.Total: got %s, want 12.5", spec.Total.String())
	}
}

// TestCostHolder_Cost_Accessors covers the *Cost-specific behaviour
// of the CostHolder accessors. The form-independent dispatch
// (GetCurrency / GetDate / GetLabel / IsBooked traversing the
// interface) is already exercised by TestCostHolder_CostSpec_Accessors
// above; what *Cost adds is (a) GetCurrency reading the dedicated
// Currency field rather than deriving from PerUnit / Total, (b)
// GetDate distinguishing zero-value from set Date via IsZero
// (different code path from CostSpec's nil-pointer check), and (c)
// retention of the user's PerUnit / Total literals across the four
// form variants the reducer's terminal pass produces. Each form has
// its own t.Run subtest because the form is the genuinely distinct
// dimension here; the date / label cross-product would only re-run
// the same code paths.
func TestCostHolder_Cost_Accessors(t *testing.T) {
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("zero value", func(t *testing.T) {
		// The cash-position sentinel used by
		// inventory.ReductionStep.Lot. GetDate must return false
		// (not a non-nil pointer to 0001-01-01); IsBooked is still
		// true because the concrete type is *Cost.
		var h CostHolder = &Cost{}
		if got := h.GetPerUnit(); got != nil {
			t.Errorf("GetPerUnit = %v, want nil", got)
		}
		if got := h.GetTotal(); got != nil {
			t.Errorf("GetTotal = %v, want nil", got)
		}
		if got := h.GetCurrency(); got != "" {
			t.Errorf("GetCurrency = %q, want empty", got)
		}
		if _, ok := h.GetDate(); ok {
			t.Error("GetDate ok = true, want false on zero Date")
		}
		if got := h.GetLabel(); got != "" {
			t.Errorf("GetLabel = %q, want empty", got)
		}
		if !h.IsBooked() {
			t.Error("IsBooked = false, want true for *Cost")
		}
	})

	t.Run("per-unit form retains PerUnit", func(t *testing.T) {
		// {X CUR}: PerUnit carries the user's literal, Total is nil,
		// Number == PerUnit.Number for the per-unit-only spec.
		perUSD := amount(t, "12.5", "USD")
		cost := &Cost{Number: dec(t, "12.5"), Currency: "USD", Date: date, Label: "lot-B", PerUnit: perUSD}
		var h CostHolder = cost
		if got := h.GetPerUnit(); got != perUSD {
			t.Errorf("GetPerUnit = %v, want field pointer %v", got, perUSD)
		}
		if got := h.GetTotal(); got != nil {
			t.Errorf("GetTotal = %v, want nil", got)
		}
		if got := h.GetCurrency(); got != "USD" {
			t.Errorf("GetCurrency = %q, want USD", got)
		}
		gotDate, ok := h.GetDate()
		if !ok || !gotDate.Equal(date) {
			t.Errorf("GetDate = (%v, %v), want (%v, true)", gotDate, ok, date)
		}
		if got := h.GetLabel(); got != "lot-B" {
			t.Errorf("GetLabel = %q, want lot-B", got)
		}
	})

	t.Run("total-only form retains Total", func(t *testing.T) {
		// {{Y CUR}}: PerUnit nil, Total carries the user's literal.
		// Number is the resolved per-unit value Y/|units|.
		totJPY := amount(t, "50", "JPY")
		cost := &Cost{Number: dec(t, "5"), Currency: "JPY", Date: date, Total: totJPY}
		var h CostHolder = cost
		if got := h.GetPerUnit(); got != nil {
			t.Errorf("GetPerUnit = %v, want nil for {{Y CUR}} form", got)
		}
		if got := h.GetTotal(); got != totJPY {
			t.Errorf("GetTotal = %v, want field pointer %v", got, totJPY)
		}
		if got := h.GetCurrency(); got != "JPY" {
			t.Errorf("GetCurrency = %q, want JPY", got)
		}
	})

	t.Run("surcharge form retains both", func(t *testing.T) {
		// {X CUR, # CUR}: both PerUnit and Total are set; the
		// canonical Number is X + #/|units| (computed elsewhere —
		// this test does not assert its value, just that retention
		// and accessors line up).
		perUSD := amount(t, "12.5", "USD")
		surUSD := amount(t, "1", "USD")
		cost := &Cost{Number: dec(t, "12.5"), Currency: "USD", Date: date, PerUnit: perUSD, Total: surUSD}
		var h CostHolder = cost
		if got := h.GetPerUnit(); got != perUSD {
			t.Errorf("GetPerUnit = %v, want field pointer %v", got, perUSD)
		}
		if got := h.GetTotal(); got != surUSD {
			t.Errorf("GetTotal = %v, want field pointer %v", got, surUSD)
		}
		if got := h.GetCurrency(); got != "USD" {
			t.Errorf("GetCurrency = %q, want USD", got)
		}
	})
}

// TestCost_Equal exercises Cost.Equal across both equal and unequal
// cases. The equal cases cover the lot-identity contract: two costs
// with matching Number / Currency / Date / Label compare equal even
// when their PerUnit / Total provenance fields differ — the printer
// round-trip fields are not part of lot identity. The unequal cases
// cover each identity dimension independently so a regression that
// drops a field from the comparison is caught at the smallest
// possible assertion.
func TestCost_Equal(t *testing.T) {
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	otherDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	base := func() *Cost {
		return &Cost{
			Number:   dec(t, "10"),
			Currency: "USD",
			Date:     date,
			Label:    "lot",
		}
	}

	withPerUnit := base()
	withPerUnit.PerUnit = amount(t, "10", "USD")
	withTotal := base()
	withTotal.Total = amount(t, "30", "USD")

	diffNumber := base()
	diffNumber.Number = dec(t, "11")
	diffCurrency := base()
	diffCurrency.Currency = "JPY"
	diffDate := base()
	diffDate.Date = otherDate
	diffLabel := base()
	diffLabel.Label = "other"

	cases := []struct {
		name string
		a, b *Cost
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "lhs nil", a: nil, b: base(), want: false},
		{name: "rhs nil", a: base(), b: nil, want: false},
		{name: "identical", a: base(), b: base(), want: true},

		// Lot identity holds across PerUnit / Total provenance:
		// these are presentation fields, not lot identity.
		{name: "ignores PerUnit retention", a: base(), b: withPerUnit, want: true},
		{name: "ignores Total retention", a: base(), b: withTotal, want: true},
		{name: "ignores both PerUnit and Total", a: withPerUnit, b: withTotal, want: true},

		// Lot identity dimensions.
		{name: "different Number", a: base(), b: diffNumber, want: false},
		{name: "different Currency", a: base(), b: diffCurrency, want: false},
		{name: "different Date", a: base(), b: diffDate, want: false},
		{name: "different Label", a: base(), b: diffLabel, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != tc.want {
				t.Errorf("Equal = %v, want %v", got, tc.want)
			}
			// Equal must be symmetric.
			if got := tc.b.Equal(tc.a); got != tc.want {
				t.Errorf("Equal (reversed) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCost_Clone_DeepCopiesPerUnitAndTotal(t *testing.T) {
	orig := &Cost{
		Number:   dec(t, "10"),
		Currency: "USD",
		Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Label:    "lot",
		PerUnit:  amount(t, "10", "USD"),
		Total:    amount(t, "30", "USD"),
	}
	clone := orig.Clone()

	if clone.PerUnit == orig.PerUnit {
		t.Error("Clone shares PerUnit pointer with original")
	}
	if clone.Total == orig.Total {
		t.Error("Clone shares Total pointer with original")
	}

	// Mutate the clone's PerUnit; the original must be untouched.
	clone.PerUnit.Currency = "JPY"
	if orig.PerUnit.Currency != "USD" {
		t.Errorf("mutating clone.PerUnit.Currency leaked: orig=%q, want USD", orig.PerUnit.Currency)
	}
}

func TestCost_Clone_NilSafe(t *testing.T) {
	var c *Cost
	if got := c.Clone(); got != nil {
		t.Errorf("(*Cost)(nil).Clone() = %v, want nil", got)
	}
}
