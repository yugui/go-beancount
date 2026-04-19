package options

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
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

// buildRawLedger constructs an ast.Ledger containing the given directives
// in canonical order for BuildRaw tests.
func buildRawLedger(ds ...ast.Directive) *ast.Ledger {
	l := &ast.Ledger{}
	l.InsertAll(ds)
	return l
}

func TestBuildRawNilLedger(t *testing.T) {
	if got := BuildRaw(nil); got != nil {
		t.Errorf("TestBuildRawNilLedger: BuildRaw(nil) = %v, want nil", got)
	}
}

func TestBuildRawEmptyLedgerReturnsNil(t *testing.T) {
	l := buildRawLedger()
	if got := BuildRaw(l); got != nil {
		t.Errorf("TestBuildRawEmptyLedgerReturnsNil: BuildRaw = %v, want nil", got)
	}
}

func TestBuildRawNoOptionDirectivesReturnsNil(t *testing.T) {
	l := buildRawLedger(&ast.Plugin{Name: "example.com/fake"})
	if got := BuildRaw(l); got != nil {
		t.Errorf("TestBuildRawNoOptionDirectivesReturnsNil: BuildRaw = %v, want nil", got)
	}
}

func TestBuildRawSingleOption(t *testing.T) {
	l := buildRawLedger(&ast.Option{Key: "title", Value: "My Ledger"})
	got := BuildRaw(l)
	want := map[string]string{"title": "My Ledger"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestBuildRawSingleOption: BuildRaw mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildRawDuplicateKeyLastWins(t *testing.T) {
	l := buildRawLedger(
		&ast.Option{Key: "title", Value: "first"},
		&ast.Option{Key: "title", Value: "second"},
		&ast.Option{Key: "title", Value: "third"},
	)
	got := BuildRaw(l)
	want := map[string]string{"title": "third"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestBuildRawDuplicateKeyLastWins: BuildRaw mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildRawMixedKeysPreserved(t *testing.T) {
	l := buildRawLedger(
		&ast.Option{Key: "title", Value: "My Ledger"},
		&ast.Option{Key: "operating_currency", Value: "USD"},
		&ast.Option{Key: "operating_currency", Value: "JPY"},
		&ast.Option{Key: "infer_from_cost", Value: "TRUE"},
	)
	got := BuildRaw(l)
	// operating_currency uses last-wins here (raw map semantics), not
	// StringList accumulation.
	want := map[string]string{
		"title":              "My Ledger",
		"operating_currency": "JPY",
		"infer_from_cost":    "TRUE",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestBuildRawMixedKeysPreserved: BuildRaw mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildRawIgnoresNonOptionDirectives(t *testing.T) {
	l := buildRawLedger(
		&ast.Plugin{Name: "example.com/fake"},
		&ast.Option{Key: "title", Value: "My Ledger"},
		&ast.Plugin{Name: "example.com/other"},
	)
	got := BuildRaw(l)
	want := map[string]string{"title": "My Ledger"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestBuildRawIgnoresNonOptionDirectives: BuildRaw mismatch (-want +got):\n%s", diff)
	}
}

func TestFromRaw_EmptyMap(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   map[string]string
	}{
		{name: "nil", in: nil},
		{name: "empty", in: map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, errs := FromRaw(tc.in)
			if v == nil {
				t.Fatalf("TestFromRaw_EmptyMap[%s]: *Values = nil, want non-nil", tc.name)
			}
			if errs != nil {
				t.Errorf("TestFromRaw_EmptyMap[%s]: errs = %v, want nil", tc.name, errs)
			}
			// The returned *Values must expose the defaultRegistry's defaults.
			if got := v.StringList("operating_currency"); got != nil {
				t.Errorf("TestFromRaw_EmptyMap[%s]: operating_currency = %v, want nil", tc.name, got)
			}
			if got := v.Bool("infer_tolerance_from_cost"); got != false {
				t.Errorf("TestFromRaw_EmptyMap[%s]: infer_tolerance_from_cost = %v, want false", tc.name, got)
			}
			d := v.Decimal("inferred_tolerance_multiplier")
			if d == nil || d.String() != "0.5" {
				t.Errorf("TestFromRaw_EmptyMap[%s]: inferred_tolerance_multiplier = %v, want 0.5", tc.name, d)
			}
		})
	}
}

func TestFromRaw_ValidKeys(t *testing.T) {
	raw := map[string]string{
		"operating_currency":            "USD",
		"inferred_tolerance_multiplier": "0.25",
		"infer_tolerance_from_cost":     "TRUE",
	}
	v, errs := FromRaw(raw)
	if errs != nil {
		t.Fatalf("TestFromRaw_ValidKeys: errs = %v, want nil", errs)
	}
	if got := v.StringList("operating_currency"); !cmp.Equal(got, []string{"USD"}) {
		t.Errorf("TestFromRaw_ValidKeys: operating_currency = %v, want [USD]", got)
	}
	d := v.Decimal("inferred_tolerance_multiplier")
	if d == nil || d.String() != "0.25" {
		t.Errorf("TestFromRaw_ValidKeys: inferred_tolerance_multiplier = %v, want 0.25", d)
	}
	if got := v.Bool("infer_tolerance_from_cost"); got != true {
		t.Errorf("TestFromRaw_ValidKeys: infer_tolerance_from_cost = %v, want true", got)
	}
}

func TestFromRaw_MalformedValue(t *testing.T) {
	raw := map[string]string{
		"inferred_tolerance_multiplier": "not-a-number",
	}
	v, errs := FromRaw(raw)
	if v == nil {
		t.Fatalf("TestFromRaw_MalformedValue: *Values = nil, want non-nil")
	}
	if len(errs) != 1 {
		t.Fatalf("TestFromRaw_MalformedValue: len(errs) = %d, want 1 (errs=%v)", len(errs), errs)
	}
	got := errs[0]
	if got.Key != "inferred_tolerance_multiplier" {
		t.Errorf("TestFromRaw_MalformedValue: Key = %q, want %q", got.Key, "inferred_tolerance_multiplier")
	}
	if got.Value != "not-a-number" {
		t.Errorf("TestFromRaw_MalformedValue: Value = %q, want %q", got.Value, "not-a-number")
	}
	if got.Err == nil {
		t.Errorf("TestFromRaw_MalformedValue: Err = nil, want non-nil")
	}
	// FromRaw has no source locations, so Span must be the zero value.
	if got.Span != (ast.Span{}) {
		t.Errorf("TestFromRaw_MalformedValue: Span = %v, want zero", got.Span)
	}
	// The malformed key must not have been stored.
	if _, ok := v.values["inferred_tolerance_multiplier"]; ok {
		t.Errorf("TestFromRaw_MalformedValue: malformed value was stored")
	}
}

func TestFromRaw_UnknownKey(t *testing.T) {
	raw := map[string]string{
		"no_such_option": "whatever",
		"another_bogus":  "value",
	}
	v, errs := FromRaw(raw)
	if v == nil {
		t.Fatalf("TestFromRaw_UnknownKey: *Values = nil, want non-nil")
	}
	if errs != nil {
		t.Errorf("TestFromRaw_UnknownKey: errs = %v, want nil", errs)
	}
	if _, ok := v.values["no_such_option"]; ok {
		t.Errorf("TestFromRaw_UnknownKey: unknown key was stored")
	}
}

func TestFromRaw_EquivalentToParse(t *testing.T) {
	// Ledger with a duplicate scalar (infer_tolerance_from_cost) to exercise
	// last-wins, plus one occurrence of each other recognized key. We use a
	// single operating_currency value because BuildRaw collapses duplicate
	// list keys to last-wins, which would diverge from Parse's accumulation.
	l := buildRawLedger(
		&ast.Option{Key: "operating_currency", Value: "USD"},
		&ast.Option{Key: "inferred_tolerance_multiplier", Value: "0.75"},
		&ast.Option{Key: "infer_tolerance_from_cost", Value: "FALSE"},
		&ast.Option{Key: "infer_tolerance_from_cost", Value: "TRUE"},
	)

	fromParse, parseErrs := Parse(l)
	fromRaw, rawErrs := FromRaw(BuildRaw(l))

	if len(parseErrs) != 0 {
		t.Fatalf("TestFromRaw_EquivalentToParse: Parse errs = %v, want none", parseErrs)
	}
	if len(rawErrs) != 0 {
		t.Fatalf("TestFromRaw_EquivalentToParse: FromRaw errs = %v, want none", rawErrs)
	}

	// Compare field-by-field: both bind the same defaultRegistry, and their
	// stored maps should agree key-for-key. *apd.Decimal values compare by
	// reflect.DeepEqual only when their coefficients and exponents match, so
	// we compare decimals via their string representation.
	if fromParse.reg != fromRaw.reg {
		t.Errorf("TestFromRaw_EquivalentToParse: reg pointers differ")
	}
	if len(fromParse.values) != len(fromRaw.values) {
		t.Fatalf("TestFromRaw_EquivalentToParse: value count: Parse=%d FromRaw=%d", len(fromParse.values), len(fromRaw.values))
	}
	for k, pv := range fromParse.values {
		rv, ok := fromRaw.values[k]
		if !ok {
			t.Errorf("TestFromRaw_EquivalentToParse: key %q missing from FromRaw", k)
			continue
		}
		// *apd.Decimal doesn't compare cleanly with DeepEqual across
		// independent allocations, so compare by string form.
		if pd, isDec := pv.(*apd.Decimal); isDec {
			rd, ok := rv.(*apd.Decimal)
			if !ok {
				t.Errorf("TestFromRaw_EquivalentToParse: key %q: types differ (Parse=%T FromRaw=%T)", k, pv, rv)
				continue
			}
			if pd.String() != rd.String() {
				t.Errorf("TestFromRaw_EquivalentToParse: key %q: Parse=%s FromRaw=%s", k, pd.String(), rd.String())
			}
			continue
		}
		if diff := cmp.Diff(pv, rv); diff != "" {
			t.Errorf("TestFromRaw_EquivalentToParse: key %q mismatch (-Parse +FromRaw):\n%s", k, diff)
		}
	}
}
