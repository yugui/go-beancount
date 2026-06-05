package csvimp

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// stringList is a TOML field that accepts either a single string or an
// array of strings; it always decodes as []string. Empty input yields a
// nil slice.
type stringList []string

// UnmarshalTOML implements the BurntSushi/toml Unmarshaler contract.
// It accepts a TOML string (decoded to []string{value}) or a TOML array
// whose elements are all strings (decoded element-wise).
func (s *stringList) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		*s = stringList{v}
	case []any:
		out := make(stringList, 0, len(v))
		for i, item := range v {
			str, ok := item.(string)
			if !ok {
				return fmt.Errorf("element %d: expected string, got %T", i, item)
			}
			out = append(out, str)
		}
		*s = out
	default:
		return fmt.Errorf("expected string or array of strings, got %T", data)
	}
	return nil
}

// shapeConfig is the on-disk shape of a csvimp TOML configuration. It is
// decoded by the factory and immediately compiled into a [shape].
type shapeConfig struct {
	Match          string          `toml:"match"`
	Delimiter      string          `toml:"delimiter"`
	SkipLines      int             `toml:"skip_lines"`
	HeaderMatch    stringList      `toml:"header_match"`
	Columns        map[string]int  `toml:"columns"`
	Encoding       string          `toml:"encoding"`
	Number         numberConfig    `toml:"number"`
	Date           dateConfig      `toml:"date"`
	Account        accountConfig   `toml:"account"`
	CounterAccount accountConfig   `toml:"counter_account"`
	Payee          payeeConfig     `toml:"payee"`
	Currency       currencyConfig  `toml:"currency"`
	Narration      narrationConfig `toml:"narration"`
	Split          splitConfig     `toml:"split"`
	Amount         []amountColumn  `toml:"amount"`
	Exclude        []excludeConfig `toml:"exclude"`
}

// excludeConfig is one [[exclude]] rule: Match (required) is a regular
// expression tested against Col when set, otherwise against every cell.
type excludeConfig struct {
	Col   string `toml:"col"`
	Match string `toml:"match"`
}

type dateConfig struct {
	Col    string `toml:"col"`
	Format string `toml:"format"`
}

// numberConfig tunes amount parsing; an absent block parses amounts as
// apd does (commas rejected, '.' decimal point).
type numberConfig struct {
	ThousandsSep string     `toml:"thousands_sep"`
	DecimalSep   string     `toml:"decimal_sep"`
	Placeholders stringList `toml:"placeholders"`
}

// accountConfig is the on-disk shape of an account-selecting block
// (used by both [account] and [counter_account]). Col accepts either a
// single column name or a list; Separator joins multi-column values
// before map lookup and is ignored otherwise.
type accountConfig struct {
	Col       stringList        `toml:"col"`
	Separator string            `toml:"separator"`
	Default   string            `toml:"default"`
	Map       map[string]string `toml:"map"`
}

type payeeConfig struct {
	Col       stringList        `toml:"col"`
	Separator string            `toml:"separator"`
	Map       map[string]string `toml:"map"`
}

type currencyConfig struct {
	Col        string            `toml:"col"`
	Default    string            `toml:"default"`
	FromAmount bool              `toml:"from_amount"`
	Map        map[string]string `toml:"map"`
}

type narrationConfig struct {
	Col       stringList        `toml:"col"`
	Separator string            `toml:"separator"`
	Map       map[string]string `toml:"map"`
	Template  string            `toml:"template"`
}

// splitConfig is the on-disk shape of the optional [split] block: Pattern
// is a regular expression whose named capture groups become synthetic
// columns extracted from Col, referenceable by any field.
type splitConfig struct {
	Col     string `toml:"col"`
	Pattern string `toml:"pattern"`
}

// amountColumn is the TOML decode target for one [[amount]] entry. It
// mirrors csvkit.AmountColumn but stays local so csvkit carries no TOML
// tags; validateShape converts between the two.
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

	// headerMatch locates the header past a variable banner; nil for a
	// fixed header. columns is non-nil only for headerless input, where
	// it is both the reader's headerless trigger and the column index.
	headerMatch func([]string) bool
	columns     map[string]int

	// inputEncoding decodes file bytes to UTF-8 before CSV parsing.
	// nil means "no transformation"; bytes flow through verbatim
	// (the legacy UTF-8 / ASCII-compatible path).
	inputEncoding encoding.Encoding

	dateCol    string
	dateFormat string

	// account resolution. accountMap == nil means "no translation table
	// configured"; the column cell (when present and non-blank) is then
	// used verbatim. A non-nil accountMap enables strict mode: a cell
	// value absent from the map yields DiagUnmappedAccount.
	accountCols    []string
	accountSep     string
	accountDefault string
	accountMap     map[string]string

	// counter_account resolution. Same semantics as account, but
	// Hints["account"] is not consulted and an empty result with no
	// default silently suppresses the second posting (rather than
	// emitting a diagnostic). counterAccountCols == nil && counterAccountDefault
	// == "" means counter_account is unconfigured.
	counterAccountCols    []string
	counterAccountSep     string
	counterAccountDefault string
	counterAccountMap     map[string]string

	payeeCols []string
	payeeSep  string
	payeeMap  map[string]string

	currencyCol        string
	currencyDefault    string
	currencyFromAmount bool
	currencyMap        map[string]string

	narrationCols     []string
	narrationSep      string
	narrationMap      map[string]string
	narrationTemplate *csvkit.NarrationTemplate // nil unless [narration].template set

	amounts      []csvkit.AmountColumn
	numberFormat csvkit.NumberFormat

	// split, when non-nil, extracts named capture groups from a source
	// column into synthetic columns referenceable by any field.
	split *splitRule

	// filters drop statement noise (footnotes, totals) before a row
	// becomes a directive; empty means no filtering.
	filters []csvkit.RowFilter
}

// splitRule is the compiled form of [split]: re's named groups become
// synthetic columns drawn from col. groups holds those names so they are
// not mistaken for required header columns.
type splitRule struct {
	col    string
	re     *regexp.Regexp
	groups map[string]bool
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

	if err := validateAccountSection(name, "account", sc.Account, false); err != nil {
		return nil, err
	}
	if err := validateAccountSection(name, "counter_account", sc.CounterAccount, true); err != nil {
		return nil, err
	}

	if len(sc.Payee.Col) == 0 && len(sc.Payee.Map) != 0 {
		return nil, fmt.Errorf("shape %q: [payee.map] is set but [payee].col is not; the map would never be consulted", name)
	}

	if sc.Currency.Col == "" && sc.Currency.Default == "" && !sc.Currency.FromAmount {
		return nil, fmt.Errorf("shape %q: [currency] requires col, default, or from_amount", name)
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

	if len(sc.Narration.Col) == 0 && len(sc.Narration.Map) != 0 {
		return nil, fmt.Errorf("shape %q: [narration.map] is set but [narration].col is empty; the map would never be consulted", name)
	}
	if len(sc.Narration.Col) > 0 && sc.Narration.Template != "" {
		return nil, fmt.Errorf("shape %q: [narration].col and [narration].template are mutually exclusive", name)
	}

	if sc.Split.Pattern == "" && sc.Split.Col != "" {
		return nil, fmt.Errorf("shape %q: [split].col is set without [split].pattern", name)
	}
	if sc.Split.Pattern != "" && sc.Split.Col == "" {
		return nil, fmt.Errorf("shape %q: [split].pattern requires [split].col", name)
	}

	if len(sc.Amount) == 0 {
		return nil, fmt.Errorf("shape %q: at least one [[amount]] entry is required", name)
	}
	amounts := make([]csvkit.AmountColumn, len(sc.Amount))
	for i, a := range sc.Amount {
		if a.Col == "" {
			return nil, fmt.Errorf("shape %q: amount[%d].col is required", name, i)
		}
		amounts[i] = csvkit.AmountColumn{Col: a.Col, Negate: a.Negate}
	}

	if sc.Number.DecimalSep != "" && sc.Number.DecimalSep == sc.Number.ThousandsSep {
		return nil, fmt.Errorf("shape %q: [number].decimal_sep and [number].thousands_sep must differ", name)
	}

	hasColumns := len(sc.Columns) > 0
	hasHeaderMatch := len(sc.HeaderMatch) > 0
	if hasColumns && hasHeaderMatch {
		return nil, fmt.Errorf("shape %q: columns (headerless) and header_match are mutually exclusive", name)
	}
	for _, c := range sc.HeaderMatch {
		if strings.TrimSpace(c) == "" {
			return nil, fmt.Errorf("shape %q: header_match contains a blank column name", name)
		}
	}
	for col, i := range sc.Columns {
		if i < 0 {
			return nil, fmt.Errorf("shape %q: [columns][%q] = %d must be non-negative", name, col, i)
		}
	}
	var columns map[string]int
	if hasColumns {
		columns = sc.Columns
	}
	var headerMatch func([]string) bool
	if hasHeaderMatch {
		headerMatch = headerMatcher([]string(sc.HeaderMatch))
	}

	// nil == "no map configured" (see shape.accountMap doc).
	s := &shape{
		delimiter:             ',',
		skipLines:             sc.SkipLines,
		headerMatch:           headerMatch,
		columns:               columns,
		dateCol:               sc.Date.Col,
		dateFormat:            sc.Date.Format,
		accountCols:           []string(sc.Account.Col),
		accountSep:            sc.Account.Separator,
		accountDefault:        sc.Account.Default,
		accountMap:            nilIfEmpty(sc.Account.Map),
		counterAccountCols:    []string(sc.CounterAccount.Col),
		counterAccountSep:     sc.CounterAccount.Separator,
		counterAccountDefault: sc.CounterAccount.Default,
		counterAccountMap:     nilIfEmpty(sc.CounterAccount.Map),
		payeeCols:             []string(sc.Payee.Col),
		payeeSep:              sc.Payee.Separator,
		payeeMap:              nilIfEmpty(sc.Payee.Map),
		currencyCol:           sc.Currency.Col,
		currencyDefault:       sc.Currency.Default,
		currencyFromAmount:    sc.Currency.FromAmount,
		currencyMap:           nilIfEmpty(sc.Currency.Map),
		narrationCols:         []string(sc.Narration.Col),
		narrationSep:          sc.Narration.Separator,
		narrationMap:          nilIfEmpty(sc.Narration.Map),
		amounts:               amounts,
		numberFormat: csvkit.NumberFormat{
			ThousandsSep: sc.Number.ThousandsSep,
			DecimalSep:   sc.Number.DecimalSep,
			Placeholders: []string(sc.Number.Placeholders),
		},
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
	if sc.Encoding != "" {
		enc, err := ianaindex.IANA.Encoding(sc.Encoding)
		if err != nil {
			return nil, fmt.Errorf("shape %q: encoding %q is not a recognised IANA charset name: %w", name, sc.Encoding, err)
		}
		if enc == nil {
			return nil, fmt.Errorf("shape %q: encoding %q is not a recognised IANA charset name", name, sc.Encoding)
		}
		s.inputEncoding = enc
	}
	for i, e := range sc.Exclude {
		if e.Match == "" {
			return nil, fmt.Errorf("shape %q: [[exclude]][%d].match is required", name, i)
		}
		re, err := regexp.Compile(e.Match)
		if err != nil {
			return nil, fmt.Errorf("shape %q: [[exclude]][%d].match: %w", name, i, err)
		}
		if e.Col != "" {
			s.filters = append(s.filters, csvkit.ExcludeMatching(e.Col, re))
		} else {
			s.filters = append(s.filters, csvkit.ExcludeAnyField(re))
		}
	}
	if sc.Narration.Template != "" {
		nt, err := csvkit.CompileNarration(sc.Narration.Template)
		if err != nil {
			return nil, fmt.Errorf("shape %q: [narration].template: %w", name, err)
		}
		s.narrationTemplate = nt
	}
	if sc.Split.Pattern != "" {
		re, err := regexp.Compile(sc.Split.Pattern)
		if err != nil {
			return nil, fmt.Errorf("shape %q: [split].pattern: %w", name, err)
		}
		groups := namedGroups(re)
		if len(groups) == 0 {
			return nil, fmt.Errorf("shape %q: [split].pattern has no named capture groups", name)
		}
		s.split = &splitRule{col: sc.Split.Col, re: re, groups: groups}
	}
	return s, nil
}

// namedGroups returns the set of named capture groups declared by re.
func namedGroups(re *regexp.Regexp) map[string]bool {
	out := map[string]bool{}
	for _, n := range re.SubexpNames() {
		if n != "" {
			out[n] = true
		}
	}
	return out
}

// validateAccountSection enforces the common rules for an [account]-like
// block (account, counter_account). When optional is true an entirely
// empty block (no Col and no Default) is accepted without error; the
// section is then treated as unconfigured by extract.
func validateAccountSection(name, section string, cfg accountConfig, optional bool) error {
	hasCol := len(cfg.Col) > 0
	hasDefault := cfg.Default != ""
	hasMap := len(cfg.Map) > 0

	if !hasCol && !hasDefault {
		if optional && !hasMap {
			return nil
		}
		if !optional {
			return fmt.Errorf("shape %q: [%s] requires col or default", name, section)
		}
	}
	if hasCol && !hasMap && !hasDefault {
		return fmt.Errorf("shape %q: [%s].col without map or default would leave every row unresolved", name, section)
	}
	if !hasCol && hasMap {
		return fmt.Errorf("shape %q: [%s.map] is set but [%s].col is not; the map would never be consulted", name, section, section)
	}
	if hasDefault && !ast.Account(cfg.Default).IsValid() {
		return fmt.Errorf("shape %q: [%s].default %q is not a valid beancount account", name, section, cfg.Default)
	}
	for k, v := range cfg.Map {
		if !ast.Account(v).IsValid() {
			return fmt.Errorf("shape %q: [%s.map][%q] = %q is not a valid beancount account", name, section, k, v)
		}
	}
	return nil
}

// headerMatcher returns a predicate accepting any row that contains every
// name in required (compared after trimming), used to locate a header that
// follows a variable-length banner.
func headerMatcher(required []string) func([]string) bool {
	return func(row []string) bool {
		present := make(map[string]bool, len(row))
		for _, c := range row {
			present[strings.TrimSpace(c)] = true
		}
		for _, r := range required {
			if !present[r] {
				return false
			}
		}
		return true
	}
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
