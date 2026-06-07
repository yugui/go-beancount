package csvkit

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/cockroachdb/apd/v3"
)

// NumberFormat describes the surface syntax of a numeric cell. The zero
// value is "plain": no thousands separator, '.' as the decimal point, and
// no placeholders — equivalent to feeding the trimmed cell straight to
// apd. All transformations are opt-in, so the zero value reproduces
// apd.BaseContext.SetString semantics (including rejecting "1,234").
type NumberFormat struct {
	// ThousandsSep, when non-empty, is removed from the cell before
	// parsing (e.g. "," turns "1,234" into "1234").
	ThousandsSep string

	// DecimalSep names the decimal point. Empty or "." leaves the cell
	// unchanged; any other value (e.g. ",") is normalised to "." after
	// thousands-separator removal.
	DecimalSep string

	// Placeholders lists cell values that denote "no value" rather than a
	// number. A cell equal (after trimming) to any entry parses as blank,
	// not as an error (e.g. "-").
	Placeholders []string
}

// ParseNumber parses s under f. The bool result is true when s is blank or
// matches a configured placeholder: callers treat that as "no value", not
// an error. A non-nil error means s held a non-blank, malformed number.
func ParseNumber(s string, f NumberFormat) (apd.Decimal, bool, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return apd.Decimal{}, true, nil
	}
	for _, p := range f.Placeholders {
		if t == p {
			return apd.Decimal{}, true, nil
		}
	}
	if f.ThousandsSep != "" {
		t = strings.ReplaceAll(t, f.ThousandsSep, "")
	}
	if f.DecimalSep != "" && f.DecimalSep != "." {
		t = strings.ReplaceAll(t, f.DecimalSep, ".")
	}
	var v apd.Decimal
	if _, _, err := apd.BaseContext.SetString(&v, t); err != nil {
		return apd.Decimal{}, false, err
	}
	return v, false, nil
}

// commodityRe matches a beancount commodity token: an uppercase letter,
// optionally followed by uppercase letters, digits, or the interior
// characters ' . _ - , and ending in a letter or digit.
var commodityRe = regexp.MustCompile(`^[A-Z][A-Z0-9'._-]*[A-Z0-9]$|^[A-Z]$`)

// SplitCurrencySuffix separates a trailing, whitespace-delimited currency
// token from a numeric prefix: "1,000 JPY" -> ("1,000", "JPY"); "1234" ->
// ("1234", ""). The suffix is split off only when it matches the beancount
// commodity grammar and a non-empty prefix remains; otherwise the trimmed
// input is returned with an empty currency. It never strips a sign or a
// thousands separator from the number.
func SplitCurrencySuffix(s string) (number, currency string) {
	t := strings.TrimSpace(s)
	i := strings.LastIndexFunc(t, unicode.IsSpace)
	if i < 0 {
		return t, ""
	}
	suffix := t[i+1:]
	prefix := strings.TrimSpace(t[:i])
	if prefix == "" || !commodityRe.MatchString(suffix) {
		return t, ""
	}
	return prefix, suffix
}

// AmountColumn names one cell contributing to a row's amount and whether
// its value is subtracted (Negate) rather than added.
type AmountColumn struct {
	Col    string
	Negate bool
}

// ValueAmount is one numeric contribution to an [AmountParser.SumValues] call,
// expressed as the already-resolved raw cell string (the parser still applies
// NumberFormat / SplitCurrency to it).
type ValueAmount struct {
	Value  string
	Negate bool
}

// Amount is the result of summing amount columns: the numeric total plus an
// optional currency observed in the cells. CurrencyHint is non-empty only
// when AmountParser.SplitCurrency is set and a consistent suffix was found.
type Amount struct {
	Number       apd.Decimal
	CurrencyHint string
}

// AmountStatus reports the outcome of [AmountParser.Sum] or
// [AmountParser.SumValues]. The zero value is not one of the named statuses
// and is never returned by Sum or SumValues.
type AmountStatus int

const (
	// AmountOK indicates at least one column held a parseable value.
	AmountOK AmountStatus = iota + 1
	// AmountBad indicates a non-blank entry failed to parse or currencies
	// conflicted across entries; the caller method documents how the offending
	// entry is identified.
	AmountBad
	// AmountAllBlank indicates every column was blank or a placeholder.
	AmountAllBlank
)

func (s AmountStatus) String() string {
	switch s {
	case AmountOK:
		return "AmountOK"
	case AmountBad:
		return "AmountBad"
	case AmountAllBlank:
		return "AmountAllBlank"
	default:
		return fmt.Sprintf("AmountStatus(%d)", int(s))
	}
}

// AmountParser sums one or more amount columns under a NumberFormat.
type AmountParser struct {
	Format NumberFormat

	// SplitCurrency, when set, extracts a trailing currency token from each
	// cell (see SplitCurrencySuffix) before numeric parsing and reports it
	// as the result's CurrencyHint. Conflicting currencies across cells are
	// an AmountBad error.
	SplitCurrency bool
}

// Sum adds each column's value (subtracting Negate columns), reading cells
// through value. Blank or placeholder cells contribute nothing. The result
// is AmountAllBlank when no column held a value, or AmountBad (with the
// offending column name) when a non-blank cell failed to parse or when
// SplitCurrency yielded conflicting currencies across cells.
func (p AmountParser) Sum(cols []AmountColumn, value func(col string) string) (Amount, AmountStatus, string) {
	var sum apd.Decimal
	allBlank := true
	currency := ""
	for _, a := range cols {
		raw := value(a.Col)
		if p.SplitCurrency {
			num, cur := SplitCurrencySuffix(raw)
			raw = num
			if cur != "" {
				if currency != "" && currency != cur {
					return Amount{}, AmountBad, a.Col
				}
				currency = cur
			}
		}
		v, blank, err := ParseNumber(raw, p.Format)
		if err != nil {
			return Amount{}, AmountBad, a.Col
		}
		if blank {
			continue
		}
		allBlank = false
		op := apd.BaseContext.Add
		if a.Negate {
			op = apd.BaseContext.Sub
		}
		if _, err := op(&sum, &sum, &v); err != nil {
			return Amount{}, AmountBad, a.Col
		}
	}
	if allBlank {
		return Amount{}, AmountAllBlank, ""
	}
	return Amount{Number: sum, CurrencyHint: currency}, AmountOK, ""
}

// SumValues is the value-keyed counterpart of [AmountParser.Sum]: callers pass
// already-resolved cell strings instead of column names. Semantics match Sum
// exactly (NumberFormat, SplitCurrency, conflict detection, AmountAllBlank
// handling). On AmountBad the int return is the 0-based index of the offending
// entry in values; on other statuses it is -1.
func (p AmountParser) SumValues(values []ValueAmount) (Amount, AmountStatus, int) {
	var sum apd.Decimal
	allBlank := true
	currency := ""
	for i, a := range values {
		raw := a.Value
		if p.SplitCurrency {
			num, cur := SplitCurrencySuffix(raw)
			raw = num
			if cur != "" {
				if currency != "" && currency != cur {
					return Amount{}, AmountBad, i
				}
				currency = cur
			}
		}
		v, blank, err := ParseNumber(raw, p.Format)
		if err != nil {
			return Amount{}, AmountBad, i
		}
		if blank {
			continue
		}
		allBlank = false
		op := apd.BaseContext.Add
		if a.Negate {
			op = apd.BaseContext.Sub
		}
		if _, err := op(&sum, &sum, &v); err != nil {
			return Amount{}, AmountBad, i
		}
	}
	if allBlank {
		return Amount{}, AmountAllBlank, -1
	}
	return Amount{Number: sum, CurrencyHint: currency}, AmountOK, -1
}
