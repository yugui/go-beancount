package inventory

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestCostMatcherZeroValueMatchesAny(t *testing.T) {
	var m CostMatcher
	if !m.IsEmpty() {
		t.Error("zero matcher should report IsEmpty")
	}

	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	cases := []Cost{
		{}, // cash / zero-value lot
		{Number: decimalVal(t, "100"), Currency: "USD", Date: date, Label: "lot"},
		{Number: decimalVal(t, "0"), Currency: "EUR"},
	}
	for _, c := range cases {
		if !m.Matches(c) {
			t.Errorf("empty matcher rejected %+v", c)
		}
	}
}

func TestCostMatcherCurrencyOnly(t *testing.T) {
	m := CostMatcher{Currency: "USD"}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	if !m.Matches(Cost{Number: decimalVal(t, "100"), Currency: "USD", Date: date}) {
		t.Error("USD lot should match currency-only matcher")
	}
	if m.Matches(Cost{Number: decimalVal(t, "100"), Currency: "EUR", Date: date}) {
		t.Error("EUR lot should not match currency-only matcher")
	}
	if m.Matches(Cost{}) {
		t.Error("zero Cost should not match a currency-only matcher")
	}
}

func TestCostMatcherPerUnit(t *testing.T) {
	m := CostMatcher{
		HasPerUnit: true,
		PerUnit:    decimalVal(t, "100"),
		Currency:   "USD",
	}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	if !m.Matches(Cost{Number: decimalVal(t, "100"), Currency: "USD", Date: date}) {
		t.Error("exact match should succeed")
	}
	if !m.Matches(Cost{Number: decimalVal(t, "100.0"), Currency: "USD", Date: date}) {
		t.Error("different scale of same value should match")
	}
	if m.Matches(Cost{Number: decimalVal(t, "101"), Currency: "USD", Date: date}) {
		t.Error("different number should not match")
	}
	if m.Matches(Cost{Number: decimalVal(t, "100"), Currency: "EUR", Date: date}) {
		t.Error("different currency should not match")
	}
}

func TestCostMatcherDateAndLabel(t *testing.T) {
	d1 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)

	m := CostMatcher{HasDate: true, Date: d1}
	if !m.Matches(Cost{Date: d1}) {
		t.Error("matching date should pass")
	}
	if m.Matches(Cost{Date: d2}) {
		t.Error("mismatched date should fail")
	}

	lbl := CostMatcher{HasLabel: true, Label: "lot-a"}
	if !lbl.Matches(Cost{Label: "lot-a"}) {
		t.Error("matching label should pass")
	}
	if lbl.Matches(Cost{Label: "lot-b"}) {
		t.Error("mismatched label should fail")
	}
}

func TestNewCostMatcherNilNoHint(t *testing.T) {
	m := NewCostMatcher(nil, "", nil)
	if !m.IsEmpty() {
		t.Errorf("NewCostMatcher(nil, \"\", nil) should be empty, got %+v", m)
	}
}

func TestNewCostMatcherNilWithHint(t *testing.T) {
	m := NewCostMatcher(nil, "USD", nil)
	if m.IsEmpty() {
		t.Error("currency-hint matcher should not be empty")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
	if m.HasPerUnit {
		t.Error("HasPerUnit should be false")
	}
}

func TestNewCostMatcherPerUnit(t *testing.T) {
	perUnit := decimalVal(t, "100")
	spec := &ast.CostSpec{
		PerUnit:  &perUnit,
		Currency: "USD",
	}
	m := NewCostMatcher(spec, "", nil)
	if !m.HasPerUnit {
		t.Error("HasPerUnit should be true")
	}
	want := decimalVal(t, "100")
	if m.PerUnit.Cmp(&want) != 0 {
		t.Errorf("PerUnit = %s, want 100", m.PerUnit.String())
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
}

func TestNewCostMatcherTotalOnly(t *testing.T) {
	// Total-only spec `{{500 USD}}` on 10 reducing units must derive
	// a per-unit constraint of 500/10 = 50 USD, mirroring what
	// ResolveCost stores on an augmentation from the same spec.
	total := decimalVal(t, "500")
	spec := &ast.CostSpec{
		Total:    &total,
		Currency: "USD",
	}
	units := ast.Amount{Number: decimalVal(t, "-10"), Currency: "STOCK"}
	m := NewCostMatcher(spec, "", &units)
	if !m.HasPerUnit {
		t.Error("HasPerUnit should be true once units are known")
	}
	want := decimalVal(t, "50")
	if m.PerUnit.Cmp(&want) != 0 {
		t.Errorf("PerUnit = %s, want 50", m.PerUnit.String())
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
	if !m.Matches(Cost{Number: decimalVal(t, "50"), Currency: "USD"}) {
		t.Error("Matches: USD lot at per-unit 50 should match {{500 USD}} on 10 units")
	}
	if m.Matches(Cost{Number: decimalVal(t, "60"), Currency: "USD"}) {
		t.Error("Matches: USD lot at per-unit 60 must not match {{500 USD}} on 10 units")
	}
}

func TestNewCostMatcherTotalOnlyWithoutUnitsFallsBack(t *testing.T) {
	// Without units (units == nil), the matcher falls back to
	// currency-only to remain usable from Pass 2 deferred-cost paths.
	total := decimalVal(t, "500")
	spec := &ast.CostSpec{
		Total:    &total,
		Currency: "USD",
	}
	m := NewCostMatcher(spec, "", nil)
	if m.HasPerUnit {
		t.Error("HasPerUnit must be false when units are unavailable")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
}

func TestNewCostMatcherCombinedForm(t *testing.T) {
	// Combined form `{100 # 500 USD}` on 10 units: ResolveCost stores
	// the lot's Number as 100 + 500/10 = 150, so the matcher must
	// derive the same per-unit value to find the lot it helped create.
	perUnit := decimalVal(t, "100")
	total := decimalVal(t, "500")
	spec := &ast.CostSpec{
		PerUnit:  &perUnit,
		Total:    &total,
		Currency: "USD",
	}
	units := ast.Amount{Number: decimalVal(t, "-10"), Currency: "STOCK"}
	m := NewCostMatcher(spec, "", &units)
	if !m.HasPerUnit {
		t.Error("HasPerUnit should be true once units are known")
	}
	want := decimalVal(t, "150")
	if m.PerUnit.Cmp(&want) != 0 {
		t.Errorf("PerUnit = %s, want 150", m.PerUnit.String())
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
	if !m.Matches(Cost{Number: decimalVal(t, "150"), Currency: "USD"}) {
		t.Error("Matches: USD lot with per-unit 150 should match combined-form matcher")
	}
	if m.Matches(Cost{Number: decimalVal(t, "100"), Currency: "USD"}) {
		t.Error("Matches: USD lot at per-unit 100 (PerUnit alone) must not match the combined-form derived 150")
	}
	if m.Matches(Cost{Number: decimalVal(t, "150"), Currency: "EUR"}) {
		t.Error("Matches: EUR lot must not match a USD combined-form matcher")
	}
}

func TestNewCostMatcherCombinedFormWithoutUnitsFallsBack(t *testing.T) {
	// Without units the combined-form matcher falls back to
	// currency-only, mirroring TestNewCostMatcherTotalOnlyWithoutUnitsFallsBack.
	perUnit := decimalVal(t, "100")
	total := decimalVal(t, "500")
	spec := &ast.CostSpec{
		PerUnit:  &perUnit,
		Total:    &total,
		Currency: "USD",
	}
	m := NewCostMatcher(spec, "", nil)
	if m.HasPerUnit {
		t.Error("HasPerUnit must be false when units are unavailable")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
}

func TestNewCostMatcherTotalOnlyZeroUnitsFallsBack(t *testing.T) {
	// A zero units amount yields a 0 denominator; the matcher must
	// fall back to currency-only rather than emit an arithmetic error.
	total := decimalVal(t, "500")
	spec := &ast.CostSpec{
		Total:    &total,
		Currency: "USD",
	}
	units := ast.Amount{Number: decimalVal(t, "0"), Currency: "STOCK"}
	m := NewCostMatcher(spec, "", &units)
	if m.HasPerUnit {
		t.Error("HasPerUnit must be false when units are zero")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
}

func TestNewCostMatcherDateAndLabel(t *testing.T) {
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	perUnit := decimalVal(t, "100")
	spec := &ast.CostSpec{
		PerUnit:  &perUnit,
		Currency: "USD",
		Date:     &date,
		Label:    "lot-a",
	}
	m := NewCostMatcher(spec, "", nil)
	if !m.HasDate {
		t.Error("HasDate should be true")
	}
	if !m.Date.Equal(date) {
		t.Errorf("Date = %v, want %v", m.Date, date)
	}
	if !m.HasLabel || m.Label != "lot-a" {
		t.Errorf("Label = %q, HasLabel = %v", m.Label, m.HasLabel)
	}
}

func TestNewCostMatcherEmptySpecFallsBackToHint(t *testing.T) {
	spec := &ast.CostSpec{}
	m := NewCostMatcher(spec, "USD", nil)
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD (fallback to hint)", m.Currency)
	}
}
