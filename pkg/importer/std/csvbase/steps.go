package csvbase

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// Column registers name as a required header column and returns a Key that
// yields its raw (untrimmed) cell value for every row.
func Column(b *Builder, name string) Key[string] {
	b.Require(name)
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		return c.At(name), nil, nil
	})
}

// Columns is a convenience for []Key[string]{Column(b, names[0]), …}: it
// registers each name as a required leaf column and returns the typed Keys in
// the same order.
func Columns(b *Builder, names ...string) []Key[string] {
	keys := make([]Key[string], len(names))
	for i, name := range names {
		keys[i] = Column(b, name)
	}
	return keys
}

// SplitColumns runs re over source and exposes its named capture groups as
// separate Keys. It is Split(b, source, re) plus a Group(b, split, name) for
// every non-empty subexpression name in re. The returned map is keyed by group
// name. SplitColumns calls no Require beyond what source itself does (so split
// groups are NOT required header columns).
func SplitColumns(b *Builder, source Key[string], re *regexp.Regexp) map[string]Key[string] {
	split := Split(b, source, re)
	names := re.SubexpNames()
	out := make(map[string]Key[string], len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		out[name] = Group(b, split, name)
	}
	return out
}

// Const yields v for every row.
func Const[T any](b *Builder, v T) Key[T] {
	return AddStep(b, func(*MappingState) (T, *ast.Diagnostic, error) {
		return v, nil, nil
	})
}

// ParseDate parses the trimmed value of in under the given time layout. On
// failure it soft-fails with code (defaulting to DiagBadDate when empty). A
// soft-failed input propagates without re-parsing.
func ParseDate(b *Builder, in Key[string], layout, code string) Key[time.Time] {
	if code == "" {
		code = DiagBadDate
	}
	return AddStep(b, func(c *MappingState) (time.Time, *ast.Diagnostic, error) {
		raw, d := Value(c, in)
		if d != nil {
			return time.Time{}, d, nil
		}
		t, err := time.Parse(layout, strings.TrimSpace(raw))
		if err != nil {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot parse date %q with format %q: %v", raw, layout, err))
			return time.Time{}, &diag, nil
		}
		return t, nil, nil
	})
}

// Split runs re over in and yields its named capture groups. On no match it
// yields an empty map (so Group reads ""), mirroring csvimp. A soft-failed
// input propagates. It registers no required columns (in already did).
func Split(b *Builder, in Key[string], re *regexp.Regexp) Key[map[string]string] {
	return AddStep(b, func(c *MappingState) (map[string]string, *ast.Diagnostic, error) {
		raw, d := Value(c, in)
		if d != nil {
			return nil, d, nil
		}
		groups, ok := csvkit.NamedSubmatches(re, raw)
		if !ok {
			return map[string]string{}, nil, nil
		}
		return groups, nil, nil
	})
}

// Group yields the named group from a Split result, or "" when absent or the
// split produced no match.
func Group(b *Builder, split Key[map[string]string], name string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		m, d := Value(c, split)
		if d != nil {
			return "", d, nil
		}
		return m[name], nil, nil
	})
}

// MapValue translates in through m. With csvkit.Strict a miss soft-fails with
// code; with csvkit.Verbatim a miss passes the value through. A soft-failed
// input propagates.
func MapValue(b *Builder, in Key[string], m map[string]string, mode csvkit.MapMode, code string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		raw, d := Value(c, in)
		if d != nil {
			return "", d, nil
		}
		mapped, ok := csvkit.ResolveThroughMap(raw, m, mode)
		if !ok {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("key %q has no entry in map", raw))
			return "", &diag, nil
		}
		return mapped, nil, nil
	})
}

// JoinKeys trims each input's value, drops blanks, and joins survivors with
// sep (csvkit.Join semantics). Soft-failed inputs are treated as blank.
func JoinKeys(b *Builder, sep string, ins ...Key[string]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		parts := make([]string, 0, len(ins))
		for _, k := range ins {
			v, _ := Value(c, k)
			if t := strings.TrimSpace(v); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, sep), nil, nil
	})
}

// Hint returns a leaf Key that reads info.Hints[name] for every row. Empty
// when the hint is absent or set to "".
func Hint(b *Builder, name string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		return c.Info().Hints[name], nil, nil
	})
}

// Coalesce returns the first input whose value (after TrimSpace) is non-empty;
// the result is "" when every input is blank. A soft-failed input is treated
// as blank (its diagnostic is NOT propagated). Coalesce does not emit a
// diagnostic itself.
func Coalesce(b *Builder, ins ...Key[string]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		for _, k := range ins {
			v, d := Value(c, k)
			if d != nil {
				continue
			}
			if t := strings.TrimSpace(v); t != "" {
				return t, nil, nil
			}
		}
		return "", nil, nil
	})
}

// Require soft-fails with code when in's value is blank (after TrimSpace).
// A soft-failed input propagates its existing diagnostic unchanged (code is
// not overridden). On success the trimmed value is returned.
func Require(b *Builder, in Key[string], code string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return "", d, nil
		}
		t := strings.TrimSpace(v)
		if t == "" {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line, "required value is blank")
			return "", &diag, nil
		}
		return t, nil, nil
	})
}

// CurrencyHint extracts the CurrencyHint string from amt. Returns "" when
// amt's value is nil, has no hint, or amt soft-failed.
func CurrencyHint(b *Builder, amt Key[*csvkit.Amount]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		v, d := Value(c, amt)
		if d != nil {
			return "", nil, nil
		}
		if v == nil {
			return "", nil, nil
		}
		return v.CurrencyHint, nil, nil
	})
}

// MapEach independently runs each input through m under mode, producing a
// parallel slice of Keys. A soft-failed input propagates its diagnostic to
// the corresponding output Key. Use with JoinKeys to reproduce per-cell map
// semantics.
func MapEach(b *Builder, ins []Key[string], m map[string]string, mode csvkit.MapMode, code string) []Key[string] {
	out := make([]Key[string], len(ins))
	for i, in := range ins {
		in := in
		out[i] = AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
			raw, d := Value(c, in)
			if d != nil {
				return "", d, nil
			}
			mapped, ok := csvkit.ResolveThroughMap(raw, m, mode)
			if !ok {
				info := c.Info()
				diag := ErrorDiag(code, info.Path, info.Line,
					fmt.Sprintf("key %q has no entry in map", raw))
				return "", &diag, nil
			}
			return mapped, nil, nil
		})
	}
	return out
}

// DiagAsWarning rewrites the severity of in's soft-fail diagnostic to Warning
// and replaces its Code with newCode. A successful value passes through
// untouched. Use to convert an error-severity miss (e.g. MapValue strict) into
// a warn-keep signal that EmitTransaction surfaces without dropping the row.
func DiagAsWarning[T any](b *Builder, in Key[T], newCode string) Key[T] {
	return AddStep(b, func(c *MappingState) (T, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			warn := *d
			warn.Severity = ast.Warning
			warn.Code = newCode
			var zero T
			return zero, &warn, nil
		}
		return v, nil, nil
	})
}

// ParseAmountConfig configures ParseAmount.
type ParseAmountConfig struct {
	Format        csvkit.NumberFormat
	SplitCurrency bool
	// Code is the diagnostic code for a non-blank unparseable value; defaults
	// to DiagBadAmount.
	Code string
}

// ParseAmount parses src's raw value under cfg.Format (and SplitCurrency).
// A blank or placeholder cell yields (nil, nil) — no amount, not an error.
// A non-blank unparseable value soft-fails with cfg.Code (default
// DiagBadAmount). A soft-failed src propagates its diagnostic.
func ParseAmount(b *Builder, src Key[string], cfg ParseAmountConfig) Key[*csvkit.Amount] {
	code := cfg.Code
	if code == "" {
		code = DiagBadAmount
	}
	return AddStep(b, func(c *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		raw, d := Value(c, src)
		if d != nil {
			return nil, d, nil
		}
		numStr := raw
		currency := ""
		if cfg.SplitCurrency {
			numStr, currency = csvkit.SplitCurrencySuffix(raw)
		}
		num, blank, err := csvkit.ParseNumber(numStr, cfg.Format)
		if blank {
			return nil, nil, nil
		}
		if err != nil {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot parse amount %q", raw))
			return nil, &diag, nil
		}
		return &csvkit.Amount{Number: num, CurrencyHint: currency}, nil, nil
	})
}

// NegateAmount returns -in (preserving CurrencyHint). A nil input yields nil.
// A soft-failed input propagates its diagnostic.
func NegateAmount(b *Builder, in Key[*csvkit.Amount]) Key[*csvkit.Amount] {
	return AddStep(b, func(c *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return nil, d, nil
		}
		if v == nil {
			return nil, nil, nil
		}
		var neg apd.Decimal
		if _, err := apd.BaseContext.Neg(&neg, &v.Number); err != nil {
			info := c.Info()
			diag := ErrorDiag(DiagBadAmount, info.Path, info.Line,
				fmt.Sprintf("cannot negate amount: %v", err))
			return nil, &diag, nil
		}
		return &csvkit.Amount{Number: neg, CurrencyHint: v.CurrencyHint}, nil, nil
	})
}

// AddAmounts returns a+c. nil is the additive identity: AddAmounts(nil,nil)=nil,
// AddAmounts(nil,v)=v, AddAmounts(v,nil)=v. Conflicting non-empty CurrencyHints
// soft-fail with code (default DiagBadAmount). A soft-failed input propagates
// its diagnostic.
func AddAmounts(b *Builder, a, c Key[*csvkit.Amount], code string) Key[*csvkit.Amount] {
	if code == "" {
		code = DiagBadAmount
	}
	return AddStep(b, func(ms *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		av, ad := Value(ms, a)
		if ad != nil {
			return nil, ad, nil
		}
		cv, cd := Value(ms, c)
		if cd != nil {
			return nil, cd, nil
		}
		if av == nil {
			return cv, nil, nil
		}
		if cv == nil {
			return av, nil, nil
		}
		hint := av.CurrencyHint
		if cv.CurrencyHint != "" {
			if hint != "" && hint != cv.CurrencyHint {
				info := ms.Info()
				diag := ErrorDiag(code, info.Path, info.Line,
					fmt.Sprintf("conflicting currency hints: %q vs %q", hint, cv.CurrencyHint))
				return nil, &diag, nil
			}
			hint = cv.CurrencyHint
		}
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &av.Number, &cv.Number); err != nil {
			info := ms.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot add amounts: %v", err))
			return nil, &diag, nil
		}
		return &csvkit.Amount{Number: sum, CurrencyHint: hint}, nil, nil
	})
}

// isZeroKey reports whether k is the zero value (name == ""), meaning it was
// not produced by any AddStep call.
func isZeroKey[T any](k Key[T]) bool { return k.name == "" }

// NarrationFromTemplate renders tmpl against the row's indexed columns
// (MappingState.Row()) overlaid with any bindings. For each (name, key) in
// bindings, the trimmed Value of key (when not soft-failed) replaces the
// same-named raw column in the data map, matching csvimp.applySplit semantics.
// A render error soft-fails with code (default DiagBadNarrationTemplate).
//
// This is the one justified exception to the leaf-only invariant: the set of
// fields a template references is dynamic (csvkit.CompileNarration does not
// expose its references), so the step must supply the full row map and let the
// template engine pick what it needs. Bindings allow split-group and other
// Key-derived values to participate in template rendering without requiring
// them to be raw columns.
func NarrationFromTemplate(b *Builder, tmpl *csvkit.NarrationTemplate,
	bindings map[string]Key[string], code string) Key[string] {
	if code == "" {
		code = DiagBadNarrationTemplate
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		data := c.Row()
		for name, key := range bindings {
			v, d := Value(c, key)
			if d != nil {
				continue
			}
			data[name] = strings.TrimSpace(v)
		}
		out, err := tmpl.Render(data)
		if err != nil {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("narration template: %v", err))
			return "", &diag, nil
		}
		return out, nil, nil
	})
}
