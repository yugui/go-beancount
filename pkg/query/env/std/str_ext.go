package std

import (
	"regexp"
	"strings"

	"github.com/yugui/go-beancount/pkg/query/types"
)

// These string utilities mirror upstream beanquery. grepn and subst take the
// pattern first (matching upstream's signatures), like the older
// grep(pattern, string). Regex syntax and replacement references follow Go's
// regexp package (RE2; backreferences use $1, not \1), so patterns relying on
// Python-only features are not byte-for-byte compatible. Where upstream would
// raise on an out-of-range index, this port yields NULL instead of failing
// the query.
func init() {
	registerStrict("grepn", []types.Type{types.String, types.String, types.Int}, types.String, func(args []types.Value) (types.Value, error) {
		pattern, _ := types.AsString(args[0])
		s, _ := types.AsString(args[1])
		n, _ := types.AsInt(args[2])
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		m := re.FindStringSubmatch(s)
		if m == nil || n < 0 || int(n) >= len(m) {
			return types.Null(types.String), nil
		}
		return types.NewString(m[n]), nil
	})

	registerStrict("subst", []types.Type{types.String, types.String, types.String}, types.String, func(args []types.Value) (types.Value, error) {
		pattern, _ := types.AsString(args[0])
		repl, _ := types.AsString(args[1])
		s, _ := types.AsString(args[2])
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		return types.NewString(re.ReplaceAllString(s, repl)), nil
	})

	registerStrict("findfirst", []types.Type{types.String, types.SetType}, types.String, func(args []types.Value) (types.Value, error) {
		pattern, _ := types.AsString(args[0])
		set, _ := types.AsSet(args[1])
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		for _, v := range set.Elements() { // Elements is sorted ascending
			if loc := re.FindStringIndex(v); loc != nil && loc[0] == 0 {
				return types.NewString(v), nil
			}
		}
		return types.Null(types.String), nil
	})

	registerStrict("joinstr", []types.Type{types.SetType}, types.String, func(args []types.Value) (types.Value, error) {
		set, _ := types.AsSet(args[0])
		return types.NewString(strings.Join(set.Elements(), ",")), nil
	})

	registerStrict("maxwidth", []types.Type{types.String, types.Int}, types.String, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsString(args[0])
		n, _ := types.AsInt(args[1])
		return types.NewString(shorten(s, int(n))), nil
	})

	registerStrict("splitcomp", []types.Type{types.String, types.String, types.Int}, types.String, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsString(args[0])
		delim, _ := types.AsString(args[1])
		index, _ := types.AsInt(args[2])
		parts := strings.Split(s, delim)
		i := int(index)
		if i < 0 {
			i += len(parts) // Python-style negative indexing
		}
		if i < 0 || i >= len(parts) {
			return types.Null(types.String), nil
		}
		return types.NewString(parts[i]), nil
	})
}

// shorten approximates Python's textwrap.shorten: it collapses runs of
// whitespace to single spaces and, if the result still exceeds width, drops
// trailing words and appends " [...]" so the whole stays within width. When
// not even the first word fits, it returns just the "[...]" placeholder.
func shorten(s string, width int) string {
	const placeholder = " [...]"
	collapsed := strings.Join(strings.Fields(s), " ")
	if width < 0 {
		width = 0
	}
	if runeLen(collapsed) <= width {
		return collapsed
	}
	var b strings.Builder
	for _, w := range strings.Fields(collapsed) {
		cand := w
		if b.Len() > 0 {
			cand = " " + w
		}
		if runeLen(b.String()+cand)+runeLen(placeholder) > width {
			break
		}
		b.WriteString(cand)
	}
	if b.Len() == 0 {
		return strings.TrimLeft(placeholder, " ")
	}
	return b.String() + placeholder
}

func runeLen(s string) int { return len([]rune(s)) }
