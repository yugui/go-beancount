package ast

import (
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

const invalidOptionCode = "invalid-option"

// kind classifies how an option's raw value is parsed and stored.
type kind int

const (
	kindString kind = iota
	kindBool
	kindDecimal
	kindStringList
)

// spec describes one option's type, parser, and default.
type spec struct {
	key          string
	kind         kind
	parse        func(raw string) (any, error)
	defaultValue any
}

// registry holds all known option specs keyed by option name.
type registry struct {
	specs map[string]spec
}

func newRegistry() *registry {
	return &registry{specs: make(map[string]spec)}
}

func (r *registry) register(s spec) error {
	if _, ok := r.specs[s.key]; ok {
		return fmt.Errorf("options: key %q is already registered", s.key)
	}
	r.specs[s.key] = s
	return nil
}

// OptionValues holds parsed option values for a single ledger.
//
// Construct via [NewOptionValues] or [ParseOptions]; the zero value is not
// usable. All four accessor methods ([String], [Bool], [Decimal], [StringList])
// share the same contract: they return the registered default when a key has
// not been set, are nil-safe (a nil receiver consults the package's default
// registry), and panic if the key is not registered. [Decimal] and [StringList]
// return fresh copies; callers may mutate them without affecting stored state.
type OptionValues struct {
	reg    *registry
	values map[string]any
}

// NewOptionValues returns an *OptionValues bound to the default registry
// with no options set. Accessor methods return registry defaults.
func NewOptionValues() *OptionValues {
	return newOptionValues(defaultRegistry)
}

func newOptionValues(reg *registry) *OptionValues {
	return &OptionValues{reg: reg, values: make(map[string]any)}
}

// set silently ignores unknown keys; unknown is not an error.
// kindStringList items accumulate; scalar values use last-write-wins.
func (v *OptionValues) set(key, raw string) error {
	s, ok := v.reg.specs[key]
	if !ok {
		return nil
	}
	parsed, err := s.parse(raw)
	if err != nil {
		return err
	}
	if s.kind == kindStringList {
		str, ok := parsed.(string)
		if !ok {
			return fmt.Errorf("string list parser returned %T, want string", parsed)
		}
		existing, _ := v.values[key].([]string)
		v.values[key] = append(existing, str)
		return nil
	}
	v.values[key] = parsed
	return nil
}

func (v *OptionValues) lookupSpec(key string) spec {
	var reg *registry
	if v != nil {
		reg = v.reg
	} else {
		reg = defaultRegistry
	}
	s, ok := reg.specs[key]
	if !ok {
		panic(fmt.Sprintf("options: key %q is not registered", key))
	}
	return s
}

// String returns the string value for key.
func (v *OptionValues) String(key string) string {
	s := v.lookupSpec(key)
	if v != nil {
		if val, ok := v.values[key]; ok {
			return val.(string)
		}
	}
	if s.defaultValue == nil {
		return ""
	}
	return s.defaultValue.(string)
}

// Bool returns the bool value for key.
func (v *OptionValues) Bool(key string) bool {
	s := v.lookupSpec(key)
	if v != nil {
		if val, ok := v.values[key]; ok {
			return val.(bool)
		}
	}
	if s.defaultValue == nil {
		return false
	}
	return s.defaultValue.(bool)
}

// Decimal returns a fresh clone of the decimal value for key.
func (v *OptionValues) Decimal(key string) *apd.Decimal {
	s := v.lookupSpec(key)
	var src *apd.Decimal
	if v != nil {
		if val, ok := v.values[key]; ok {
			src = val.(*apd.Decimal)
		}
	}
	if src == nil && s.defaultValue != nil {
		src = s.defaultValue.(*apd.Decimal)
	}
	if src == nil {
		return nil
	}
	out := new(apd.Decimal)
	out.Set(src)
	return out
}

// StringList returns a fresh clone of the accumulated string list for key.
func (v *OptionValues) StringList(key string) []string {
	s := v.lookupSpec(key)
	var src []string
	if v != nil {
		if val, ok := v.values[key]; ok {
			src = val.([]string)
		}
	}
	if src == nil && s.defaultValue != nil {
		src = s.defaultValue.([]string)
	}
	if src == nil {
		return nil
	}
	return append([]string(nil), src...)
}

// ParseOptions walks ledger's directives and applies the default registry,
// returning typed option values and diagnostics for any malformed entries.
// The diagnostic format matches what [Ledger.Diagnostics] records. Option
// keys not in the registry are silently ignored. A nil ledger returns
// default values and no diagnostics.
func ParseOptions(ledger *Ledger) (*OptionValues, []Diagnostic) {
	v := newOptionValues(defaultRegistry)
	if ledger == nil {
		return v, nil
	}
	var diags []Diagnostic
	for _, d := range ledger.All() {
		opt, ok := d.(*Option)
		if !ok {
			continue
		}
		if err := v.set(opt.Key, opt.Value); err != nil {
			diags = append(diags, Diagnostic{
				Code:     invalidOptionCode,
				Span:     opt.Span,
				Severity: Error,
				Message:  fmt.Sprintf("invalid option %q: %v", opt.Key, err),
			})
		}
	}
	return v, diags
}

var defaultRegistry = newDefaultRegistry()

func newDefaultRegistry() *registry {
	r := newRegistry()
	mustRegisterDefault(r.register(spec{
		key:          "operating_currency",
		kind:         kindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	}))
	mustRegisterDefault(r.register(spec{
		key:          "inferred_tolerance_multiplier",
		kind:         kindDecimal,
		parse:        parseDecimalOption,
		defaultValue: apd.New(5, -1),
	}))
	mustRegisterDefault(r.register(spec{
		key:          "infer_tolerance_from_cost",
		kind:         kindBool,
		parse:        parseBoolOption,
		defaultValue: false,
	}))
	mustRegisterDefault(r.register(spec{
		key:          "plugin_processing_mode",
		kind:         kindString,
		parse:        parseStringOption,
		defaultValue: "",
	}))
	mustRegisterDefault(r.register(spec{
		key:          "title",
		kind:         kindString,
		parse:        parseStringOption,
		defaultValue: "",
	}))
	return r
}

func mustRegisterDefault(err error) {
	if err != nil {
		panic(fmt.Sprintf("options: default registry initialization failed: %v", err))
	}
}

func parseStringOption(raw string) (any, error) {
	return raw, nil
}

func parseBoolOption(raw string) (any, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	}
	return nil, fmt.Errorf("expected TRUE or FALSE, got %q", raw)
}

func parseDecimalOption(raw string) (any, error) {
	d, _, err := apd.BaseContext.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return d, nil
}

func parseCurrencyListItem(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("currency must not be empty")
	}
	return s, nil
}
