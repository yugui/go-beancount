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
	m := NewCostMatcher(nil, "")
	if !m.IsEmpty() {
		t.Errorf("NewCostMatcher(nil, \"\") should be empty, got %+v", m)
	}
}

func TestNewCostMatcherNilWithHint(t *testing.T) {
	m := NewCostMatcher(nil, "USD")
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
	spec := &ast.CostSpec{
		PerUnit: &ast.Amount{Number: decimalVal(t, "100"), Currency: "USD"},
	}
	m := NewCostMatcher(spec, "")
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
	spec := &ast.CostSpec{
		Total: &ast.Amount{Number: decimalVal(t, "500"), Currency: "USD"},
	}
	m := NewCostMatcher(spec, "")
	if m.HasPerUnit {
		t.Error("HasPerUnit should be false for total-only spec")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
}

func TestNewCostMatcherCombinedForm(t *testing.T) {
	// Combined form `{100 # 500 USD}`: both PerUnit and Total are
	// present. ResolveCost stores the lot's Number as
	// per + total/|units| (e.g. 100 + 500/10 = 150 for 10 units), so a
	// matcher built from the same spec MUST NOT constrain Number on
	// PerUnit alone — it would never match the lot it helped create.
	// The Total is informational for gain calculation, and lot
	// selection is currency-only.
	spec := &ast.CostSpec{
		PerUnit: &ast.Amount{Number: decimalVal(t, "100"), Currency: "USD"},
		Total:   &ast.Amount{Number: decimalVal(t, "500"), Currency: "USD"},
	}
	m := NewCostMatcher(spec, "")
	if m.HasPerUnit {
		t.Error("HasPerUnit must be false for combined form {per # total CUR}")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
	// The authoritative resolved Number for 10 units is 100 + 500/10 = 150.
	if !m.Matches(Cost{Number: decimalVal(t, "150"), Currency: "USD"}) {
		t.Error("USD lot with resolved per-unit 150 should match combined-form matcher")
	}
	// Since the matcher is currency-only, it must accept any USD cost,
	// regardless of the Number stored on the lot.
	if !m.Matches(Cost{Number: decimalVal(t, "0"), Currency: "USD"}) {
		t.Error("zero-Number USD lot should match a currency-only combined-form matcher")
	}
	if !m.Matches(Cost{Number: decimalVal(t, "200"), Currency: "USD"}) {
		t.Error("arbitrary USD lot should match a currency-only combined-form matcher")
	}
	// A non-USD lot must still be rejected.
	if m.Matches(Cost{Number: decimalVal(t, "150"), Currency: "EUR"}) {
		t.Error("EUR lot must not match a USD combined-form matcher")
	}
}

func TestNewCostMatcherDateAndLabel(t *testing.T) {
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{
		PerUnit: &ast.Amount{Number: decimalVal(t, "100"), Currency: "USD"},
		Date:    &date,
		Label:   "lot-a",
	}
	m := NewCostMatcher(spec, "")
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
	m := NewCostMatcher(spec, "USD")
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD (fallback to hint)", m.Currency)
	}
}
