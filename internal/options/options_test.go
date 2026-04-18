package options

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

// testRegistry constructs a registry that exercises all four kinds so unit
// tests do not depend on which specs are wired into defaultRegistry.
func testRegistry(t *testing.T) *registry {
	t.Helper()
	r := newRegistry()
	if err := r.register(spec{
		key:          "title",
		kind:         kindString,
		parse:        parseStringOption,
		defaultValue: "default title",
	}); err != nil {
		t.Fatalf("testRegistry: register title: %v", err)
	}
	if err := r.register(spec{
		key:          "infer_from_cost",
		kind:         kindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}); err != nil {
		t.Fatalf("testRegistry: register infer_from_cost: %v", err)
	}
	def := apd.New(1, -1) // 0.1
	if err := r.register(spec{
		key:          "tolerance_multiplier",
		kind:         kindDecimal,
		parse:        parseDecimalOption,
		defaultValue: def,
	}); err != nil {
		t.Fatalf("testRegistry: register tolerance_multiplier: %v", err)
	}
	if err := r.register(spec{
		key:          "operating_currency",
		kind:         kindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	}); err != nil {
		t.Fatalf("testRegistry: register operating_currency: %v", err)
	}
	return r
}

func TestRegistryDefaults(t *testing.T) {
	v := newValues(testRegistry(t))
	if got := v.String("title"); got != "default title" {
		t.Errorf("TestRegistryDefaults: String default = %q, want %q", got, "default title")
	}
	if got := v.Bool("infer_from_cost"); got != false {
		t.Errorf("TestRegistryDefaults: Bool default = %v, want false", got)
	}
	d := v.Decimal("tolerance_multiplier")
	if d == nil {
		t.Fatalf("TestRegistryDefaults: Decimal default = nil")
	}
	if s := d.String(); s != "0.1" {
		t.Errorf("TestRegistryDefaults: Decimal default = %q, want %q", s, "0.1")
	}
	if got := v.StringList("operating_currency"); got != nil {
		t.Errorf("TestRegistryDefaults: StringList default = %v, want nil", got)
	}
}

func TestRegistryParseSuccess(t *testing.T) {
	v := newValues(testRegistry(t))
	if err := v.set("title", "My Ledger"); err != nil {
		t.Fatalf("TestRegistryParseSuccess: set title: %v", err)
	}
	if err := v.set("infer_from_cost", "TRUE"); err != nil {
		t.Fatalf("TestRegistryParseSuccess: set infer_from_cost: %v", err)
	}
	if err := v.set("tolerance_multiplier", "0.5"); err != nil {
		t.Fatalf("TestRegistryParseSuccess: set tolerance_multiplier: %v", err)
	}
	if err := v.set("operating_currency", "USD"); err != nil {
		t.Fatalf("TestRegistryParseSuccess: set operating_currency: %v", err)
	}

	if got := v.String("title"); got != "My Ledger" {
		t.Errorf("TestRegistryParseSuccess: String = %q, want %q", got, "My Ledger")
	}
	if got := v.Bool("infer_from_cost"); got != true {
		t.Errorf("TestRegistryParseSuccess: Bool = %v, want true", got)
	}
	d := v.Decimal("tolerance_multiplier")
	if d.String() != "0.5" {
		t.Errorf("TestRegistryParseSuccess: Decimal = %q, want %q", d.String(), "0.5")
	}
	// Mutating the returned decimal must not affect the stored value.
	d.SetInt64(999)
	d2 := v.Decimal("tolerance_multiplier")
	if d2.String() != "0.5" {
		t.Errorf("TestRegistryParseSuccess: Decimal after mutation = %q, want %q", d2.String(), "0.5")
	}
	got := v.StringList("operating_currency")
	want := []string{"USD"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestRegistryParseSuccess: StringList mismatch (-want +got):\n%s", diff)
	}
	// Mutating the returned slice must not affect the stored value.
	got[0] = "MUTATED"
	got2 := v.StringList("operating_currency")
	if diff := cmp.Diff([]string{"USD"}, got2); diff != "" {
		t.Errorf("TestRegistryParseSuccess: StringList after mutation mismatch (-want +got):\n%s", diff)
	}
}

func TestRegistryParseError(t *testing.T) {
	v := newValues(testRegistry(t))
	if err := v.set("tolerance_multiplier", "not-a-number"); err == nil {
		t.Errorf("TestRegistryParseError: set bad decimal: err = nil, want non-nil")
	}
	if err := v.set("infer_from_cost", "maybe"); err == nil {
		t.Errorf("TestRegistryParseError: set bad bool: err = nil, want non-nil")
	}
	if err := v.set("operating_currency", "   "); err == nil {
		t.Errorf("TestRegistryParseError: set empty currency: err = nil, want non-nil")
	}
}

func TestRegistryDuplicateScalarOverwrites(t *testing.T) {
	v := newValues(testRegistry(t))
	if err := v.set("title", "first"); err != nil {
		t.Fatal(err)
	}
	if err := v.set("title", "second"); err != nil {
		t.Fatal(err)
	}
	if got := v.String("title"); got != "second" {
		t.Errorf("TestRegistryDuplicateScalarOverwrites: String = %q, want %q", got, "second")
	}
}

func TestRegistryStringListAppends(t *testing.T) {
	v := newValues(testRegistry(t))
	for _, c := range []string{"USD", "JPY", "EUR"} {
		if err := v.set("operating_currency", c); err != nil {
			t.Fatalf("TestRegistryStringListAppends: set %q: %v", c, err)
		}
	}
	got := v.StringList("operating_currency")
	want := []string{"USD", "JPY", "EUR"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestRegistryStringListAppends: StringList mismatch (-want +got):\n%s", diff)
	}
}

func TestRegistryUnknownKeyIgnored(t *testing.T) {
	v := newValues(testRegistry(t))
	if err := v.set("no_such_option", "whatever"); err != nil {
		t.Errorf("TestRegistryUnknownKeyIgnored: set unknown: err = %v, want nil", err)
	}
	if _, ok := v.values["no_such_option"]; ok {
		t.Errorf("TestRegistryUnknownKeyIgnored: unknown key was stored")
	}
}

func TestDefaultRegistryHasOperatingCurrency(t *testing.T) {
	if _, ok := defaultRegistry.specs["operating_currency"]; !ok {
		t.Errorf("TestDefaultRegistryHasOperatingCurrency: defaultRegistry missing operating_currency")
	}
}

func TestRegistryRegisterDuplicateKeyErrors(t *testing.T) {
	r := newRegistry()
	if err := r.register(spec{key: "x", kind: kindString, parse: parseStringOption}); err != nil {
		t.Fatalf("TestRegistryRegisterDuplicateKeyErrors: first register: %v", err)
	}
	if err := r.register(spec{key: "x", kind: kindString, parse: parseStringOption}); err == nil {
		t.Errorf("TestRegistryRegisterDuplicateKeyErrors: second register: err = nil, want non-nil")
	}
}
