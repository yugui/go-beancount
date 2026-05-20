package csvimp

import (
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/yugui/go-beancount/pkg/importer"
)

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

// newImporter is the factory function registered under kind "csv". It returns
// one [*Importer] bound to name, or (nil, err) with the error prefixed
// "csvimp: configure: " on any failure.
func newImporter(name string, decode func(dest any) error) (importer.Importer, error) {
	if decode == nil {
		return nil, fmt.Errorf("csvimp: configure: nil decoder")
	}
	var sc shapeConfig
	if err := decode(&sc); err != nil {
		return nil, fmt.Errorf("csvimp: configure: %w", err)
	}
	s, err := validateShape(name, sc)
	if err != nil {
		return nil, fmt.Errorf("csvimp: configure: %w", err)
	}
	return &Importer{name: name, s: s}, nil
}

// validateShape validates sc and returns a compiled shape. date_format must
// contain a year component.
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
