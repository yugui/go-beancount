// Package options parses and stores beancount `option` directive values.
//
// The public API consists of the Values accessor type, the Parse entry
// point that walks an *ast.Ledger and produces parsed values, and the
// ParseError struct used to surface per-directive parse failures.
//
// Internally the package uses a small typed registry: each known option is
// described by a spec (key, kind, parser, default). The package-level
// defaultRegistry wires up the options consumed by validation
// (operating_currency, inferred_tolerance_multiplier,
// infer_tolerance_from_cost). Unknown option keys are silently ignored,
// preserving forward compatibility with ledgers written for a newer tool.
package options

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// kind classifies how an option's raw value is parsed and stored.
type kind int

const (
	// kindString is a single string-valued option (last-write-wins).
	kindString kind = iota
	// kindBool is a boolean option accepting TRUE/FALSE.
	kindBool
	// kindDecimal is a decimal-valued option.
	kindDecimal
	// kindStringList is repeatable; values accumulate in source order.
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

// newRegistry returns an empty registry.
func newRegistry() *registry {
	return &registry{specs: make(map[string]spec)}
}

// register adds s to the registry. It returns an error if the key is
// already registered, guarding against accidental double-registration.
func (r *registry) register(s spec) error {
	if _, ok := r.specs[s.key]; ok {
		return fmt.Errorf("options: key %q is already registered", s.key)
	}
	r.specs[s.key] = s
	return nil
}

// Values holds parsed option values from a single ledger.
//
// A zero Values is not usable; construct one via Parse, which binds it to
// the default registry. Accessor methods return the registered default for
// any key that was not set in the ledger.
type Values struct {
	reg    *registry
	values map[string]any
}

// newValues constructs an empty Values bound to reg.
func newValues(reg *registry) *Values {
	return &Values{reg: reg, values: make(map[string]any)}
}

// set looks up the spec, parses the raw value, and stores it.
// Unknown keys return nil silently (no error). Parse errors are returned.
// For kindStringList the parsed item is appended; for scalars the value is
// overwritten (last-write-wins).
func (v *Values) set(key, raw string) error {
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

// lookupSpec returns the spec for key or panics if the key is not registered.
// The panic guards against programmer errors in callers of the accessors.
func (v *Values) lookupSpec(key string) spec {
	s, ok := v.reg.specs[key]
	if !ok {
		panic(fmt.Sprintf("options: key %q is not registered", key))
	}
	return s
}

// String returns the stored string value or the spec default.
// It panics if key is not a registered option name.
func (v *Values) String(key string) string {
	s := v.lookupSpec(key)
	if val, ok := v.values[key]; ok {
		return val.(string)
	}
	if s.defaultValue == nil {
		return ""
	}
	return s.defaultValue.(string)
}

// Bool returns the stored bool value or the spec default.
// It panics if key is not a registered option name.
func (v *Values) Bool(key string) bool {
	s := v.lookupSpec(key)
	if val, ok := v.values[key]; ok {
		return val.(bool)
	}
	if s.defaultValue == nil {
		return false
	}
	return s.defaultValue.(bool)
}

// Decimal returns a fresh clone of the stored decimal or the spec default.
// Callers may mutate the returned value without affecting stored state.
// It panics if key is not a registered option name.
func (v *Values) Decimal(key string) *apd.Decimal {
	s := v.lookupSpec(key)
	var src *apd.Decimal
	if val, ok := v.values[key]; ok {
		src = val.(*apd.Decimal)
	} else if s.defaultValue != nil {
		src = s.defaultValue.(*apd.Decimal)
	}
	if src == nil {
		return nil
	}
	out := new(apd.Decimal)
	out.Set(src)
	return out
}

// StringList returns a fresh clone of the accumulated list or the spec
// default. Callers may mutate the returned slice without affecting stored
// state. It panics if key is not a registered option name.
func (v *Values) StringList(key string) []string {
	s := v.lookupSpec(key)
	var src []string
	if val, ok := v.values[key]; ok {
		src = val.([]string)
	} else if s.defaultValue != nil {
		src = s.defaultValue.([]string)
	}
	if src == nil {
		return nil
	}
	return append([]string(nil), src...)
}
