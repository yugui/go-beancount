package ast

import (
	"sort"
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

// testRegistry constructs a registry exercising all seven kinds so unit tests
// do not depend on which specs are in defaultRegistry.
func testRegistry(t *testing.T) *registry {
	t.Helper()
	r := newRegistry()
	if err := r.register(spec{
		key:          "title",
		kind:         KindString,
		parse:        parseStringOption,
		defaultValue: "default title",
	}); err != nil {
		t.Fatalf("testRegistry: register title: %v", err)
	}
	if err := r.register(spec{
		key:          "infer_from_cost",
		kind:         KindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}); err != nil {
		t.Fatalf("testRegistry: register infer_from_cost: %v", err)
	}
	def := apd.New(1, -1) // 0.1
	if err := r.register(spec{
		key:          "tolerance_multiplier",
		kind:         KindDecimal,
		parse:        parseDecimalOption,
		defaultValue: def,
	}); err != nil {
		t.Fatalf("testRegistry: register tolerance_multiplier: %v", err)
	}
	if err := r.register(spec{
		key:          "operating_currency",
		kind:         KindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	}); err != nil {
		t.Fatalf("testRegistry: register operating_currency: %v", err)
	}
	if err := r.register(spec{
		key:          "max_lines",
		kind:         KindInt,
		parse:        parseIntOption,
		defaultValue: 64,
	}); err != nil {
		t.Fatalf("testRegistry: register max_lines: %v", err)
	}
	if err := r.register(spec{
		key:          "tolerance_default",
		kind:         KindDecimalMap,
		parse:        parseDecimalMapEntry,
		defaultValue: map[string]*apd.Decimal(nil),
	}); err != nil {
		t.Fatalf("testRegistry: register tolerance_default: %v", err)
	}
	if err := r.register(spec{
		key:          "display_precision",
		kind:         KindIntMap,
		parse:        parseIntMapEntry,
		defaultValue: map[string]int(nil),
	}); err != nil {
		t.Fatalf("testRegistry: register display_precision: %v", err)
	}
	return r
}

func TestOptionValuesDefaults(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if got := v.String("title"); got != "default title" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "default title")
	}
	if got := v.Bool("infer_from_cost"); got != false {
		t.Errorf("v.Bool(%q) = %v, want false", "infer_from_cost", got)
	}
	d := v.Decimal("tolerance_multiplier")
	if d == nil {
		t.Fatalf("v.Decimal(%q) = nil", "tolerance_multiplier")
	}
	if s := d.String(); s != "0.1" {
		t.Errorf("v.Decimal(%q) = %q, want %q", "tolerance_multiplier", s, "0.1")
	}
	if got := v.StringList("operating_currency"); got != nil {
		t.Errorf("v.StringList(%q) = %v, want nil", "operating_currency", got)
	}
}

func TestOptionValuesParseSuccess(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("title", "My Ledger"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	if err := v.set("infer_from_cost", "TRUE"); err != nil {
		t.Fatalf("set infer_from_cost: %v", err)
	}
	if err := v.set("tolerance_multiplier", "0.5"); err != nil {
		t.Fatalf("set tolerance_multiplier: %v", err)
	}
	if err := v.set("operating_currency", "USD"); err != nil {
		t.Fatalf("set operating_currency: %v", err)
	}

	if got := v.String("title"); got != "My Ledger" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "My Ledger")
	}
	if got := v.Bool("infer_from_cost"); got != true {
		t.Errorf("v.Bool(%q) = %v, want true", "infer_from_cost", got)
	}
	d := v.Decimal("tolerance_multiplier")
	if d.String() != "0.5" {
		t.Errorf("v.Decimal(%q) = %q, want %q", "tolerance_multiplier", d.String(), "0.5")
	}
	d.SetInt64(999)
	d2 := v.Decimal("tolerance_multiplier")
	if d2.String() != "0.5" {
		t.Errorf("v.Decimal(%q) after mutation = %q, want %q", "tolerance_multiplier", d2.String(), "0.5")
	}
	got := v.StringList("operating_currency")
	want := []string{"USD"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("v.StringList(%q) mismatch (-want +got):\n%s", "operating_currency", diff)
	}
	got[0] = "MUTATED"
	got2 := v.StringList("operating_currency")
	if diff := cmp.Diff([]string{"USD"}, got2); diff != "" {
		t.Errorf("v.StringList(%q) after mutation mismatch (-want +got):\n%s", "operating_currency", diff)
	}
}

func TestOptionValuesParseError(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("tolerance_multiplier", "not-a-number"); err == nil {
		t.Errorf("set bad decimal: err = nil, want non-nil")
	}
	if err := v.set("infer_from_cost", "maybe"); err == nil {
		t.Errorf("set bad bool: err = nil, want non-nil")
	}
	if err := v.set("operating_currency", "   "); err == nil {
		t.Errorf("set empty currency: err = nil, want non-nil")
	}
}

func TestOptionValuesDuplicateScalarOverwrites(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("title", "first"); err != nil {
		t.Fatal(err)
	}
	if err := v.set("title", "second"); err != nil {
		t.Fatal(err)
	}
	if got := v.String("title"); got != "second" {
		t.Errorf("String = %q, want %q", got, "second")
	}
}

func TestOptionValuesStringListAppends(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	for _, c := range []string{"USD", "JPY", "EUR"} {
		if err := v.set("operating_currency", c); err != nil {
			t.Fatalf("set %q: %v", c, err)
		}
	}
	got := v.StringList("operating_currency")
	want := []string{"USD", "JPY", "EUR"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("StringList mismatch (-want +got):\n%s", diff)
	}
}

func TestOptionValuesUnknownKeyIgnored(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("no_such_option", "whatever"); err != nil {
		t.Errorf("set unknown: err = %v, want nil", err)
	}
	if got := v.String("title"); got != "default title" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "default title")
	}
}

func TestOptionValuesRegisterDuplicateKeyErrors(t *testing.T) {
	r := newRegistry()
	if err := r.register(spec{key: "x", kind: KindString, parse: parseStringOption}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.register(spec{key: "x", kind: KindString, parse: parseStringOption}); err == nil {
		t.Errorf("second register: err = nil, want non-nil")
	}
}

func TestDefaultRegistryKeys(t *testing.T) {
	v := NewOptionValues()
	// Each accessor call exercises the registered kind: a missing key
	// would panic inside lookupSpec, so reaching the assertion proves
	// registration.
	if got := v.StringList("operating_currency"); got != nil {
		t.Errorf("v.StringList(%q) = %v, want nil", "operating_currency", got)
	}
	d := v.Decimal("inferred_tolerance_multiplier")
	if d == nil || d.String() != "0.5" {
		t.Errorf("v.Decimal(%q) = %v, want 0.5", "inferred_tolerance_multiplier", d)
	}
	if got := v.Bool("infer_tolerance_from_cost"); got != false {
		t.Errorf("v.Bool(%q) = %v, want false", "infer_tolerance_from_cost", got)
	}
	if got := v.String("plugin_processing_mode"); got != "" {
		t.Errorf("v.String(%q) = %q, want %q", "plugin_processing_mode", got, "")
	}
	if got := v.String("title"); got != "" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "")
	}
}

func TestOptionValuesNilSafeAccessors(t *testing.T) {
	var v *OptionValues

	if got := v.String("title"); got != "" {
		t.Errorf("v.String(%q) on nil = %q, want %q", "title", got, "")
	}
	if got := v.String("plugin_processing_mode"); got != "" {
		t.Errorf("v.String(%q) on nil = %q, want %q", "plugin_processing_mode", got, "")
	}
	if got := v.Bool("infer_tolerance_from_cost"); got != false {
		t.Errorf("v.Bool(%q) on nil = %v, want false", "infer_tolerance_from_cost", got)
	}
	d := v.Decimal("inferred_tolerance_multiplier")
	if d == nil || d.String() != "0.5" {
		t.Errorf("v.Decimal(%q) on nil = %v, want 0.5", "inferred_tolerance_multiplier", d)
	}
	if got := v.StringList("operating_currency"); got != nil {
		t.Errorf("v.StringList(%q) on nil = %v, want nil", "operating_currency", got)
	}
}

func TestParseNilLedger(t *testing.T) {
	v, errs := ParseOptions(nil)
	if v == nil {
		t.Fatalf("ParseOptions(nil) returned nil *OptionValues")
	}
	if len(errs) != 0 {
		t.Errorf("ParseOptions(nil) errs = %v, want none", errs)
	}
	if got := v.StringList("operating_currency"); got != nil {
		t.Errorf("v.StringList(%q) = %v, want nil", "operating_currency", got)
	}
	if got := v.Bool("infer_tolerance_from_cost"); got != false {
		t.Errorf("v.Bool(%q) = %v, want false", "infer_tolerance_from_cost", got)
	}
	d := v.Decimal("inferred_tolerance_multiplier")
	if d == nil || d.String() != "0.5" {
		t.Errorf("v.Decimal(%q) = %v, want 0.5", "inferred_tolerance_multiplier", d)
	}
}

func TestParseLedger(t *testing.T) {
	l := &Ledger{}
	l.InsertAll([]Directive{
		&Option{Key: "title", Value: "My Ledger"},
		&Option{Key: "operating_currency", Value: "USD"},
		&Option{Key: "operating_currency", Value: "JPY"},
		&Option{Key: "infer_tolerance_from_cost", Value: "TRUE"},
	})

	v, errs := ParseOptions(l)
	if len(errs) != 0 {
		t.Fatalf("ParseOptions errs = %v, want none", errs)
	}
	if got := v.String("title"); got != "My Ledger" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "My Ledger")
	}
	if got := v.Bool("infer_tolerance_from_cost"); got != true {
		t.Errorf("v.Bool(%q) = %v, want true", "infer_tolerance_from_cost", got)
	}
	if diff := cmp.Diff([]string{"USD", "JPY"}, v.StringList("operating_currency")); diff != "" {
		t.Errorf("operating_currency mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLedgerMalformedOption(t *testing.T) {
	span := Span{Start: Position{Filename: "test.bean", Line: 5}}
	l := &Ledger{}
	l.InsertAll([]Directive{
		&Option{Key: "inferred_tolerance_multiplier", Value: "not-a-number", Span: span},
	})

	v, diags := ParseOptions(l)
	if v == nil {
		t.Fatalf("ParseOptions returned nil *OptionValues")
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1 (diags=%v)", len(diags), diags)
	}
	got := diags[0]
	want := Diagnostic{
		Code:     invalidOptionCode,
		Span:     span,
		Severity: Error,
	}
	if diff := cmp.Diff(want, got, cmp.FilterPath(func(p cmp.Path) bool {
		return p.Last().String() == ".Message"
	}, cmp.Ignore())); diff != "" {
		t.Errorf("Diagnostic mismatch (-want +got):\n%s", diff)
	}
	wantMsgPrefix := `invalid option "inferred_tolerance_multiplier":`
	if !strings.HasPrefix(got.Message, wantMsgPrefix) {
		t.Errorf("Message = %q, want prefix %q", got.Message, wantMsgPrefix)
	}
}

func TestParseLedgerUnknownKeyIgnored(t *testing.T) {
	l := &Ledger{}
	l.InsertAll([]Directive{
		&Option{Key: "no_such_option", Value: "whatever"},
	})

	v, errs := ParseOptions(l)
	if v == nil {
		t.Fatalf("ParseOptions returned nil *OptionValues")
	}
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none", errs)
	}
}

func TestParseLedgerLastWinsScalar(t *testing.T) {
	l := &Ledger{}
	l.InsertAll([]Directive{
		&Option{Key: "title", Value: "first"},
		&Option{Key: "title", Value: "second"},
		&Option{Key: "title", Value: "third"},
	})

	v, errs := ParseOptions(l)
	if len(errs) != 0 {
		t.Fatalf("ParseOptions errs = %v, want none", errs)
	}
	if got := v.String("title"); got != "third" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "third")
	}
}

// TestParseLedgerMixedValidAndInvalid verifies that a malformed option does not
// suppress valid ones, and only the malformed directive surfaces in errs.
func TestParseLedgerMixedValidAndInvalid(t *testing.T) {
	l := &Ledger{}
	l.InsertAll([]Directive{
		&Option{Key: "operating_currency", Value: "USD"},
		&Option{Key: "inferred_tolerance_multiplier", Value: "not-a-number"},
		&Option{Key: "operating_currency", Value: "JPY"},
	})

	v, diags := ParseOptions(l)
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1 (diags=%v)", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "inferred_tolerance_multiplier") {
		t.Errorf("diags[0].Message = %q, want it to mention %q", diags[0].Message, "inferred_tolerance_multiplier")
	}
	if diff := cmp.Diff([]string{"USD", "JPY"}, v.StringList("operating_currency")); diff != "" {
		t.Errorf("operating_currency mismatch (-want +got):\n%s", diff)
	}
	// Malformed decimal must leave the registry default intact.
	d := v.Decimal("inferred_tolerance_multiplier")
	if d == nil || d.String() != "0.5" {
		t.Errorf("v.Decimal(%q) = %v, want 0.5", "inferred_tolerance_multiplier", d)
	}
}

// TestParseLedgerSkipsNonOptionDirectives verifies that non-Option directives
// are ignored by ParseOptions.
func TestParseLedgerSkipsNonOptionDirectives(t *testing.T) {
	l := &Ledger{}
	l.InsertAll([]Directive{
		&Plugin{Name: "beancount.plugins.auto"},
		&Option{Key: "title", Value: "Mixed"},
		&Plugin{Name: "beancount.plugins.auto_accounts"},
		&Option{Key: "operating_currency", Value: "USD"},
	})

	v, errs := ParseOptions(l)
	if len(errs) != 0 {
		t.Fatalf("ParseOptions errs = %v, want none", errs)
	}
	if got := v.String("title"); got != "Mixed" {
		t.Errorf("v.String(%q) = %q, want %q", "title", got, "Mixed")
	}
	if diff := cmp.Diff([]string{"USD"}, v.StringList("operating_currency")); diff != "" {
		t.Errorf("operating_currency mismatch (-want +got):\n%s", diff)
	}
}

// TestOptionValuesDefaultDecimalCloneIsolation verifies that mutating a
// returned Decimal default does not affect subsequent retrievals.
func TestOptionValuesDefaultDecimalCloneIsolation(t *testing.T) {
	v := NewOptionValues()
	d := v.Decimal("inferred_tolerance_multiplier")
	if d == nil || d.String() != "0.5" {
		t.Fatalf("initial Decimal default = %v, want 0.5", d)
	}
	d.SetInt64(999)
	d2 := v.Decimal("inferred_tolerance_multiplier")
	if d2 == nil || d2.String() != "0.5" {
		t.Errorf("Decimal default after mutation = %v, want 0.5", d2)
	}
}

// TestOptionValuesDefaultStringListCloneIsolation verifies that mutating a
// returned StringList default does not affect subsequent retrievals.
// Uses a custom registry because defaultRegistry's operating_currency default is nil.
func TestOptionValuesDefaultStringListCloneIsolation(t *testing.T) {
	r := newRegistry()
	if err := r.register(spec{
		key:          "currencies",
		kind:         KindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string{"USD", "JPY"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	v := newOptionValues(r)
	got := v.StringList("currencies")
	if diff := cmp.Diff([]string{"USD", "JPY"}, got); diff != "" {
		t.Fatalf("initial default mismatch (-want +got):\n%s", diff)
	}
	got[0] = "MUTATED"
	got2 := v.StringList("currencies")
	if diff := cmp.Diff([]string{"USD", "JPY"}, got2); diff != "" {
		t.Errorf("default after mutation mismatch (-want +got):\n%s", diff)
	}
}

func TestOptionValuesIntParseSuccess(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("max_lines", "128"); err != nil {
		t.Fatalf("set max_lines: %v", err)
	}
	if got := v.Int("max_lines"); got != 128 {
		t.Errorf("Int(%q) = %d, want 128", "max_lines", got)
	}
}

func TestOptionValuesIntParseError(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("max_lines", "not-an-int"); err == nil {
		t.Errorf("set bad int: err = nil, want non-nil")
	}
	if got := v.Int("max_lines"); got != 64 {
		t.Errorf("Int(%q) after error = %d, want 64 (default)", "max_lines", got)
	}
}

func TestOptionValuesIntDefault(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if got := v.Int("max_lines"); got != 64 {
		t.Errorf("Int(%q) default = %d, want 64", "max_lines", got)
	}
}

func TestOptionValuesDecimalMapParseSuccess(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("tolerance_default", "USD:0.005"); err != nil {
		t.Fatalf("set tolerance_default: %v", err)
	}
	got := v.DecimalMap("tolerance_default")
	if len(got) != 1 {
		t.Fatalf("DecimalMap len = %d, want 1", len(got))
	}
	d := got["USD"]
	if d == nil || d.String() != "0.005" {
		t.Errorf("DecimalMap[%q] = %v, want 0.005", "USD", d)
	}
}

func TestOptionValuesDecimalMapParseErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing separator", "USD0.005"},
		{"empty key", ":0.005"},
		{"bad decimal", "USD:not-a-decimal"},
		{"empty value", "USD:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newOptionValues(testRegistry(t))
			if err := v.set("tolerance_default", tc.raw); err == nil {
				t.Errorf("set %q: err = nil, want non-nil", tc.raw)
			}
		})
	}
}

func TestOptionValuesDecimalMapAccumulation(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	entries := []string{"USD:0.01", "JPY:1", "USD:0.005"}
	for _, e := range entries {
		if err := v.set("tolerance_default", e); err != nil {
			t.Fatalf("set %q: %v", e, err)
		}
	}
	got := v.DecimalMap("tolerance_default")
	if d := got["USD"]; d == nil || d.String() != "0.005" {
		t.Errorf("USD = %v, want 0.005", d)
	}
	if d := got["JPY"]; d == nil || d.String() != "1" {
		t.Errorf("JPY = %v, want 1", d)
	}
}

func TestOptionValuesDecimalMapCloneIsolation(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("tolerance_default", "USD:0.01"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := v.DecimalMap("tolerance_default")
	got["EUR"] = apd.New(1, -2)
	got2 := v.DecimalMap("tolerance_default")
	if _, ok := got2["EUR"]; ok {
		t.Errorf("DecimalMap(%q): caller mutation of returned map leaked into stored state", "tolerance_default")
	}
	// Mutating returned Decimal should not affect next read.
	got3 := v.DecimalMap("tolerance_default")
	got3["USD"].SetInt64(999)
	got4 := v.DecimalMap("tolerance_default")
	if d := got4["USD"]; d == nil || d.String() != "0.01" {
		t.Errorf("DecimalMap(%q)[%q] after decimal mutation = %v, want 0.01", "tolerance_default", "USD", d)
	}
}

func TestOptionValuesIntMapParseSuccess(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("display_precision", "USD:2"); err != nil {
		t.Fatalf("set display_precision: %v", err)
	}
	got := v.IntMap("display_precision")
	if got["USD"] != 2 {
		t.Errorf("IntMap[%q] = %d, want 2", "USD", got["USD"])
	}
}

func TestOptionValuesIntMapParseErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing separator", "USD2"},
		{"empty key", ":2"},
		{"bad integer", "USD:not-an-int"},
		{"empty value", "USD:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newOptionValues(testRegistry(t))
			if err := v.set("display_precision", tc.raw); err == nil {
				t.Errorf("set %q: err = nil, want non-nil", tc.raw)
			}
		})
	}
}

func TestOptionValuesIntMapAccumulation(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	entries := []string{"USD:2", "JPY:0", "USD:4"}
	for _, e := range entries {
		if err := v.set("display_precision", e); err != nil {
			t.Fatalf("set %q: %v", e, err)
		}
	}
	got := v.IntMap("display_precision")
	if got["USD"] != 4 {
		t.Errorf("USD = %d, want 4 (last-write-wins)", got["USD"])
	}
	if v, ok := got["JPY"]; !ok {
		t.Errorf("JPY missing from IntMap, want 0")
	} else if v != 0 {
		t.Errorf("JPY = %d, want 0", v)
	}
}

func TestOptionValuesIntMapCloneIsolation(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	if err := v.set("display_precision", "USD:2"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := v.IntMap("display_precision")
	got["EUR"] = 3
	got2 := v.IntMap("display_precision")
	if _, ok := got2["EUR"]; ok {
		t.Errorf("IntMap(%q): caller mutation of returned map leaked into stored state", "display_precision")
	}
}

func TestOptionValuesMapDefault(t *testing.T) {
	v := newOptionValues(testRegistry(t))
	dm := v.DecimalMap("tolerance_default")
	if dm == nil {
		t.Errorf("DecimalMap default = nil, want non-nil empty map")
	}
	if len(dm) != 0 {
		t.Errorf("DecimalMap default len = %d, want 0", len(dm))
	}
	im := v.IntMap("display_precision")
	if im == nil {
		t.Errorf("IntMap default = nil, want non-nil empty map")
	}
	if len(im) != 0 {
		t.Errorf("IntMap default len = %d, want 0", len(im))
	}
}

// TestSnapshotOrderAndKinds verifies that Snapshot returns every registered
// key in ascending order, with correct Kind, correct values, and that map
// mutation does not affect a subsequent Snapshot call.
func TestSnapshotOrderAndKinds(t *testing.T) {
	reg := testRegistry(t)
	v := newOptionValues(reg)

	// Set values for each kind.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(v.set("title", "My Ledger"))
	must(v.set("infer_from_cost", "TRUE"))
	must(v.set("tolerance_multiplier", "0.5"))
	must(v.set("operating_currency", "USD"))
	must(v.set("operating_currency", "JPY"))
	must(v.set("max_lines", "100"))
	must(v.set("tolerance_default", "USD:0.01"))
	must(v.set("display_precision", "USD:2"))

	entries := v.Snapshot()

	// Keys must be in ascending order.
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("Snapshot keys not sorted: %v", keys)
	}

	// Every registered key must appear exactly once.
	if len(entries) != len(reg.specs) {
		t.Errorf("Snapshot len = %d, want %d", len(entries), len(reg.specs))
	}
	byKey := make(map[string]OptionEntry, len(entries))
	for _, e := range entries {
		if _, dup := byKey[e.Key]; dup {
			t.Errorf("duplicate key %q in Snapshot", e.Key)
		}
		byKey[e.Key] = e
	}

	// Kind checks.
	kindFor := map[string]OptionKind{
		"title":                KindString,
		"infer_from_cost":      KindBool,
		"tolerance_multiplier": KindDecimal,
		"operating_currency":   KindStringList,
		"max_lines":            KindInt,
		"tolerance_default":    KindDecimalMap,
		"display_precision":    KindIntMap,
	}
	for key, want := range kindFor {
		e, ok := byKey[key]
		if !ok {
			t.Errorf("key %q missing from Snapshot", key)
			continue
		}
		if e.Kind != want {
			t.Errorf("key %q Kind = %v, want %v", key, e.Kind, want)
		}
	}

	// Value checks.
	if e := byKey["title"]; e.String() != "My Ledger" {
		t.Errorf("title String() = %q, want %q", e.String(), "My Ledger")
	}
	if e := byKey["infer_from_cost"]; !e.Bool() {
		t.Errorf("infer_from_cost Bool() = false, want true")
	}
	if d := byKey["tolerance_multiplier"].Decimal(); d == nil || d.String() != "0.5" {
		t.Errorf("tolerance_multiplier Decimal() = %v, want 0.5", d)
	}
	if diff := cmp.Diff([]string{"USD", "JPY"}, byKey["operating_currency"].StringList()); diff != "" {
		t.Errorf("operating_currency StringList mismatch (-want +got):\n%s", diff)
	}
	if e := byKey["max_lines"]; e.Int() != 100 {
		t.Errorf("max_lines Int() = %d, want 100", e.Int())
	}
	if dm := byKey["tolerance_default"].DecimalMap(); dm["USD"] == nil || dm["USD"].String() != "0.01" {
		t.Errorf("tolerance_default DecimalMap[USD] = %v, want 0.01", dm["USD"])
	}
	if im := byKey["display_precision"].IntMap(); im["USD"] != 2 {
		t.Errorf("display_precision IntMap[USD] = %d, want 2", im["USD"])
	}

	// Wrong-kind accessors return zero values.
	strEntry := byKey["title"]
	if strEntry.Bool() {
		t.Errorf("title Bool() on KindString = true, want false")
	}
	if strEntry.Decimal() != nil {
		t.Errorf("title Decimal() on KindString = non-nil, want nil")
	}
	if strEntry.StringList() != nil {
		t.Errorf("title StringList() on KindString = non-nil, want nil")
	}
	if strEntry.Int() != 0 {
		t.Errorf("title Int() on KindString = %d, want 0", strEntry.Int())
	}
	if m := strEntry.DecimalMap(); m == nil || len(m) != 0 {
		t.Errorf("title DecimalMap() on KindString = %v, want non-nil empty map", m)
	}
	if m := strEntry.IntMap(); m == nil || len(m) != 0 {
		t.Errorf("title IntMap() on KindString = %v, want non-nil empty map", m)
	}
	// KindBool: String() returns "".
	boolEntry := byKey["infer_from_cost"]
	if boolEntry.String() != "" {
		t.Errorf("infer_from_cost String() on KindBool = %q, want %q", boolEntry.String(), "")
	}

	// Map mutation does not affect next Snapshot.
	dm := byKey["tolerance_default"].DecimalMap()
	dm["EUR"] = apd.New(1, -2)
	entries2 := v.Snapshot()
	byKey2 := make(map[string]OptionEntry, len(entries2))
	for _, e := range entries2 {
		byKey2[e.Key] = e
	}
	e2, ok2 := byKey2["tolerance_default"]
	if !ok2 {
		t.Errorf("tolerance_default missing from second Snapshot")
	} else if _, leaked := e2.DecimalMap()["EUR"]; leaked {
		t.Errorf("DecimalMap(%q): map mutation from first Snapshot leaked into second Snapshot", "tolerance_default")
	}
	im := byKey["display_precision"].IntMap()
	im["EUR"] = 3
	entries3 := v.Snapshot()
	byKey3 := make(map[string]OptionEntry, len(entries3))
	for _, e := range entries3 {
		byKey3[e.Key] = e
	}
	e3, ok3 := byKey3["display_precision"]
	if !ok3 {
		t.Errorf("display_precision missing from third Snapshot")
	} else if _, leaked := e3.IntMap()["EUR"]; leaked {
		t.Errorf("IntMap(%q): map mutation from first Snapshot leaked into third Snapshot", "display_precision")
	}
}

// TestSnapshotDecimalNilDefault verifies that Snapshot on a KindDecimal spec
// whose default is nil does not panic and returns nil from Decimal().
func TestSnapshotDecimalNilDefault(t *testing.T) {
	r := newRegistry()
	if err := r.register(spec{
		key:          "account_rounding",
		kind:         KindDecimal,
		parse:        parseDecimalOption,
		defaultValue: (*apd.Decimal)(nil),
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	v := newOptionValues(r)
	entries := v.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("Snapshot len = %d, want 1", len(entries))
	}
	if d := entries[0].Decimal(); d != nil {
		t.Errorf("Decimal() = %v, want nil for nil-default spec", d)
	}
}

// TestSnapshotNilReceiver verifies that Snapshot on a nil *OptionValues
// returns default-registry entries for every registered key.
func TestSnapshotNilReceiver(t *testing.T) {
	var v *OptionValues
	entries := v.Snapshot()
	if len(entries) == 0 {
		t.Fatalf("Snapshot on nil returned 0 entries, want default registry count")
	}
	// All keys in the default registry must appear.
	byKey := make(map[string]OptionEntry, len(entries))
	for _, e := range entries {
		byKey[e.Key] = e
	}
	defaultKeys := []string{
		"operating_currency",
		"inferred_tolerance_multiplier",
		"infer_tolerance_from_cost",
		"plugin_processing_mode",
		"title",
	}
	for _, key := range defaultKeys {
		if _, ok := byKey[key]; !ok {
			t.Errorf("default-registry key %q missing from nil Snapshot", key)
		}
	}
	// Keys must be sorted.
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("nil Snapshot keys not sorted: %v", keys)
	}
}
