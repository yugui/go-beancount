package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// ContextKind classifies the completion context at a cursor position.
type ContextKind int

const (
	// ContextUnknown means no specific completion context was identified.
	ContextUnknown ContextKind = iota
	// ContextAccount means the cursor is on a partial account name.
	ContextAccount
	// ContextCurrency means the cursor is on a partial currency token.
	ContextCurrency
	// ContextKeyword means the cursor is at a directive keyword position.
	ContextKeyword
	// ContextFlag means the cursor is at a transaction flag position.
	ContextFlag
	// ContextTag means the cursor is on a partial #tag token.
	ContextTag
	// ContextLink means the cursor is on a partial ^link token.
	ContextLink
	// ContextInString means the cursor is inside a string literal.
	ContextInString
	// ContextPayee means the cursor is in the payee string (first quoted string) of a transaction header line.
	ContextPayee
	// ContextNarration means the cursor is inside the narration string of a
	// transaction header (the second quoted string).
	ContextNarration
	// ContextPayeeOrNarration means the cursor is inside the first quoted string
	// of a transaction header and no second string follows, so the value may
	// become either the payee (if a second string is added) or the narration (a
	// lone string is the narration). Both candidate sets are offered.
	ContextPayeeOrNarration
	// ContextMetaKey means the cursor is on an indented metadata key being typed.
	ContextMetaKey
	// ContextMetaValue means the cursor is on the value side of a metadata line
	// (the key has already been typed and the colon separator is present).
	ContextMetaValue
)

// String returns the human-readable name of k.
func (k ContextKind) String() string {
	switch k {
	case ContextUnknown:
		return "Unknown"
	case ContextAccount:
		return "Account"
	case ContextCurrency:
		return "Currency"
	case ContextKeyword:
		return "Keyword"
	case ContextFlag:
		return "Flag"
	case ContextTag:
		return "Tag"
	case ContextLink:
		return "Link"
	case ContextInString:
		return "InString"
	case ContextPayee:
		return "Payee"
	case ContextNarration:
		return "Narration"
	case ContextPayeeOrNarration:
		return "PayeeOrNarration"
	case ContextMetaKey:
		return "MetaKey"
	case ContextMetaValue:
		return "MetaValue"
	default:
		return fmt.Sprintf("ContextKind(%d)", int(k))
	}
}

var (
	reDatePrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+`)

	// reTagToken matches a #tag token preceded by space or BOL anywhere on the line.
	reTagToken = regexp.MustCompile(`(?:^|\s)#[A-Za-z0-9._-]*$`)
	// reLinkToken matches a ^link token preceded by space or BOL anywhere on the line.
	reLinkToken = regexp.MustCompile(`(?:^|\s)\^[A-Za-z0-9._-]*$`)

	// reCurrencyToken: uppercase-only token (no colon).
	reCurrencyToken = regexp.MustCompile(`[A-Z][A-Z0-9]*$`)

	// reTxnHeader matches a transaction header prefix: date followed by a flag
	// (* or !) or the keyword "txn".
	reTxnHeader = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+(?:\*|!|txn)\s`)

	// rePartialKeyword matches a token of all lowercase letters with no other
	// characters, treated as a directive keyword the user is part-way through
	// typing (e.g. "o", "op", "ope"). Digits, uppercase, and punctuation are
	// excluded so currency- and account-like tokens stay on their own paths.
	rePartialKeyword = regexp.MustCompile(`^[a-z]+$`)

	// reMetaKey matches an indented line where only the key is being typed (no colon yet).
	reMetaKey = regexp.MustCompile(`^\s+[a-z][a-z0-9_-]*$`)
	// reMetaValue matches an indented line with key: value — captures (key, value-prefix).
	reMetaValue = regexp.MustCompile(`^\s+([a-z][a-z0-9_-]*):\s*(.*)$`)
)

// classifyContext returns the completion context for the cursor at the end of
// linePrefix (the bytes from start of line to cursor, exclusive). The heuristic
// is lexical only; when ambiguous it returns ContextUnknown, which produces
// no completions rather than wrong ones.
//
// For transaction header lines (date + flag/txn keyword), quote counting
// distinguishes payee from narration: 1 quote → ContextPayee (cursor is in
// the first string; whether a second string follows is unknown at this point),
// 3 quotes → ContextNarration (cursor is in the second string).
func classifyContext(linePrefix string) ContextKind {
	// must precede the generic odd-quote InString check
	if reTxnHeader.MatchString(linePrefix) {
		n := strings.Count(linePrefix, `"`)
		if n == 1 {
			return ContextPayee
		}
		if n == 3 {
			return ContextNarration
		}
	}

	// Metadata value: indented key: value-prefix — must precede the odd-quote
	// InString check so that `  key: "partial` is MetaValue, not InString.
	if reMetaValue.MatchString(linePrefix) {
		return ContextMetaValue
	}

	// Count quotes: odd number means we are inside a string literal.
	if strings.Count(linePrefix, `"`)%2 == 1 {
		return ContextInString
	}

	// Tag: #token preceded by space or BOL, anywhere on the line.
	if reTagToken.MatchString(linePrefix) {
		return ContextTag
	}

	// Link: ^token preceded by space or BOL, anywhere on the line.
	if reLinkToken.MatchString(linePrefix) {
		return ContextLink
	}

	trimmed := strings.TrimRight(linePrefix, " \t")

	// Check for date prefix (YYYY-MM-DD followed by whitespace).
	if reDatePrefix.MatchString(linePrefix) {
		afterDate := reDatePrefix.ReplaceAllString(linePrefix, "")
		afterDate = strings.TrimLeft(afterDate, " \t")

		if afterDate == "" {
			return ContextKeyword
		}

		fields := strings.Fields(afterDate)
		isOpenOrClose := len(fields) >= 2 && (fields[0] == "open" || fields[0] == "close")
		switch {
		case len(fields) == 1 && isFlag(fields[0]):
			return ContextFlag
		case len(fields) == 1 && isKeyword(fields[0]):
			return ContextKeyword
		case len(fields) == 1 && rePartialKeyword.MatchString(fields[0]):
			// Partial directive keyword in progress (e.g. "o", "op"); the
			// editor's word-boundary auto-trigger calls completion here, and
			// returning ContextKeyword lets the client prefix-filter against
			// the full directive list.
			return ContextKeyword
		case isOpenOrClose && reCurrencyToken.MatchString(trimmed):
			// narrowest first: pure uppercase token is a currency identifier
			return ContextCurrency
		case isOpenOrClose && accountTokenWithColon(trimmed):
			return ContextAccount
		case isOpenOrClose && trailingAccountToken(trimmed) != "":
			// mixed-case token, no colon yet — ambiguous; currency is safer than account
			return ContextCurrency
		case (len(fields) == 1 || !isFlag(fields[0])) && accountTokenWithColon(trimmed):
			// date + non-flag token, cursor on account-like token
			return ContextAccount
		case (len(fields) == 1 || !isFlag(fields[0])) && reCurrencyToken.MatchString(trimmed):
			return ContextCurrency
		}

		// Cursor is somewhere after date; fall through to general token checks.
	}

	// Empty or whitespace-only line: top-level keyword position.
	if strings.TrimSpace(linePrefix) == "" {
		return ContextKeyword
	}

	// Indented line: could be a posting or a metadata line.
	if strings.HasPrefix(linePrefix, " ") || strings.HasPrefix(linePrefix, "\t") {
		// Metadata key: indented lowercase key chars, no colon yet
		if reMetaKey.MatchString(linePrefix) {
			return ContextMetaKey
		}
		if tok := trailingAccountToken(trimmed); tok != "" {
			if strings.Contains(tok, ":") {
				return ContextAccount
			}
			// Uppercase but no colon yet: could be account start or currency.
			// Only treat as account start when it is the sole token on the posting line.
			rest := strings.TrimLeft(linePrefix, " \t")
			if rest == tok {
				return ContextAccount
			}
			// After some content, uppercase token without colon is likely currency.
			if reCurrencyToken.MatchString(trimmed) {
				return ContextCurrency
			}
		}
		return ContextUnknown
	}

	return ContextUnknown
}

func isFlag(s string) bool {
	return s == "*" || s == "!"
}

// trailingAccountToken returns the longest suffix of s that reads as a
// (possibly partial) beancount account name under the grammar ast.Account
// implements: an ASCII uppercase root letter, then runes accepted by
// syntax.IsAccountComponentStart / IsAccountComponentCont and ':' separators.
// It returns "" when s does not end in such a token.
func trailingAccountToken(s string) string {
	rs := []rune(s)
	i := len(rs)
	for i > 0 {
		r := rs[i-1]
		if r == ':' || syntax.IsAccountComponentStart(r) || syntax.IsAccountComponentCont(r) {
			i--
			continue
		}
		break
	}
	for i < len(rs) && (rs[i] < 'A' || rs[i] > 'Z') {
		i++
	}
	if i == len(rs) {
		return ""
	}
	return string(rs[i:])
}

// accountTokenWithColon reports whether the trailing account token of s spans
// at least two components (contains a ':'), each obeying the ast.Account
// component grammar.
func accountTokenWithColon(s string) bool {
	tok := trailingAccountToken(s)
	if !strings.Contains(tok, ":") {
		return false
	}
	for _, c := range strings.Split(tok, ":") {
		if !accountComponentLike(c) {
			return false
		}
	}
	return true
}

// accountComponentLike reports whether c is a non-empty account component: a
// component-start rune followed by continuation runes.
func accountComponentLike(c string) bool {
	if c == "" {
		return false
	}
	for i, r := range c {
		if i == 0 {
			if !syntax.IsAccountComponentStart(r) {
				return false
			}
			continue
		}
		if !syntax.IsAccountComponentCont(r) {
			return false
		}
	}
	return true
}

var dateDirectiveKeywords = map[string]bool{
	"open": true, "close": true, "commodity": true, "balance": true,
	"pad": true, "note": true, "document": true, "event": true,
	"query": true, "price": true, "txn": true,
}

var headerKeywords = map[string]bool{
	"option": true, "plugin": true, "include": true, "pushtag": true,
	"poptag": true, "pushmeta": true, "popmeta": true, "custom": true,
}

func isKeyword(s string) bool {
	return dateDirectiveKeywords[s] || headerKeywords[s]
}

// disambiguateFirstString refines a ContextPayee classification using the line
// suffix (cursor to end of line). When a second quoted string already follows
// the cursor's string, the first string is unambiguously the payee, so
// ContextPayee is returned. Otherwise the lone string may become either payee
// or narration, so ContextPayeeOrNarration is returned.
//
// The suffix's first quote closes the current string; a second quote opens a
// following string, so two or more quotes in the suffix mean a second string
// exists.
func disambiguateFirstString(suffix string) ContextKind {
	if strings.Count(suffix, `"`) >= 2 {
		return ContextPayee
	}
	return ContextPayeeOrNarration
}

// metaKeyFromLine returns the metadata key name from a ContextMetaValue line
// prefix (matches reMetaValue), or "" if the line is not in value-context.
func metaKeyFromLine(linePrefix string) string {
	m := reMetaValue.FindStringSubmatch(linePrefix)
	if m == nil {
		return ""
	}
	return m[1]
}
