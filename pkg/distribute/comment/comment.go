package comment

import (
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Block describes a contiguous run of `;`-prefixed lines that successfully
// parsed (with the prefix stripped) to at least one directive.
//
// StartLine is the zero-indexed line number of the block's first line in
// the original source. EndLine is exclusive and reflects the shrunk-to
// length: if the candidate had N lines and only the first K parsed,
// EndLine == StartLine + K. Any remaining N-K lines stay in the source as
// ordinary comment text.
//
// Indent is the literal byte sequence shared by every line of the
// recognized block (a `;` followed by zero or more space and tab bytes).
// Body is the joined-by-`\n` text of the K parsed lines with Indent
// stripped — exactly the string passed to ast.Load. Directive is the first
// directive of the lowered Body; if the body parsed to multiple
// directives, only the first is recorded.
type Block struct {
	SourcePath string
	StartLine  int
	EndLine    int
	Indent     string
	Body       string
	Directive  ast.Directive
}

// Extract scans src for commented-out directives and returns one Block per
// recognized run. The path argument is propagated to each result's
// SourcePath verbatim.
//
// See the package documentation for the recognition rules and the
// tail-shrink behavior.
func Extract(src, path string) []Block {
	lines := splitLines(src)
	var out []Block
	for i := 0; i < len(lines); {
		indent, ok := candidatePrefix(lines[i])
		if !ok {
			i++
			continue
		}
		end := i + 1
		for end < len(lines) && strings.HasPrefix(lines[end], indent) {
			end++
		}
		if b, ok := tryParse(lines[i:end], indent, path, i); ok {
			out = append(out, b)
		}
		// Resume after the entire candidate, not after the shrunk-to length;
		// dropped tail lines stay as plain comments and shouldn't seed a new
		// candidate from inside the same block.
		i = end
	}
	return out
}

// splitLines returns the lines of s with their terminators removed. Both
// `\n` and `\r\n` are accepted; the `\r` of a `\r\n` terminator is stripped
// from the line content. A non-empty trailing line without a terminator is
// included as a final element. A bare empty source returns nil.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		end := i
		if end > start && s[end-1] == '\r' {
			end--
		}
		out = append(out, s[start:end])
		start = i + 1
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// candidatePrefix returns the `;`+whitespace prefix of line if it begins a
// candidate block (a `;`, optional spaces and tabs, and a YYYY-MM-DD-shaped
// header). The second return is false otherwise.
func candidatePrefix(line string) (string, bool) {
	if line == "" || line[0] != ';' {
		return "", false
	}
	j := 1
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if !hasDateShape(line[j:]) {
		return "", false
	}
	return line[:j], true
}

// hasDateShape reports whether s starts with ten ASCII bytes matching
// YYYY-MM-DD (four digits, dash, two digits, dash, two digits). It is only
// a shape check; the parser later rejects out-of-range months and days.
func hasDateShape(s string) bool {
	if len(s) < 10 {
		return false
	}
	for _, k := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if s[k] < '0' || s[k] > '9' {
			return false
		}
	}
	return s[4] == '-' && s[7] == '-'
}

// tryParse strips indent from each line of block and tries successively
// shorter prefixes through ast.Load until one yields at least one
// directive. The candidate is dropped (returns false) iff every prefix
// length from N down to 1 fails to lower a directive.
//
// The shrink loop is defensive: ast.Load's lower stage recovers
// per-directive on most malformed input, so in practice the K=N attempt
// almost always wins for any candidate that contains a parseable leading
// directive. The loop guards against regressions in parser recovery — if
// ast.Load ever returns Len()==0 for an otherwise-valid leading directive
// followed by garbage, the leading directive is still recoverable from
// some smaller K.
func tryParse(block []string, indent, path string, startLine int) (Block, bool) {
	stripped := make([]string, len(block))
	for k, line := range block {
		stripped[k] = line[len(indent):]
	}
	for k := len(stripped); k >= 1; k-- {
		body := strings.Join(stripped[:k], "\n")
		ledger, err := ast.Load(body, ast.WithBaseDir(""))
		if err != nil {
			continue
		}
		if ledger.Len() >= 1 {
			return Block{
				SourcePath: path,
				StartLine:  startLine,
				EndLine:    startLine + k,
				Indent:     indent,
				Body:       body,
				Directive:  ledger.At(0),
			}, true
		}
	}
	return Block{}, false
}
