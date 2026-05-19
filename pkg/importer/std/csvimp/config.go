package csvimp

import (
	"fmt"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

type config struct {
	Shapes map[string]shapeConfig `toml:"shape"`
}

type shapeConfig struct {
	Match              string         `toml:"match"`
	Delimiter          string         `toml:"delimiter"`
	SkipLines          int            `toml:"skip_lines"`
	DateCol            string         `toml:"date_col"`
	DateFormat         string         `toml:"date_format"`
	PayeeCol           string         `toml:"payee_col"`
	CurrencyCol        string         `toml:"currency_col"`
	DefaultCurrency    string         `toml:"default_currency"`
	NarrationCols      []string       `toml:"narration_cols"`
	NarrationSeparator string         `toml:"narration_separator"`
	Account            string         `toml:"account"`
	Amount             []amountColumn `toml:"amount"`
}

type amountColumn struct {
	Col    string `toml:"col"`
	Negate bool   `toml:"negate"`
}

type shape struct {
	name          string
	compiledMatch *regexp.Regexp // nil when Match was unset
	delimiter     rune           // default ','
	skipLines     int

	dateCol     string
	dateFormat  string
	payeeCol    string
	currencyCol string
	defaultCur  string

	narrationCols []string
	narrationSep  string

	account string
	amounts []amountColumn
}

// Configure decodes the importer's configuration through the caller-
// supplied closure, validates it, and replaces any previously-
// installed shape table. The closure MUST NOT be nil. On any failure
// (nil decoder, decode error, validation error) the previous
// configuration is left untouched and a non-nil error prefixed
// "csvimp: configure: " is returned.
func (i *Importer) Configure(decode func(dest any) error) error {
	if decode == nil {
		return fmt.Errorf("csvimp: configure: nil decoder")
	}
	var cfg config
	if err := decode(&cfg); err != nil {
		return fmt.Errorf("csvimp: configure: %w", err)
	}
	shapes, err := buildShapes(cfg)
	if err != nil {
		return fmt.Errorf("csvimp: configure: %w", err)
	}
	i.mu.Lock()
	i.shapes = shapes
	i.cache = identifyCache{}
	i.mu.Unlock()
	return nil
}

// buildShapes returns shapes in lexicographic name order; date_format is
// validated to include a year component.
func buildShapes(cfg config) ([]*shape, error) {
	if len(cfg.Shapes) == 0 {
		return nil, fmt.Errorf("no shapes defined")
	}
	names := make([]string, 0, len(cfg.Shapes))
	for n := range cfg.Shapes {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]*shape, 0, len(names))
	for _, name := range names {
		sc := cfg.Shapes[name]
		s, err := validateShape(name, sc)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// validateShape validates sc and returns a compiled shape. The date_format
// must include a year component (year-less layouts produce ambiguous dates).
func validateShape(name string, sc shapeConfig) (*shape, error) {
	if sc.DateCol == "" {
		return nil, fmt.Errorf("shape %q: date_col is required", name)
	}
	if sc.DateFormat == "" {
		return nil, fmt.Errorf("shape %q: date_format is required", name)
	}
	// year required: year-less layouts produce ambiguous beancount dates.
	if t, err := time.Parse(sc.DateFormat, sc.DateFormat); err != nil || t.Year() != 2006 {
		return nil, fmt.Errorf("shape %q: date_format %q does not parse the Go reference time (must include year)", name, sc.DateFormat)
	}
	if len(sc.Amount) == 0 {
		return nil, fmt.Errorf("shape %q: at least one [[amount]] entry is required", name)
	}
	for i, a := range sc.Amount {
		if a.Col == "" {
			return nil, fmt.Errorf("shape %q: amount[%d].col is required", name, i)
		}
	}

	s := &shape{
		name:          name,
		delimiter:     ',',
		skipLines:     sc.SkipLines,
		dateCol:       sc.DateCol,
		dateFormat:    sc.DateFormat,
		payeeCol:      sc.PayeeCol,
		currencyCol:   sc.CurrencyCol,
		defaultCur:    sc.DefaultCurrency,
		narrationCols: sc.NarrationCols,
		narrationSep:  sc.NarrationSeparator,
		account:       sc.Account,
		amounts:       sc.Amount,
	}

	if sc.Match != "" {
		re, err := regexp.Compile(sc.Match)
		if err != nil {
			return nil, fmt.Errorf("shape %q: match: %w", name, err)
		}
		s.compiledMatch = re
	}
	if sc.Delimiter != "" {
		r, size := utf8.DecodeRuneInString(sc.Delimiter)
		if r == utf8.RuneError || size != len(sc.Delimiter) {
			return nil, fmt.Errorf("shape %q: delimiter %q must be exactly one rune", name, sc.Delimiter)
		}
		s.delimiter = r
	}
	return s, nil
}
