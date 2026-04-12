package validation

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

// testRegistry constructs an optionRegistry that exercises all four kinds so
// unit tests do not depend on which specs are wired into defaultOptionRegistry.
func testRegistry() *optionRegistry {
	r := &optionRegistry{specs: make(map[string]optionSpec)}
	r.register(optionSpec{
		key:          "title",
		kind:         optionKindString,
		parse:        parseStringOption,
		defaultValue: "default title",
	})
	r.register(optionSpec{
		key:          "infer_from_cost",
		kind:         optionKindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	})
	def := apd.New(1, -1) // 0.1
	r.register(optionSpec{
		key:          "tolerance_multiplier",
		kind:         optionKindDecimal,
		parse:        parseDecimalOption,
		defaultValue: def,
	})
	r.register(optionSpec{
		key:          "operating_currency",
		kind:         optionKindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	})
	return r
}

func TestOptionRegistryDefaults(t *testing.T) {
	ov := newOptionValues(testRegistry())
	if got := ov.String("title"); got != "default title" {
		t.Errorf("TestOptionRegistryDefaults: String default = %q, want %q", got, "default title")
	}
	if got := ov.Bool("infer_from_cost"); got != false {
		t.Errorf("TestOptionRegistryDefaults: Bool default = %v, want false", got)
	}
	d := ov.Decimal("tolerance_multiplier")
	if d == nil {
		t.Fatalf("TestOptionRegistryDefaults: Decimal default = nil")
	}
	if s := d.String(); s != "0.1" {
		t.Errorf("TestOptionRegistryDefaults: Decimal default = %q, want %q", s, "0.1")
	}
	if got := ov.StringList("operating_currency"); got != nil {
		t.Errorf("TestOptionRegistryDefaults: StringList default = %v, want nil", got)
	}
}

func TestOptionRegistryParseSuccess(t *testing.T) {
	ov := newOptionValues(testRegistry())
	if err := ov.set("title", "My Ledger"); err != nil {
		t.Fatalf("TestOptionRegistryParseSuccess: set title: %v", err)
	}
	if err := ov.set("infer_from_cost", "TRUE"); err != nil {
		t.Fatalf("TestOptionRegistryParseSuccess: set infer_from_cost: %v", err)
	}
	if err := ov.set("tolerance_multiplier", "0.5"); err != nil {
		t.Fatalf("TestOptionRegistryParseSuccess: set tolerance_multiplier: %v", err)
	}
	if err := ov.set("operating_currency", "USD"); err != nil {
		t.Fatalf("TestOptionRegistryParseSuccess: set operating_currency: %v", err)
	}

	if got := ov.String("title"); got != "My Ledger" {
		t.Errorf("TestOptionRegistryParseSuccess: String = %q, want %q", got, "My Ledger")
	}
	if got := ov.Bool("infer_from_cost"); got != true {
		t.Errorf("TestOptionRegistryParseSuccess: Bool = %v, want true", got)
	}
	d := ov.Decimal("tolerance_multiplier")
	if d.String() != "0.5" {
		t.Errorf("TestOptionRegistryParseSuccess: Decimal = %q, want %q", d.String(), "0.5")
	}
	// Mutating the returned decimal must not affect the stored value.
	d.SetInt64(999)
	d2 := ov.Decimal("tolerance_multiplier")
	if d2.String() != "0.5" {
		t.Errorf("TestOptionRegistryParseSuccess: Decimal after mutation = %q, want %q", d2.String(), "0.5")
	}
	got := ov.StringList("operating_currency")
	want := []string{"USD"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestOptionRegistryParseSuccess: StringList mismatch (-want +got):\n%s", diff)
	}
}

func TestOptionRegistryParseError(t *testing.T) {
	ov := newOptionValues(testRegistry())
	if err := ov.set("tolerance_multiplier", "not-a-number"); err == nil {
		t.Errorf("TestOptionRegistryParseError: set bad decimal: err = nil, want non-nil")
	}
	if err := ov.set("infer_from_cost", "maybe"); err == nil {
		t.Errorf("TestOptionRegistryParseError: set bad bool: err = nil, want non-nil")
	}
	if err := ov.set("operating_currency", "   "); err == nil {
		t.Errorf("TestOptionRegistryParseError: set empty currency: err = nil, want non-nil")
	}
}

func TestOptionRegistryDuplicateScalarOverwrites(t *testing.T) {
	ov := newOptionValues(testRegistry())
	if err := ov.set("title", "first"); err != nil {
		t.Fatal(err)
	}
	if err := ov.set("title", "second"); err != nil {
		t.Fatal(err)
	}
	if got := ov.String("title"); got != "second" {
		t.Errorf("TestOptionRegistryDuplicateScalarOverwrites: String = %q, want %q", got, "second")
	}
}

func TestOptionRegistryStringListAppends(t *testing.T) {
	ov := newOptionValues(testRegistry())
	for _, c := range []string{"USD", "JPY", "EUR"} {
		if err := ov.set("operating_currency", c); err != nil {
			t.Fatalf("TestOptionRegistryStringListAppends: set %q: %v", c, err)
		}
	}
	got := ov.StringList("operating_currency")
	want := []string{"USD", "JPY", "EUR"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestOptionRegistryStringListAppends: StringList mismatch (-want +got):\n%s", diff)
	}
}

func TestOptionRegistryUnknownKeyIgnored(t *testing.T) {
	ov := newOptionValues(testRegistry())
	if err := ov.set("no_such_option", "whatever"); err != nil {
		t.Errorf("TestOptionRegistryUnknownKeyIgnored: set unknown: err = %v, want nil", err)
	}
	if _, ok := ov.values["no_such_option"]; ok {
		t.Errorf("TestOptionRegistryUnknownKeyIgnored: unknown key was stored")
	}
}

func TestDefaultOptionRegistryHasOperatingCurrency(t *testing.T) {
	if _, ok := defaultOptionRegistry.specs["operating_currency"]; !ok {
		t.Errorf("TestDefaultOptionRegistryHasOperatingCurrency: defaultOptionRegistry missing operating_currency")
	}
}
