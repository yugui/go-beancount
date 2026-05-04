package merge

import (
	"strings"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/format"
)

// patch carries one rendered insertion: the body bytes (one or more
// directive bodies joined by inter-insert padding), plus the leading
// and trailing extra bytes the merger contributes adjacent to the
// destination file's surrounding content.
//
// The applier composes the output as data[:offset] + leading + body +
// trailing + data[offset:]. Multiple inserts sharing one offset are
// merged into a single patch with a fused body.
type patch struct {
	offset   int
	leading  string
	body     string
	trailing string
}

// paddingFor returns the extra newline characters the merger
// contributes on one side of an insertion.
//
// b is Plan.InsertBlankLinesBetweenDirectives; n is
// Plan.BlankLinesBetweenDirectives; x is the count of blank lines
// already present on the existing side adjacent to the insertion. When
// b is false the merger contributes nothing. When b is true it
// contributes max(0, n-x) newlines, never reducing pre-existing blanks.
func paddingFor(b bool, n, x int) string {
	if !b {
		return ""
	}
	extra := n - x
	if extra <= 0 {
		return ""
	}
	return strings.Repeat("\n", extra)
}

// countTrailingBlanks returns the number of blank lines at the end of
// b. A blank line is a "\n" beyond the one terminating the preceding
// content line: e.g. "x\n" has zero, "x\n\n" has one, "x\n\n\n" has two.
// Both "\n" and "\r\n" line terminators are accepted on input.
func countTrailingBlanks(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	// Count terminators at the end. We strip "\r\n" and "\n" pairs from
	// the right; total terminators minus one (the terminator of the last
	// content line) is the number of blank lines. If the buffer is
	// terminator-only the result is total-1 as well, but we floor at 0.
	terms := 0
	i := len(b)
	for i > 0 {
		if i >= 2 && b[i-2] == '\r' && b[i-1] == '\n' {
			terms++
			i -= 2
			continue
		}
		if b[i-1] == '\n' {
			terms++
			i--
			continue
		}
		break
	}
	if terms == 0 {
		return 0
	}
	if i == 0 {
		// The buffer is all terminators. Each terminator is a blank line.
		return terms
	}
	// The last terminator belongs to the preceding content line; the
	// rest are blank lines.
	return terms - 1
}

// countLeadingBlanks returns the number of blank lines at the start of
// b. Symmetrically to countTrailingBlanks, a blank line is a leading
// terminator that is not paired with a preceding content line. Both
// "\n" and "\r\n" are accepted.
func countLeadingBlanks(b []byte) int {
	blanks := 0
	i := 0
	for i < len(b) {
		if i+1 < len(b) && b[i] == '\r' && b[i+1] == '\n' {
			blanks++
			i += 2
			continue
		}
		if b[i] == '\n' {
			blanks++
			i++
			continue
		}
		break
	}
	return blanks
}

// optsAsClosures converts a fully-resolved formatopt.Options back to a
// slice of format.Option closures. The merger needs this round-trip
// because printer.Fprint accepts only public format.Option values, but
// the merger must override the spacing fields after resolving the
// caller-supplied per-insert options. The returned slice carries every
// public field, so passing it to Fprint reproduces opts exactly.
func optsAsClosures(opts formatopt.Options) []format.Option {
	return []format.Option{
		format.WithCommaGrouping(opts.CommaGrouping),
		format.WithAlignAmounts(opts.AlignAmounts),
		format.WithAmountColumn(opts.AmountColumn),
		format.WithEastAsianAmbiguousWidth(opts.EastAsianAmbiguousWidth),
		format.WithIndentWidth(opts.IndentWidth),
		format.WithBlankLinesBetweenDirectives(opts.BlankLinesBetweenDirectives),
		format.WithInsertBlankLinesBetweenDirectives(opts.InsertBlankLinesBetweenDirectives),
	}
}

// applyPatches composes the output bytes by interleaving slices of
// data with each patch's leading + body + trailing text, in offset
// order. Patches must already be sorted by offset.
func applyPatches(data []byte, patches []patch) []byte {
	if len(patches) == 0 {
		return data
	}
	// Pre-size the output buffer.
	total := len(data)
	for _, p := range patches {
		total += len(p.leading) + len(p.body) + len(p.trailing)
	}
	out := make([]byte, 0, total)
	cursor := 0
	for _, p := range patches {
		out = append(out, data[cursor:p.offset]...)
		out = append(out, p.leading...)
		out = append(out, p.body...)
		out = append(out, p.trailing...)
		cursor = p.offset
	}
	out = append(out, data[cursor:]...)
	return out
}
