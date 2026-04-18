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
