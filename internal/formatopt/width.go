package formatopt

import "github.com/mattn/go-runewidth"

// StringWidth returns the display width of s, accounting for East Asian
// character widths. The eaAmbiguousWidth parameter controls the width of
// East Asian Ambiguous characters (1 or 2).
func StringWidth(s string, eaAmbiguousWidth int) int {
	cond := runewidth.NewCondition()
	cond.EastAsianWidth = eaAmbiguousWidth >= 2
	return cond.StringWidth(s)
}
