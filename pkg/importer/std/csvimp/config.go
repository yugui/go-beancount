package csvimp

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
)

// shapeConfig is the on-disk shape of a csvimp TOML configuration. It is
// decoded by the factory and immediately compiled into a [shape].
type shapeConfig struct {
	Match     string          `toml:"match"`
	Delimiter string          `toml:"delimiter"`
	SkipLines int             `toml:"skip_lines"`
	Date      dateConfig      `toml:"date"`
	Account   accountConfig   `toml:"account"`
	Payee     payeeConfig     `toml:"payee"`
	Currency  currencyConfig  `toml:"currency"`
	Narration narrationConfig `toml:"narration"`
	Amount    []amountColumn  `toml:"amount"`
}

type dateConfig struct {
	Col    string `toml:"col"`
	Format string `toml:"format"`
}

type accountConfig struct {
	Col     string            `toml:"col"`
	Default string            `toml:"default"`
	Map     map[string]string `toml:"map"`
}

type payeeConfig struct {
	Col string            `toml:"col"`
	Map map[string]string `toml:"map"`
}

type currencyConfig struct {
	Col     string            `toml:"col"`
	Default string            `toml:"default"`
	Map     map[string]string `toml:"map"`
}

type narrationConfig struct {
	Cols      []string          `toml:"cols"`
	Separator string            `toml:"separator"`
	Map       map[string]string `toml:"map"`
}

type amountColumn struct {
	Col    string `toml:"col"`
	Negate bool   `toml:"negate"`
}

// shape is the validated, compiled form of [shapeConfig]. All fields are
// frozen after construction; the value is safe for concurrent use.
type shape struct {
	compiledMatch *regexp.Regexp // nil when Match was unset
	delimiter     rune           // default ','
	skipLines     int

	dateCol    string
	dateFormat string

	// account resolution. accountMap == nil means "no translation table
	// configured"; the column cell (when present and non-blank) is then
	// used verbatim. A non-nil accountMap enables strict mode: a cell
	// value absent from the map yields DiagUnmappedAccount.
	accountCol     string
	accountDefault string
	accountMap     map[string]string

	payeeCol string
	payeeMap map[string]string

	currencyCol     string
	currencyDefault string
	currencyMap     map[string]string

	narrationCols []string
	narrationSep  string
	narrationMap  map[string]string

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

// validateShape validates sc and returns a compiled shape. The TOML paths
// quoted in error messages match the user-facing schema (e.g. [account.map]
// rather than the Go field path).
func validateShape(name string, sc shapeConfig) (*shape, error) {
	if sc.Date.Col == "" {
		return nil, fmt.Errorf("shape %q: [date].col is required", name)
	}
	if sc.Date.Format == "" {
		return nil, fmt.Errorf("shape %q: [date].format is required", name)
	}
	// year/month/day required: shorter layouts produce ambiguous beancount dates.
	if t, err := time.Parse(sc.Date.Format, sc.Date.Format); err != nil || t.Year() != 2006 || t.Month() != time.January || t.Day() != 2 {
		return nil, fmt.Errorf(`shape %q: [date].format %q must include year, month and day expressed against the layout reference date Jan 2, 2006 (for example "2006-01-02" or "02/01/2006")`, name, sc.Date.Format)
	}

	if sc.Account.Col == "" && sc.Account.Default == "" {
		return nil, fmt.Errorf("shape %q: [account] requires col or default", name)
	}
	if sc.Account.Col != "" && len(sc.Account.Map) == 0 && sc.Account.Default == "" {
		return nil, fmt.Errorf("shape %q: [account].col without map or default would leave every row unresolved", name)
	}
	if sc.Account.Col == "" && len(sc.Account.Map) != 0 {
		return nil, fmt.Errorf("shape %q: [account.map] is set but [account].col is not; the map would never be consulted", name)
	}
	if sc.Account.Default != "" && !ast.Account(sc.Account.Default).IsValid() {
		return nil, fmt.Errorf("shape %q: [account].default %q is not a valid beancount account", name, sc.Account.Default)
	}
	for k, v := range sc.Account.Map {
		if !ast.Account(v).IsValid() {
			return nil, fmt.Errorf("shape %q: [account.map][%q] = %q is not a valid beancount account", name, k, v)
		}
	}

	if sc.Payee.Col == "" && len(sc.Payee.Map) != 0 {
		return nil, fmt.Errorf("shape %q: [payee.map] is set but [payee].col is not; the map would never be consulted", name)
	}

	if sc.Currency.Col == "" && sc.Currency.Default == "" {
		return nil, fmt.Errorf("shape %q: [currency] requires col or default", name)
	}
	if sc.Currency.Default != "" && strings.TrimSpace(sc.Currency.Default) == "" {
		return nil, fmt.Errorf("shape %q: [currency].default is blank", name)
	}
	if sc.Currency.Col == "" && len(sc.Currency.Map) != 0 {
		return nil, fmt.Errorf("shape %q: [currency.map] is set but [currency].col is not; the map would never be consulted", name)
	}
	for k, v := range sc.Currency.Map {
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("shape %q: [currency.map][%q] maps to a blank value", name, k)
		}
	}

	if len(sc.Narration.Cols) == 0 && len(sc.Narration.Map) != 0 {
		return nil, fmt.Errorf("shape %q: [narration.map] is set but [narration].cols is empty; the map would never be consulted", name)
	}

	if len(sc.Amount) == 0 {
		return nil, fmt.Errorf("shape %q: at least one [[amount]] entry is required", name)
	}
	for i, a := range sc.Amount {
		if a.Col == "" {
			return nil, fmt.Errorf("shape %q: amount[%d].col is required", name, i)
		}
	}

	// nil == "no map configured" (see shape.accountMap doc).
	s := &shape{
		delimiter:       ',',
		skipLines:       sc.SkipLines,
		dateCol:         sc.Date.Col,
		dateFormat:      sc.Date.Format,
		accountCol:      sc.Account.Col,
		accountDefault:  sc.Account.Default,
		accountMap:      nilIfEmpty(sc.Account.Map),
		payeeCol:        sc.Payee.Col,
		payeeMap:        nilIfEmpty(sc.Payee.Map),
		currencyCol:     sc.Currency.Col,
		currencyDefault: sc.Currency.Default,
		currencyMap:     nilIfEmpty(sc.Currency.Map),
		narrationCols:   sc.Narration.Cols,
		narrationSep:    sc.Narration.Separator,
		narrationMap:    nilIfEmpty(sc.Narration.Map),
		amounts:         sc.Amount,
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

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
