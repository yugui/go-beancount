package csvkit

import (
	"fmt"
	"strings"

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

// AmountColumn names one cell contributing to a row's amount and whether
// its value is subtracted (Negate) rather than added.
type AmountColumn struct {
	Col    string
	Negate bool
}

// AmountStatus reports the outcome of [AmountParser.Sum].
type AmountStatus int

const (
	// AmountOK indicates at least one column held a parseable value.
	AmountOK AmountStatus = iota + 1
	// AmountBad indicates a non-blank column failed to parse; the
	// offending column name is returned alongside.
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
}

// Sum adds each column's value (subtracting Negate columns), reading cells
// through value. Blank or placeholder cells contribute nothing. The result
// is AmountAllBlank when no column held a value, or AmountBad (with the
// offending column name) when a non-blank cell failed to parse.
func (p AmountParser) Sum(cols []AmountColumn, value func(col string) string) (apd.Decimal, AmountStatus, string) {
	var sum apd.Decimal
	allBlank := true
	for _, a := range cols {
		v, blank, err := ParseNumber(value(a.Col), p.Format)
		if err != nil {
			return apd.Decimal{}, AmountBad, a.Col
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
			return apd.Decimal{}, AmountBad, a.Col
		}
	}
	if allBlank {
		return apd.Decimal{}, AmountAllBlank, ""
	}
	return sum, AmountOK, ""
}
