package ast

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

func mustDecimal(t *testing.T, s string) *apd.Decimal {
	t.Helper()
	d, _, err := apd.BaseContext.NewFromString(s)
	if err != nil {
		t.Fatalf("mustDecimal(%q): %v", s, err)
	}
	return d
}

func TestPrecisionProfileEmpty(t *testing.T) {
	p := NewPrecisionProfile()
	if prec, ok := p.MostCommon("USD"); ok || prec != 0 {
		t.Errorf("MostCommon on empty = (%d, %v), want (0, false)", prec, ok)
	}
	if got := p.Currencies(); got != nil {
		t.Errorf("Currencies on empty = %v, want nil", got)
	}
}

func TestPrecisionProfileSingleObservation(t *testing.T) {
	p := NewPrecisionProfile()
	p.Update(mustDecimal(t, "1.23"), "USD")
	prec, ok := p.MostCommon("USD")
	if !ok || prec != 2 {
		t.Errorf("MostCommon = (%d, %v), want (2, true)", prec, ok)
	}
}

func TestPrecisionProfileMixedExponents(t *testing.T) {
	p := NewPrecisionProfile()
	d2 := mustDecimal(t, "1.23")
	d3 := mustDecimal(t, "1.234")
	p.Update(d2, "USD")
	p.Update(d2, "USD")
	p.Update(d3, "USD")
	prec, ok := p.MostCommon("USD")
	if !ok || prec != 2 {
		t.Errorf("MostCommon = (%d, %v), want (2, true)", prec, ok)
	}
}

func TestPrecisionProfileTieBreakHighestWins(t *testing.T) {
	p := NewPrecisionProfile()
	d2 := mustDecimal(t, "1.23")
	d3 := mustDecimal(t, "1.234")
	p.Update(d2, "USD")
	p.Update(d3, "USD")
	prec, ok := p.MostCommon("USD")
	if !ok || prec != 3 {
		t.Errorf("MostCommon = (%d, %v), want (3, true)", prec, ok)
	}
}

func TestPrecisionProfileIntegerDecimal(t *testing.T) {
	// apd.New(160000, 0) stores an integer with exponent 0; precision is 0.
	t.Run("integer only", func(t *testing.T) {
		p := NewPrecisionProfile()
		p.Update(apd.New(160000, 0), "JPY")
		prec, ok := p.MostCommon("JPY")
		if !ok || prec != 0 {
			t.Errorf("MostCommon = (%d, %v), want (0, true)", prec, ok)
		}
	})

	// apd.New(16, 4) has positive exponent — precision clamps to 0.
	t.Run("positive exponent", func(t *testing.T) {
		p := NewPrecisionProfile()
		p.Update(apd.New(16, 4), "JPY")
		prec, ok := p.MostCommon("JPY")
		if !ok || prec != 0 {
			t.Errorf("MostCommon = (%d, %v), want (0, true)", prec, ok)
		}
	})

	t.Run("integer outvoted", func(t *testing.T) {
		p := NewPrecisionProfile()
		d2 := mustDecimal(t, "1.23")
		p.Update(apd.New(160000, 0), "JPY")
		p.Update(d2, "JPY")
		p.Update(d2, "JPY")
		prec, ok := p.MostCommon("JPY")
		if !ok || prec != 2 {
			t.Errorf("MostCommon = (%d, %v), want (2, true)", prec, ok)
		}
	})
}

func TestPrecisionProfileCurrenciesOrdering(t *testing.T) {
	p := NewPrecisionProfile()
	d := mustDecimal(t, "1.0")
	p.Update(d, "USD")
	p.Update(d, "JPY")
	p.Update(d, "EUR")
	got := p.Currencies()
	want := []string{"EUR", "JPY", "USD"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Currencies mismatch (-want +got):\n%s", diff)
	}
}

func TestPrecisionProfileNilReceiver(t *testing.T) {
	var p *PrecisionProfile
	if prec, ok := p.MostCommon("X"); ok || prec != 0 {
		t.Errorf("nil.MostCommon = (%d, %v), want (0, false)", prec, ok)
	}
	if got := p.Currencies(); got != nil {
		t.Errorf("nil.Currencies = %v, want nil", got)
	}
}

func TestPrecisionProfileNoOpUpdate(t *testing.T) {
	p := NewPrecisionProfile()
	d := mustDecimal(t, "1.23")

	p.Update(nil, "USD")
	p.Update(d, "")

	if got := p.Currencies(); got != nil {
		t.Errorf("Currencies after no-op updates = %v, want nil", got)
	}
	if _, ok := p.MostCommon("USD"); ok {
		t.Errorf("MostCommon(USD) after nil-decimal update = (_, true), want (_, false)")
	}
	if _, ok := p.MostCommon(""); ok {
		t.Errorf("MostCommon(\"\") = (_, true), want (_, false)")
	}
}
