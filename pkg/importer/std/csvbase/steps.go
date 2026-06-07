package csvbase

import (
	"fmt"
	"regexp"
	"strings"
	"time"

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

// AmountInput is one numeric contribution to a SumAmounts step.
type AmountInput struct {
	Source Key[string]
	Negate bool
}

// AmountConfig configures SumAmounts.
type AmountConfig struct {
	Cols          []AmountInput
	Format        csvkit.NumberFormat
	SplitCurrency bool
	// BadCode is the diagnostic code for a non-blank unparseable input; defaults
	// to DiagBadAmount.
	BadCode string
	// BlankCode is the diagnostic code when every input is blank; defaults to
	// DiagAllBlankAmount.
	BlankCode string
}

// SumAmounts sums cfg.Cols under cfg.Format via csvkit.AmountParser.SumValues.
// A non-blank unparseable input soft-fails with cfg.BadCode (default
// DiagBadAmount); every input blank soft-fails with cfg.BlankCode (default
// DiagAllBlankAmount). Bad inputs are identified by 0-based position.
func SumAmounts(b *Builder, cfg AmountConfig) Key[csvkit.Amount] {
	badCode := cfg.BadCode
	if badCode == "" {
		badCode = DiagBadAmount
	}
	blankCode := cfg.BlankCode
	if blankCode == "" {
		blankCode = DiagAllBlankAmount
	}
	p := csvkit.AmountParser{Format: cfg.Format, SplitCurrency: cfg.SplitCurrency}
	return AddStep(b, func(c *MappingState) (csvkit.Amount, *ast.Diagnostic, error) {
		vals := make([]csvkit.ValueAmount, len(cfg.Cols))
		for i, col := range cfg.Cols {
			v, _ := Value(c, col.Source) // soft-failed source treated as blank, per spec
			vals[i] = csvkit.ValueAmount{Value: v, Negate: col.Negate}
		}
		sum, status, badIdx := p.SumValues(vals)
		info := c.Info()
		switch status {
		case csvkit.AmountOK:
			return sum, nil, nil
		case csvkit.AmountBad:
			diag := ErrorDiag(badCode, info.Path, info.Line,
				fmt.Sprintf("cannot parse amount input #%d", badIdx))
			return csvkit.Amount{}, &diag, nil
		default: // AmountAllBlank
			diag := ErrorDiag(blankCode, info.Path, info.Line, "all amount columns blank")
			return csvkit.Amount{}, &diag, nil
		}
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

// joinSources trims each source's Value, drops blank and soft-failed entries,
// and joins survivors with sep. Used internally by resolver steps.
func joinSources(c *MappingState, sources []Key[string], sep string) string {
	parts := make([]string, 0, len(sources))
	for _, k := range sources {
		v, d := Value(c, k)
		if d != nil {
			continue
		}
		if t := strings.TrimSpace(v); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, sep)
}

// AccountConfig configures ResolveAccount.
type AccountConfig struct {
	Sources []Key[string]
	Sep     string
	Default string
	Map     map[string]string
	// HintKey, when non-empty, makes Hints[HintKey] take priority over sources.
	HintKey string
	// MissingCode is the soft-fail code when no account can be resolved; defaults
	// to DiagMissingAccount.
	MissingCode string
	// UnmappedCode is the soft-fail code when Map is set and the key is absent;
	// defaults to DiagUnmappedAccount.
	UnmappedCode string
}

// ResolveAccount mirrors csvimp.resolveAccount: priority Hints[HintKey] (when
// HintKey != "" and present non-empty) > joined Sources (Strict via Map when
// Map != nil, else verbatim) > Default. Missing soft-fails with MissingCode
// (default DiagMissingAccount); unmapped (Map set, key absent) soft-fails with
// UnmappedCode (default DiagUnmappedAccount).
func ResolveAccount(b *Builder, cfg AccountConfig) Key[string] {
	missingCode := cfg.MissingCode
	if missingCode == "" {
		missingCode = DiagMissingAccount
	}
	unmappedCode := cfg.UnmappedCode
	if unmappedCode == "" {
		unmappedCode = DiagUnmappedAccount
	}
	mapMode := csvkit.Verbatim
	if cfg.Map != nil {
		mapMode = csvkit.Strict
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		info := c.Info()
		// priority 1: hint override
		if cfg.HintKey != "" {
			if v, ok := info.Hints[cfg.HintKey]; ok && v != "" {
				return v, nil, nil
			}
		}
		// priority 2: joined source values
		if len(cfg.Sources) > 0 {
			key := joinSources(c, cfg.Sources, cfg.Sep)
			if key != "" {
				mapped, ok := csvkit.ResolveThroughMap(key, cfg.Map, mapMode)
				if !ok {
					diag := ErrorDiag(unmappedCode, info.Path, info.Line,
						fmt.Sprintf("account key %q from sources has no entry in account map", key))
					return "", &diag, nil
				}
				return mapped, nil, nil
			}
		}
		// priority 3: default
		if cfg.Default != "" {
			return cfg.Default, nil, nil
		}
		diag := ErrorDiag(missingCode, info.Path, info.Line,
			`no account: hint empty, sources blank/absent, and default unset`)
		return "", &diag, nil
	})
}

// CounterConfig configures ResolveCounter.
type CounterConfig struct {
	Sources []Key[string]
	Sep     string
	Default string
	Map     map[string]string
	// UnmappedCode is the code for the Warning soft-fail when Map is set and a
	// non-empty key is absent; defaults to DiagUnmappedCounterAccount.
	UnmappedCode string
}

// ResolveCounter mirrors csvimp.resolveCounterAccount: yields the counter
// account or "" (no second posting). When Map is set (Strict) and a non-empty
// joined key is absent, it soft-fails with a Warning (WarnDiag, UnmappedCode
// default DiagUnmappedCounterAccount) and yields no account: the row is kept
// with a single posting. A blank key with no Default yields "" and no
// diagnostic.
func ResolveCounter(b *Builder, cfg CounterConfig) Key[string] {
	unmappedCode := cfg.UnmappedCode
	if unmappedCode == "" {
		unmappedCode = DiagUnmappedCounterAccount
	}
	mapMode := csvkit.Verbatim
	if cfg.Map != nil {
		mapMode = csvkit.Strict
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		info := c.Info()
		if len(cfg.Sources) == 0 {
			return cfg.Default, nil, nil
		}
		key := joinSources(c, cfg.Sources, cfg.Sep)
		if key == "" {
			return cfg.Default, nil, nil
		}
		mapped, ok := csvkit.ResolveThroughMap(key, cfg.Map, mapMode)
		if !ok {
			diag := WarnDiag(unmappedCode, info.Path, info.Line,
				fmt.Sprintf("counter_account key %q from sources has no entry in counter_account map", key))
			return "", &diag, nil
		}
		return mapped, nil, nil
	})
}

// CurrencyConfig configures ResolveCurrency.
type CurrencyConfig struct {
	// Source, when non-zero, is read for an explicit currency value.
	Source     Key[string]
	Default    string
	FromAmount bool
	Map        map[string]string
	// Amount is the source of CurrencyHint when FromAmount is true.
	Amount Key[csvkit.Amount]
	// MissingCode is the soft-fail code when no currency resolves; defaults to
	// DiagMissingCurrency.
	MissingCode string
}

// ResolveCurrency mirrors csvimp.resolveCurrency: priority Source value (if
// non-zero Key and trimmed value non-empty; verbatim Map) > Amount's
// CurrencyHint (when FromAmount) > Default. Empty result soft-fails with
// MissingCode (default DiagMissingCurrency).
func ResolveCurrency(b *Builder, cfg CurrencyConfig) Key[string] {
	missingCode := cfg.MissingCode
	if missingCode == "" {
		missingCode = DiagMissingCurrency
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		info := c.Info()
		// priority 1: explicit source
		if !isZeroKey(cfg.Source) {
			if v, _ := Value(c, cfg.Source); strings.TrimSpace(v) != "" { // soft-failed source treated as blank, per spec
				mapped, _ := csvkit.ResolveThroughMap(strings.TrimSpace(v), cfg.Map, csvkit.Verbatim)
				return mapped, nil, nil
			}
		}
		// priority 2: currency hint from amount
		if cfg.FromAmount && !isZeroKey(cfg.Amount) {
			if amt, _ := Value(c, cfg.Amount); amt.CurrencyHint != "" {
				return amt.CurrencyHint, nil, nil
			}
		}
		// priority 3: default
		if cfg.Default != "" {
			return cfg.Default, nil, nil
		}
		diag := ErrorDiag(missingCode, info.Path, info.Line,
			"no currency: source empty, no amount hint, no default")
		return "", &diag, nil
	})
}

// isZeroKey reports whether k is the zero value (name == ""), meaning it was
// not produced by any AddStep call.
func isZeroKey[T any](k Key[T]) bool { return k.name == "" }

// PayeeConfig configures ResolvePayee.
type PayeeConfig struct {
	Sources []Key[string]
	Sep     string
	Map     map[string]string
}

// ResolvePayee mirrors csvimp.resolvePayee: joined Sources, verbatim Map (a
// mapped "" suppresses), yields "" when blank. Soft-failed inputs contribute
// nothing to the join.
func ResolvePayee(b *Builder, cfg PayeeConfig) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		if len(cfg.Sources) == 0 {
			return "", nil, nil
		}
		v := joinSources(c, cfg.Sources, cfg.Sep)
		if v == "" {
			return "", nil, nil
		}
		mapped, _ := csvkit.ResolveThroughMap(v, cfg.Map, csvkit.Verbatim)
		return mapped, nil, nil
	})
}

// NarrationFromSources mirrors csvimp.buildNarration: per-source verbatim Map
// (mapped "" drops the entry), trim, drop blanks, join with sep. Soft-failed
// inputs contribute nothing.
func NarrationFromSources(b *Builder, sources []Key[string], sep string, m map[string]string) Key[string] {
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		parts := make([]string, 0, len(sources))
		for _, src := range sources {
			v, d := Value(c, src)
			if d != nil {
				continue
			}
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			v, _ = csvkit.ResolveThroughMap(v, m, csvkit.Verbatim)
			if v == "" {
				continue
			}
			parts = append(parts, v)
		}
		return strings.Join(parts, sep), nil, nil
	})
}

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

// CostConfig configures ResolveCost.
type CostConfig struct {
	Number          Key[string]
	IsTotal         bool
	Currency        Key[string]
	DefaultCurrency string
	Date            Key[string]
	DateFormat      string
	Label           Key[string]
	Format          csvkit.NumberFormat
	// Code is the soft-fail code for any cost error; defaults to DiagBadCost.
	Code string
}

// ResolveCost mirrors csvimp.buildCost. A blank Number Key value yields a nil
// *ast.CostSpec (no cost, not an error). A non-blank unparseable number, a
// number with no resolvable currency, or an unparseable date soft-fails with
// Code (default DiagBadCost). A zero Currency/Date/Label Key means "not
// configured".
func ResolveCost(b *Builder, cfg CostConfig) Key[*ast.CostSpec] {
	code := cfg.Code
	if code == "" {
		code = DiagBadCost
	}
	return AddStep(b, func(c *MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		info := c.Info()

		var rawNum string
		if !isZeroKey(cfg.Number) {
			rawNum, _ = Value(c, cfg.Number) // soft-failed source treated as blank, per spec
		}
		num, blank, err := csvkit.ParseNumber(rawNum, cfg.Format)
		if blank {
			return nil, nil, nil
		}
		if err != nil {
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot parse cost number: %q", rawNum))
			return nil, &diag, nil
		}

		cur := cfg.DefaultCurrency
		if !isZeroKey(cfg.Currency) {
			if v, _ := Value(c, cfg.Currency); strings.TrimSpace(v) != "" { // soft-failed source treated as blank, per spec
				cur = strings.TrimSpace(v)
			}
		}
		if cur == "" {
			diag := ErrorDiag(code, info.Path, info.Line,
				"cost has no currency: currency source blank and no default_currency")
			return nil, &diag, nil
		}

		cs := &ast.CostSpec{
			Currency: cur,
		}
		if !isZeroKey(cfg.Label) {
			if v, _ := Value(c, cfg.Label); strings.TrimSpace(v) != "" { // soft-failed source treated as blank, per spec
				cs.Label = strings.TrimSpace(v)
			}
		}
		n := num
		if cfg.IsTotal {
			cs.Total = &n
		} else {
			cs.PerUnit = &n
		}

		if !isZeroKey(cfg.Date) {
			if dv, _ := Value(c, cfg.Date); strings.TrimSpace(dv) != "" { // soft-failed source treated as blank, per spec
				t, err := time.Parse(cfg.DateFormat, strings.TrimSpace(dv))
				if err != nil {
					diag := ErrorDiag(code, info.Path, info.Line,
						fmt.Sprintf("cannot parse cost date %q with format %q: %v",
							strings.TrimSpace(dv), cfg.DateFormat, err))
					return nil, &diag, nil
				}
				cs.Date = &t
			}
		}
		return cs, nil, nil
	})
}
