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

// AmountConfig configures SumAmounts.
type AmountConfig struct {
	Cols          []csvkit.AmountColumn
	Format        csvkit.NumberFormat
	SplitCurrency bool
	// BadCode is the diagnostic code for a non-blank unparseable column; defaults
	// to DiagBadAmount.
	BadCode string
	// BlankCode is the diagnostic code when every column is blank; defaults to
	// DiagAllBlankAmount.
	BlankCode string
}

// SumAmounts sums cfg.Cols under cfg.Format via csvkit.AmountParser. It
// registers each column as required. A non-blank unparseable column soft-fails
// with cfg.BadCode (default DiagBadAmount); every column blank soft-fails with
// cfg.BlankCode (default DiagAllBlankAmount).
func SumAmounts(b *Builder, cfg AmountConfig) Key[csvkit.Amount] {
	badCode := cfg.BadCode
	if badCode == "" {
		badCode = DiagBadAmount
	}
	blankCode := cfg.BlankCode
	if blankCode == "" {
		blankCode = DiagAllBlankAmount
	}
	for _, col := range cfg.Cols {
		b.Require(col.Col)
	}
	p := csvkit.AmountParser{Format: cfg.Format, SplitCurrency: cfg.SplitCurrency}
	return AddStep(b, func(c *MappingState) (csvkit.Amount, *ast.Diagnostic, error) {
		sum, status, badCol := p.Sum(cfg.Cols, c.At)
		info := c.Info()
		switch status {
		case csvkit.AmountOK:
			return sum, nil, nil
		case csvkit.AmountBad:
			diag := ErrorDiag(badCode, info.Path, info.Line,
				fmt.Sprintf("cannot parse amount column %q: %q", badCol, c.At(badCol)))
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

// AccountConfig configures ResolveAccount.
type AccountConfig struct {
	Cols    []string
	Sep     string
	Default string
	Map     map[string]string
	// HintKey, when non-empty, makes Hints[HintKey] take priority over column cells.
	HintKey string
	// MissingCode is the soft-fail code when no account can be resolved; defaults
	// to DiagMissingAccount.
	MissingCode string
	// UnmappedCode is the soft-fail code when Map is set and the key is absent;
	// defaults to DiagUnmappedAccount.
	UnmappedCode string
}

// ResolveAccount mirrors csvimp.resolveAccount: priority Hints[HintKey] (when
// HintKey != "" and present non-empty) > joined Cols (Strict via Map when Map
// != nil, else verbatim) > Default. Missing soft-fails with MissingCode
// (default DiagMissingAccount); unmapped (Map set, key absent) soft-fails with
// UnmappedCode (default DiagUnmappedAccount). Registers Cols as required.
func ResolveAccount(b *Builder, cfg AccountConfig) Key[string] {
	missingCode := cfg.MissingCode
	if missingCode == "" {
		missingCode = DiagMissingAccount
	}
	unmappedCode := cfg.UnmappedCode
	if unmappedCode == "" {
		unmappedCode = DiagUnmappedAccount
	}
	for _, col := range cfg.Cols {
		b.Require(col)
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
		// priority 2: joined column cells
		if len(cfg.Cols) > 0 {
			key := csvkit.Join(cfg.Cols, cfg.Sep, c.At)
			if key != "" {
				mapped, ok := csvkit.ResolveThroughMap(key, cfg.Map, mapMode)
				if !ok {
					diag := ErrorDiag(unmappedCode, info.Path, info.Line,
						fmt.Sprintf("account key %q from columns %v has no entry in account map", key, cfg.Cols))
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
			`no account: hint empty, column cells blank/absent, and default unset`)
		return "", &diag, nil
	})
}

// CounterConfig configures ResolveCounter.
type CounterConfig struct {
	Cols    []string
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
// diagnostic. Registers Cols as required.
func ResolveCounter(b *Builder, cfg CounterConfig) Key[string] {
	unmappedCode := cfg.UnmappedCode
	if unmappedCode == "" {
		unmappedCode = DiagUnmappedCounterAccount
	}
	for _, col := range cfg.Cols {
		b.Require(col)
	}
	mapMode := csvkit.Verbatim
	if cfg.Map != nil {
		mapMode = csvkit.Strict
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		info := c.Info()
		if len(cfg.Cols) == 0 {
			return cfg.Default, nil, nil
		}
		key := csvkit.Join(cfg.Cols, cfg.Sep, c.At)
		if key == "" {
			return cfg.Default, nil, nil
		}
		mapped, ok := csvkit.ResolveThroughMap(key, cfg.Map, mapMode)
		if !ok {
			diag := WarnDiag(unmappedCode, info.Path, info.Line,
				fmt.Sprintf("counter_account key %q from columns %v has no entry in counter_account map", key, cfg.Cols))
			return "", &diag, nil
		}
		return mapped, nil, nil
	})
}

// CurrencyConfig configures ResolveCurrency.
type CurrencyConfig struct {
	Col        string
	Default    string
	FromAmount bool
	Map        map[string]string
	// Amount is the source of CurrencyHint when FromAmount is true.
	Amount Key[csvkit.Amount]
	// MissingCode is the soft-fail code when no currency resolves; defaults to
	// DiagMissingCurrency.
	MissingCode string
}

// ResolveCurrency mirrors csvimp.resolveCurrency: priority Col cell (verbatim
// via Map) > Amount's CurrencyHint (when FromAmount) > Default. Empty result
// soft-fails with MissingCode (default DiagMissingCurrency). Registers Col
// when set.
func ResolveCurrency(b *Builder, cfg CurrencyConfig) Key[string] {
	missingCode := cfg.MissingCode
	if missingCode == "" {
		missingCode = DiagMissingCurrency
	}
	if cfg.Col != "" {
		b.Require(cfg.Col)
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		info := c.Info()
		// priority 1: explicit column
		if cfg.Col != "" {
			if v := strings.TrimSpace(c.At(cfg.Col)); v != "" {
				mapped, _ := csvkit.ResolveThroughMap(v, cfg.Map, csvkit.Verbatim)
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
			fmt.Sprintf("no currency: col=%q default=%q", cfg.Col, cfg.Default))
		return "", &diag, nil
	})
}

// isZeroKey reports whether k is the zero value (name == ""), meaning it was
// not produced by any AddStep call.
func isZeroKey[T any](k Key[T]) bool { return k.name == "" }

// PayeeConfig configures ResolvePayee.
type PayeeConfig struct {
	Cols []string
	Sep  string
	Map  map[string]string
}

// ResolvePayee mirrors csvimp.resolvePayee: joined Cols, verbatim Map (a
// mapped "" suppresses), yields "" when blank. Registers Cols as required.
func ResolvePayee(b *Builder, cfg PayeeConfig) Key[string] {
	for _, col := range cfg.Cols {
		b.Require(col)
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		if len(cfg.Cols) == 0 {
			return "", nil, nil
		}
		v := csvkit.Join(cfg.Cols, cfg.Sep, c.At)
		if v == "" {
			return "", nil, nil
		}
		mapped, _ := csvkit.ResolveThroughMap(v, cfg.Map, csvkit.Verbatim)
		return mapped, nil, nil
	})
}

// NarrationFromColumns mirrors csvimp.buildNarration: per-cell verbatim Map
// (mapped "" drops the cell), trim, drop blanks, join with sep. Registers Cols.
func NarrationFromColumns(b *Builder, cols []string, sep string, m map[string]string) Key[string] {
	for _, col := range cols {
		b.Require(col)
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		parts := make([]string, 0, len(cols))
		for _, col := range cols {
			v := strings.TrimSpace(c.At(col))
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
// (MappingState.Row()). A render error soft-fails with code (default
// DiagBadNarrationTemplate). It registers no columns (template references are
// best-effort, as in csvimp). Known limitation: the template data is the
// file's raw column map, not split-group keys; pipelines that combine Split
// with a template should use NarrationFromColumns or JoinKeys over Group keys
// instead.
func NarrationFromTemplate(b *Builder, tmpl *csvkit.NarrationTemplate, code string) Key[string] {
	if code == "" {
		code = DiagBadNarrationTemplate
	}
	return AddStep(b, func(c *MappingState) (string, *ast.Diagnostic, error) {
		out, err := tmpl.Render(c.Row())
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
	NumberCol       string
	IsTotal         bool
	CurrencyCol     string
	DefaultCurrency string
	DateCol         string
	DateFormat      string
	LabelCol        string
	Format          csvkit.NumberFormat
	// Code is the soft-fail code for any cost error; defaults to DiagBadCost.
	Code string
}

// ResolveCost mirrors csvimp.buildCost. A blank number cell yields a nil
// *ast.CostSpec (no cost, not an error). A non-blank unparseable number, a
// number with no resolvable currency, or an unparseable date soft-fails with
// Code (default DiagBadCost). Registers NumberCol (+ CurrencyCol/DateCol/
// LabelCol when set) as required.
func ResolveCost(b *Builder, cfg CostConfig) Key[*ast.CostSpec] {
	code := cfg.Code
	if code == "" {
		code = DiagBadCost
	}
	b.Require(cfg.NumberCol)
	if cfg.CurrencyCol != "" {
		b.Require(cfg.CurrencyCol)
	}
	if cfg.DateCol != "" {
		b.Require(cfg.DateCol)
	}
	if cfg.LabelCol != "" {
		b.Require(cfg.LabelCol)
	}
	return AddStep(b, func(c *MappingState) (*ast.CostSpec, *ast.Diagnostic, error) {
		info := c.Info()
		raw := c.At(cfg.NumberCol)
		num, blank, err := csvkit.ParseNumber(raw, cfg.Format)
		if blank {
			return nil, nil, nil
		}
		if err != nil {
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("cannot parse cost column %q: %q", cfg.NumberCol, raw))
			return nil, &diag, nil
		}

		cur := cfg.DefaultCurrency
		if cfg.CurrencyCol != "" {
			if v := strings.TrimSpace(c.At(cfg.CurrencyCol)); v != "" {
				cur = v
			}
		}
		if cur == "" {
			diag := ErrorDiag(code, info.Path, info.Line,
				"cost has no currency: currency column blank and no default_currency")
			return nil, &diag, nil
		}

		cs := &ast.CostSpec{
			Currency: cur,
		}
		if cfg.LabelCol != "" {
			cs.Label = strings.TrimSpace(c.At(cfg.LabelCol))
		}
		n := num
		if cfg.IsTotal {
			cs.Total = &n
		} else {
			cs.PerUnit = &n
		}

		if cfg.DateCol != "" {
			if dv := strings.TrimSpace(c.At(cfg.DateCol)); dv != "" {
				t, err := time.Parse(cfg.DateFormat, dv)
				if err != nil {
					diag := ErrorDiag(code, info.Path, info.Line,
						fmt.Sprintf("cannot parse cost date %q with format %q: %v", dv, cfg.DateFormat, err))
					return nil, &diag, nil
				}
				cs.Date = &t
			}
		}
		return cs, nil, nil
	})
}
