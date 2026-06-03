package csvkit

import "regexp"

// RowFilter reports whether a parsed record should be dropped before it
// becomes a directive — used to skip footnotes, totals, and similar
// non-data rows. A nil regexp is never constructed by this package's
// helpers; implementations must be safe for concurrent use.
type RowFilter interface {
	// Skip reports whether the record should be dropped. fields are the
	// raw row cells; get resolves a cell by column name.
	Skip(fields []string, get Get) bool
}

// ExcludeMatching drops a record when the named column matches re. Use it
// for marker columns, such as a "Type" column whose value is "Total".
func ExcludeMatching(col string, re *regexp.Regexp) RowFilter {
	return excludeCol{col: col, re: re}
}

type excludeCol struct {
	col string
	re  *regexp.Regexp
}

func (f excludeCol) Skip(_ []string, get Get) bool {
	return f.re.MatchString(get(f.col))
}

// ExcludeAnyField drops a record when any of its cells matches re. Use it
// for footnote rows (for example a leading "※") that do not align to a
// declared column.
func ExcludeAnyField(re *regexp.Regexp) RowFilter {
	return excludeAny{re: re}
}

type excludeAny struct{ re *regexp.Regexp }

func (f excludeAny) Skip(fields []string, _ Get) bool {
	for _, v := range fields {
		if f.re.MatchString(v) {
			return true
		}
	}
	return false
}
