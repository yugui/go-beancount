package options

import (
	"github.com/yugui/go-beancount/pkg/ast"
)

// ParseError describes a single option directive whose value could not be
// parsed. The Span identifies the directive's source location so callers
// can convert the error into a diagnostic keyed to the ledger.
type ParseError struct {
	// Key is the option name as written in the directive.
	Key string
	// Value is the raw option value that failed to parse.
	Value string
	// Span locates the offending directive in the source ledger.
	Span ast.Span
	// Err is the underlying parse error.
	Err error
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return "invalid option \"" + e.Key + "\": " + e.Err.Error()
}

// Unwrap exposes the underlying parse error to errors.Is/As.
func (e *ParseError) Unwrap() error { return e.Err }

// Parse walks ledger's directives in canonical order, applying the default
// registry. Option directives whose keys are not registered are silently
// ignored. Directives whose values fail to parse produce ParseError
// entries; successfully-parsed directives are accumulated into the returned
// Values. A nil ledger yields an empty Values and no errors.
func Parse(ledger *ast.Ledger) (*Values, []ParseError) {
	v := newValues(defaultRegistry)
	if ledger == nil {
		return v, nil
	}
	var errs []ParseError
	for _, d := range ledger.All() {
		opt, ok := d.(*ast.Option)
		if !ok {
			continue
		}
		if err := v.set(opt.Key, opt.Value); err != nil {
			errs = append(errs, ParseError{
				Key:   opt.Key,
				Value: opt.Value,
				Span:  opt.Span,
				Err:   err,
			})
		}
	}
	return v, errs
}

// FromRaw builds a typed *Values from a raw map of option key/value pairs
// (typically obtained from BuildRaw). Unknown keys are ignored; malformed
// values produce ParseError entries with a zero Span because the raw map
// does not carry source locations. A nil or empty map yields a default
// *Values and no errors.
//
// FromRaw is equivalent to Parse(ledger) when raw equals BuildRaw(ledger):
// because BuildRaw has already condensed duplicate keys to last-wins, each
// key here is fed to Values.set exactly once. For kindStringList options
// this means the resulting list contains at most one element, matching
// what Parse would have produced for a ledger with a single directive for
// that key.
func FromRaw(raw map[string]string) (*Values, []ParseError) {
	v := newValues(defaultRegistry)
	if len(raw) == 0 {
		return v, nil
	}
	var errs []ParseError
	for key, value := range raw {
		if err := v.set(key, value); err != nil {
			errs = append(errs, ParseError{
				Key:   key,
				Value: value,
				Err:   err,
			})
		}
	}
	return v, errs
}

// BuildRaw walks ledger's directives in canonical order and builds a
// map[string]string of raw option key/value pairs. Later occurrences of
// the same key overwrite earlier ones (last-wins semantics) — this
// differs from the typed StringList accumulation applied by Parse.
//
// BuildRaw returns nil if ledger is nil or contains no option
// directives; it never returns an empty non-nil map. This is the form
// consumed by plugin runners that pass raw option values through to
// plugins without type coercion.
func BuildRaw(ledger *ast.Ledger) map[string]string {
	if ledger == nil {
		return nil
	}
	var opts map[string]string
	for _, d := range ledger.All() {
		o, ok := d.(*ast.Option)
		if !ok {
			continue
		}
		if opts == nil {
			opts = make(map[string]string)
		}
		opts[o.Key] = o.Value
	}
	return opts
}
