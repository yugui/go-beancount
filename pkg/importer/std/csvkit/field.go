package csvkit

import "strings"

// Get returns the value of a named column for the current record, or ""
// when the column is absent or the row is too short. Whether the returned
// value is trimmed is the caller's choice.
type Get func(col string) string

// Join trims each named column's cell, drops the blanks, and joins the
// survivors with sep. It returns "" when cols is empty or every cell is
// blank.
func Join(cols []string, sep string, get Get) string {
	if len(cols) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		if v := strings.TrimSpace(get(c)); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, sep)
}

// MapMode selects how [ResolveThroughMap] treats a key absent from a
// translation map.
type MapMode int

const (
	// Verbatim returns an absent key unchanged (pass-through).
	Verbatim MapMode = iota
	// Strict reports an absent key as unresolved.
	Strict
)

// ResolveThroughMap translates key through m. A hit returns (value, true).
// On a miss the result depends on mode: Verbatim returns (key, true);
// Strict returns ("", false). A nil or empty m carries no translation, so
// every lookup is a miss and the mode alone decides the outcome.
func ResolveThroughMap(key string, m map[string]string, mode MapMode) (string, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	if mode == Strict {
		return "", false
	}
	return key, true
}
