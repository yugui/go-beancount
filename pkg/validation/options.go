package validation

import (
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

// optionKind classifies how an option's raw value is parsed and stored.
type optionKind int

const (
	// optionKindString is a single string-valued option (last-write-wins).
	optionKindString optionKind = iota
	// optionKindBool is a boolean option accepting TRUE/FALSE.
	optionKindBool
	// optionKindDecimal is a decimal-valued option.
	optionKindDecimal
	// optionKindStringList is repeatable; values accumulate in source order.
	optionKindStringList
)

// optionSpec describes one option's type, parser, and default.
type optionSpec struct {
	key          string
	kind         optionKind
	parse        func(raw string) (any, error)
	defaultValue any
}

// optionRegistry holds all known option specs keyed by option name.
type optionRegistry struct {
	specs map[string]optionSpec
}

// register adds spec to the registry, overwriting any previous entry for the key.
func (r *optionRegistry) register(spec optionSpec) {
	r.specs[spec.key] = spec
}

// optionValues holds parsed option values from a single ledger.
type optionValues struct {
	reg    *optionRegistry
	values map[string]any
}

// newOptionValues constructs an empty optionValues bound to reg.
func newOptionValues(reg *optionRegistry) *optionValues {
	return &optionValues{reg: reg, values: make(map[string]any)}
}

// set looks up the spec, parses the raw value, and stores it.
// Unknown keys return nil silently (no error). Parse errors are returned.
// For StringList the parsed item is appended; for scalars the value is
// overwritten (last-write-wins).
func (o *optionValues) set(key, raw string) error {
	spec, ok := o.reg.specs[key]
	if !ok {
		return nil
	}
	v, err := spec.parse(raw)
	if err != nil {
		return err
	}
	if spec.kind == optionKindStringList {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("string list parser returned %T, want string", v)
		}
		existing, _ := o.values[key].([]string)
		o.values[key] = append(existing, s)
		return nil
	}
	o.values[key] = v
	return nil
}

// lookupSpec returns the spec for key or panics if the key is not registered.
// The panic guards against programmer errors inside the validation package.
func (o *optionValues) lookupSpec(key string) optionSpec {
	spec, ok := o.reg.specs[key]
	if !ok {
		panic(fmt.Sprintf("validation: option %q is not registered", key))
	}
	return spec
}

// String returns the stored string value or the spec default.
func (o *optionValues) String(key string) string {
	spec := o.lookupSpec(key)
	if v, ok := o.values[key]; ok {
		return v.(string)
	}
	if spec.defaultValue == nil {
		return ""
	}
	return spec.defaultValue.(string)
}

// Bool returns the stored bool value or the spec default.
func (o *optionValues) Bool(key string) bool {
	spec := o.lookupSpec(key)
	if v, ok := o.values[key]; ok {
		return v.(bool)
	}
	if spec.defaultValue == nil {
		return false
	}
	return spec.defaultValue.(bool)
}

// Decimal returns a fresh clone of the stored decimal or the spec default.
func (o *optionValues) Decimal(key string) *apd.Decimal {
	spec := o.lookupSpec(key)
	var src *apd.Decimal
	if v, ok := o.values[key]; ok {
		src = v.(*apd.Decimal)
	} else if spec.defaultValue != nil {
		src = spec.defaultValue.(*apd.Decimal)
	}
	if src == nil {
		return nil
	}
	out := new(apd.Decimal)
	out.Set(src)
	return out
}

// StringList returns the accumulated list or the spec default.
func (o *optionValues) StringList(key string) []string {
	spec := o.lookupSpec(key)
	if v, ok := o.values[key]; ok {
		return v.([]string)
	}
	if spec.defaultValue == nil {
		return nil
	}
	return spec.defaultValue.([]string)
}

// parseStringOption is the identity parser.
func parseStringOption(raw string) (any, error) {
	return raw, nil
}

// parseBoolOption accepts TRUE/FALSE case-insensitive.
func parseBoolOption(raw string) (any, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	}
	return nil, fmt.Errorf("expected TRUE or FALSE, got %q", raw)
}

// parseDecimalOption parses a decimal literal.
func parseDecimalOption(raw string) (any, error) {
	d, _, err := apd.BaseContext.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return d, nil
}

// parseCurrencyListItem trims and rejects empty entries.
func parseCurrencyListItem(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("currency must not be empty")
	}
	return s, nil
}

// newDefaultOptionRegistry constructs the package-default registry.
func newDefaultOptionRegistry() *optionRegistry {
	r := &optionRegistry{specs: make(map[string]optionSpec)}
	r.register(optionSpec{
		key:          "operating_currency",
		kind:         optionKindStringList,
		parse:        parseCurrencyListItem,
		defaultValue: []string(nil),
	})
	return r
}

// defaultOptionRegistry is the package-wide registry used by Check.
var defaultOptionRegistry = newDefaultOptionRegistry()
