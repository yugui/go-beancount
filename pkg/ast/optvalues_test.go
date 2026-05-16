package ast

import (
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

// testRegistry constructs a registry exercising all four kinds so unit tests
// do not depend on which specs are in defaultRegistry.
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
	if err := r.register(spec{key: "x", kind: kindString, parse: parseStringOption}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.register(spec{key: "x", kind: kindString, parse: parseStringOption}); err == nil {
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
		kind:         kindStringList,
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
