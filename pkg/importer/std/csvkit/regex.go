package csvkit

import "regexp"

// NamedSubmatches applies re to s and returns its named capture groups.
// The bool is false when re does not match s. Groups that did not
// participate in the match map to ""; unnamed groups are ignored. It is
// the building block for splitting a single field (e.g. a combined
// payee/narration column) into named parts.
func NamedSubmatches(re *regexp.Regexp, s string) (map[string]string, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	names := re.SubexpNames()
	out := make(map[string]string, len(names))
	for i, name := range names {
		if name == "" {
			continue
		}
		out[name] = m[i]
	}
	return out, true
}
