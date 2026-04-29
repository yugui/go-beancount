package ast

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
)

// TestSeverityZeroValueIsError pins the invariant that a freshly
// constructed Diagnostic literal omitting Severity defaults to Error.
// Every Diagnostic emitter in the codebase relies on this; if a future
// edit ever makes Error not the iota-0 constant, this test fails loudly
// instead of silently flipping every diagnostic's severity.
func TestSeverityZeroValueIsError(t *testing.T) {
	var s Severity
	if s != Error {
		t.Errorf("Severity zero value = %d, want %d (Error)", s, Error)
	}
	if got := (Diagnostic{}).Severity; got != Error {
		t.Errorf("Diagnostic{}.Severity = %d, want %d (Error)", got, Error)
	}
}

func TestAmount(t *testing.T) {
	var num apd.Decimal
	if _, _, err := num.SetString("123.45"); err != nil {
		t.Fatalf("SetString: %v", err)
	}
	a := Amount{
		Number:   num,
		Currency: "USD",
	}
	if a.Currency != "USD" {
		t.Errorf("Currency = %q, want %q", a.Currency, "USD")
	}
	if a.Number.String() != "123.45" {
		t.Errorf("Number = %s, want 123.45", a.Number.String())
	}
}

func TestMetaValueString(t *testing.T) {
	mv := MetaValue{
		Kind:   MetaString,
		String: "hello",
	}
	if mv.Kind != MetaString {
		t.Errorf("Kind = %d, want MetaString (%d)", mv.Kind, MetaString)
	}
	if mv.String != "hello" {
		t.Errorf("String = %q, want %q", mv.String, "hello")
	}
}

func TestMetaValueDate(t *testing.T) {
	d := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	mv := MetaValue{
		Kind: MetaDate,
		Date: d,
	}
	if mv.Kind != MetaDate {
		t.Errorf("Kind = %d, want MetaDate (%d)", mv.Kind, MetaDate)
	}
	if !mv.Date.Equal(d) {
		t.Errorf("Date = %v, want %v", mv.Date, d)
	}
}

func TestMetaValueNumber(t *testing.T) {
	var num apd.Decimal
	if _, _, err := num.SetString("42.00"); err != nil {
		t.Fatalf("SetString: %v", err)
	}
	mv := MetaValue{
		Kind:   MetaNumber,
		Number: num,
	}
	if mv.Kind != MetaNumber {
		t.Errorf("Kind = %d, want MetaNumber (%d)", mv.Kind, MetaNumber)
	}
	if mv.Number.String() != "42.00" {
		t.Errorf("Number = %s, want 42.00", mv.Number.String())
	}
}

func TestMetaValueAmount(t *testing.T) {
	var num apd.Decimal
	if _, _, err := num.SetString("100.50"); err != nil {
		t.Fatalf("SetString: %v", err)
	}
	mv := MetaValue{
		Kind: MetaAmount,
		Amount: Amount{
			Number:   num,
			Currency: "EUR",
		},
	}
	if mv.Kind != MetaAmount {
		t.Errorf("Kind = %d, want MetaAmount (%d)", mv.Kind, MetaAmount)
	}
	if mv.Amount.Currency != "EUR" {
		t.Errorf("Amount.Currency = %q, want %q", mv.Amount.Currency, "EUR")
	}
}

func TestMetaValueBool(t *testing.T) {
	mv := MetaValue{
		Kind: MetaBool,
		Bool: true,
	}
	if mv.Kind != MetaBool {
		t.Errorf("Kind = %d, want MetaBool (%d)", mv.Kind, MetaBool)
	}
	if !mv.Bool {
		t.Error("Bool = false, want true")
	}
}

func TestMetadataProps(t *testing.T) {
	m := Metadata{
		Props: make(map[string]MetaValue),
	}
	m.Props["name"] = MetaValue{Kind: MetaString, String: "Alice"}
	m.Props["active"] = MetaValue{Kind: MetaBool, Bool: true}

	if len(m.Props) != 2 {
		t.Fatalf("Props length = %d, want 2", len(m.Props))
	}

	v, ok := m.Props["name"]
	if !ok {
		t.Fatal("Props[\"name\"] not found")
	}
	if v.Kind != MetaString || v.String != "Alice" {
		t.Errorf("Props[\"name\"] = {Kind: %d, String: %q}, want {Kind: %d, String: %q}",
			v.Kind, v.String, MetaString, "Alice")
	}

	v, ok = m.Props["active"]
	if !ok {
		t.Fatal("Props[\"active\"] not found")
	}
	if v.Kind != MetaBool || !v.Bool {
		t.Errorf("Props[\"active\"] = {Kind: %d, Bool: %v}, want {Kind: %d, Bool: true}",
			v.Kind, v.Bool, MetaBool)
	}
}
