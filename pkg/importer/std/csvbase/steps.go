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

// MapValue translates in through m. A blank (TrimSpace-empty) input yields ""
// without consulting m (no miss). With csvkit.Strict a non-blank miss
// soft-fails with code; with csvkit.Verbatim a non-blank miss passes the
// value through. A soft-failed input propagates.
func MapValue(b *Builder, in Key[string], m map[string]string, mode csvkit.MapMode, code string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		raw, d := Value(c, in)
		if d != nil {
			return "", d, nil
		}
		if strings.TrimSpace(raw) == "" {
			return "", nil, nil
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

// Trim returns a Key yielding the TrimSpace of in's value. A soft-failed input
// propagates its diagnostic unchanged.
func Trim(b *Builder, in Key[string]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return "", d, nil
		}
		return strings.TrimSpace(v), nil, nil
	})
}

// Else returns primary's trimmed value when primary succeeds and is non-blank
// (after TrimSpace). When primary succeeds but is blank, Else returns
// fallback's trimmed value and propagates a fallback soft-fail. When primary
// itself soft-fails, Else propagates primary's diagnostic without consulting
// fallback. Unlike Coalesce, a primary soft-fail is never swallowed.
func Else(b *Builder, primary, fallback Key[string]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		pv, pd := Value(c, primary)
		if pd != nil {
			return "", pd, nil
		}
		if t := strings.TrimSpace(pv); t != "" {
			return t, nil, nil
		}
		fv, fd := Value(c, fallback)
		if fd != nil {
			return "", fd, nil
		}
		return strings.TrimSpace(fv), nil, nil
	})
}

// CurrencyHint extracts the CurrencyHint string from amt. Returns "" when
// amt's value is nil, has no hint, or amt soft-failed. Soft-fails are
// intentionally swallowed (rather than propagated) so the absent-amount case
// and the parse-failed case both produce an empty hint; use Coalesce or
// Require downstream to handle absence.
func CurrencyHint(b *Builder, amt Key[*csvkit.Amount]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		v, d := Value(c, amt)
		if d != nil {
			// swallows soft-fail intentionally
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
		out[i] = MapValue(b, in, m, mode, code)
	}
	return out
}

// DiagAsWarning rewrites the severity of in's soft-fail diagnostic to Warning
// and replaces its Code with newCode. A successful value passes through
// untouched. Use to convert an error-severity miss (e.g. MapValue strict) into
// a warn-keep signal that DoubleEntry surfaces without dropping the row.
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
	// Code is the diagnostic code for a non-blank unparseable value; ""
	// selects DiagBadAmount.
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

// AddAmounts returns lhs+rhs. nil is the additive identity:
// AddAmounts(nil,nil)=nil, AddAmounts(nil,v)=v, AddAmounts(v,nil)=v.
// Conflicting non-empty CurrencyHints soft-fail with code (default
// DiagBadAmount). A soft-failed input propagates its diagnostic.
func AddAmounts(b *Builder, lhs, rhs Key[*csvkit.Amount], code string) Key[*csvkit.Amount] {
	if code == "" {
		code = DiagBadAmount
	}
	return AddStep(b, func(ms *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		lv, ld := Value(ms, lhs)
		if ld != nil {
			return nil, ld, nil
		}
		rv, rd := Value(ms, rhs)
		if rd != nil {
			return nil, rd, nil
		}
		if lv == nil {
			return rv, nil, nil
		}
		if rv == nil {
			return lv, nil, nil
		}
		hint := lv.CurrencyHint
		if rv.CurrencyHint != "" {
			if hint != "" && hint != rv.CurrencyHint {
				info := ms.Info()
				diag := ErrorDiag(code, info.Path, info.Line,
					fmt.Sprintf("conflicting currency hints: %q vs %q", hint, rv.CurrencyHint))
				return nil, &diag, nil
			}
			hint = rv.CurrencyHint
		}
		var sum apd.Decimal
		if _, err := apd.BaseContext.Add(&sum, &lv.Number, &rv.Number); err != nil {
			info := ms.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot add amounts: %v", err))
			return nil, &diag, nil
		}
		return &csvkit.Amount{Number: sum, CurrencyHint: hint}, nil, nil
	})
}

// SubAmounts returns lhs-rhs, treating nil as zero: a nil rhs yields lhs
// unchanged, a nil lhs yields -rhs, and nil-nil yields nil. Conflicting
// non-empty CurrencyHints soft-fail with code (default DiagBadAmount). A
// soft-failed input propagates its diagnostic.
func SubAmounts(b *Builder, lhs, rhs Key[*csvkit.Amount], code string) Key[*csvkit.Amount] {
	if code == "" {
		code = DiagBadAmount
	}
	return AddStep(b, func(c *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		lv, ld := Value(c, lhs)
		if ld != nil {
			return nil, ld, nil
		}
		rv, rd := Value(c, rhs)
		if rd != nil {
			return nil, rd, nil
		}
		if rv == nil {
			return lv, nil, nil
		}
		var hint string
		var lnum apd.Decimal
		if lv != nil {
			hint = lv.CurrencyHint
			lnum = lv.Number
		}
		if rv.CurrencyHint != "" {
			if hint != "" && hint != rv.CurrencyHint {
				info := c.Info()
				diag := ErrorDiag(code, info.Path, info.Line,
					fmt.Sprintf("conflicting currency hints: %q vs %q", hint, rv.CurrencyHint))
				return nil, &diag, nil
			}
			hint = rv.CurrencyHint
		}
		var diff apd.Decimal
		if _, err := apd.BaseContext.Sub(&diff, &lnum, &rv.Number); err != nil {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot subtract amounts: %v", err))
			return nil, &diag, nil
		}
		return &csvkit.Amount{Number: diff, CurrencyHint: hint}, nil, nil
	})
}

// AbsAmount returns the absolute value of in (preserving CurrencyHint). A nil
// input yields nil. A soft-failed input propagates its diagnostic.
func AbsAmount(b *Builder, in Key[*csvkit.Amount]) Key[*csvkit.Amount] {
	return AddStep(b, func(c *MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return nil, d, nil
		}
		if v == nil {
			return nil, nil, nil
		}
		var abs apd.Decimal
		if _, err := apd.BaseContext.Abs(&abs, &v.Number); err != nil {
			info := c.Info()
			diag := ErrorDiag(DiagBadAmount, info.Path, info.Line,
				fmt.Sprintf("cannot take absolute value: %v", err))
			return nil, &diag, nil
		}
		return &csvkit.Amount{Number: abs, CurrencyHint: v.CurrencyHint}, nil, nil
	})
}

// isZeroKey reports whether k is the zero value (name == ""), meaning it was
// not produced by any AddStep call.
func isZeroKey[T any](k Key[T]) bool { return k.name == "" }

// Row returns a leaf Key that yields a fresh map of every indexed column name
// to its raw (untrimmed) cell value for each row. Unlike Column it registers no
// required header columns, so a template fed from Row does not force its
// referenced columns to be present.
//
// Row joins Column as a leaf under the leaf-only invariant: it is the typed
// entry point for steps (such as Template) that consume the whole row as a map
// without reading raw cells themselves.
func Row(b *Builder) Key[map[string]string] {
	return AddStep(b, func(c *MappingState) (map[string]string, *ast.Diagnostic, error) {
		return c.Row(), nil, nil
	})
}

// Merge overlays over onto a copy of base's map, yielding a new map Key. For
// each (name, key) in over, the trimmed Value of key replaces the same-named
// entry when key did not soft-fail; a soft-failed binding is skipped, leaving
// base's entry intact. This reproduces csvimp.applySplit semantics, letting
// split-group and other Key-derived values shadow raw columns before a Template
// renders. A soft-failed base propagates its diagnostic.
func Merge(b *Builder, base Key[map[string]string], over map[string]Key[string]) Key[map[string]string] {
	return AddStep(b, func(c *MappingState) (map[string]string, *ast.Diagnostic, error) {
		src, d := Value(c, base)
		if d != nil {
			return nil, d, nil
		}
		out := make(map[string]string, len(src)+len(over))
		for k, v := range src {
			out[k] = v
		}
		for name, key := range over {
			v, vd := Value(c, key)
			if vd != nil {
				continue
			}
			out[name] = strings.TrimSpace(v)
		}
		return out, nil, nil
	})
}

// Template renders tmpl against the column map yielded by data. A render error
// (e.g. a reference to a column absent from the map) soft-fails with
// DiagBadTemplate. A soft-failed data propagates its diagnostic.
//
// Template reads only data via Value; the raw row reaches it through the Row
// leaf, so Template upholds the leaf-only invariant. Compose it as
// Template(b, tmpl, Row(b)) for the plain case, or
// Template(b, tmpl, Merge(b, Row(b), bindings)) to overlay Key-derived values.
func Template(b *Builder, tmpl *csvkit.Template, data Key[map[string]string]) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		m, d := Value(c, data)
		if d != nil {
			return "", d, nil
		}
		out, err := tmpl.Render(m)
		if err != nil {
			info := c.Info()
			diag := ErrorDiag(DiagBadTemplate, info.Path, info.Line,
				fmt.Sprintf("template: %v", err))
			return "", &diag, nil
		}
		return out, nil, nil
	})
}
