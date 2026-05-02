package syntax

import (
	"unicode"
	"unicode/utf8"
)

// scanner tokenizes a beancount source string into a sequence of Tokens.
// It is zero-copy: all Raw strings are substrings of the original input.
type scanner struct {
	src    string // full source input
	offset int    // current byte offset
}

// newScanner creates a new scanner for the given source string.
func newScanner(src string) *scanner {
	return &scanner{src: src}
}

// scan returns the next token from the input, with trivia attached.
func (s *scanner) scan() Token {
	leading := s.scanLeadingTrivia()

	// Check for EOF
	if s.offset >= len(s.src) {
		return Token{
			Kind:          EOF,
			Pos:           s.offset,
			Raw:           "",
			LeadingTrivia: leading,
		}
	}

	tok := s.scanToken()
	tok.LeadingTrivia = leading
	tok.TrailingTrivia = s.scanTrailingTrivia()
	return tok
}

// scanLeadingTrivia collects whitespace, comments, newlines, and headings before a token.
func (s *scanner) scanLeadingTrivia() []Trivia {
	var trivia []Trivia
	atLineStart := s.offset == 0
	for s.offset < len(s.src) {
		ch := s.src[s.offset]
		switch {
		case ch == ' ' || ch == '\t':
			trivia = append(trivia, s.scanWhitespace())
			atLineStart = false
		case ch == '\n':
			trivia = append(trivia, Trivia{Kind: NewlineTrivia, Raw: s.src[s.offset : s.offset+1]})
			s.offset++
			atLineStart = true
		case ch == '\r' && s.offset+1 < len(s.src) && s.src[s.offset+1] == '\n':
			trivia = append(trivia, Trivia{Kind: NewlineTrivia, Raw: s.src[s.offset : s.offset+2]})
			s.offset += 2
			atLineStart = true
		case ch == '\r':
			// bare \r treated as newline
			trivia = append(trivia, Trivia{Kind: NewlineTrivia, Raw: s.src[s.offset : s.offset+1]})
			s.offset++
			atLineStart = true
		case ch == ';':
			trivia = append(trivia, s.scanComment())
			atLineStart = false
		case ch == '*' && atLineStart && s.isHeadingStart():
			trivia = append(trivia, s.scanHeading())
			atLineStart = false
		default:
			return trivia
		}
	}
	return trivia
}

// scanTrailingTrivia collects same-line whitespace and an optional comment after a token.
// Newlines are NOT included — they become leading trivia of the next token.
func (s *scanner) scanTrailingTrivia() []Trivia {
	var trivia []Trivia
	for s.offset < len(s.src) {
		ch := s.src[s.offset]
		switch {
		case ch == ' ' || ch == '\t':
			trivia = append(trivia, s.scanWhitespace())
		case ch == ';':
			trivia = append(trivia, s.scanComment())
			// After a comment, stop collecting trailing trivia
			return trivia
		default:
			// Hit a newline or non-trivia character; stop
			return trivia
		}
	}
	return trivia
}

// scanWhitespace consumes a run of spaces and tabs.
func (s *scanner) scanWhitespace() Trivia {
	start := s.offset
	for s.offset < len(s.src) {
		ch := s.src[s.offset]
		if ch != ' ' && ch != '\t' {
			break
		}
		s.offset++
	}
	return Trivia{Kind: WhitespaceTrivia, Raw: s.src[start:s.offset]}
}

// scanComment consumes from ';' to end of line (not including the newline).
func (s *scanner) scanComment() Trivia {
	start := s.offset
	for s.offset < len(s.src) && s.src[s.offset] != '\n' && s.src[s.offset] != '\r' {
		s.offset++
	}
	return Trivia{Kind: CommentTrivia, Raw: s.src[start:s.offset]}
}

// isHeadingStart reports whether the current position looks like the start of
// an org-mode heading. The caller must have already verified that s.src[s.offset] == '*'
// and that we are at column 0. A heading is '*' followed by another '*', a space,
// a tab, or a newline (\n or \r).
func (s *scanner) isHeadingStart() bool {
	next := s.offset + 1
	if next >= len(s.src) {
		return false // bare '*' at EOF is not a heading
	}
	ch := s.src[next]
	return ch == '*' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

// scanHeading consumes an org-mode heading from '*' to end of line (not including the newline).
func (s *scanner) scanHeading() Trivia {
	start := s.offset
	for s.offset < len(s.src) && s.src[s.offset] != '\n' && s.src[s.offset] != '\r' {
		s.offset++
	}
	return Trivia{Kind: HeadingTrivia, Raw: s.src[start:s.offset]}
}

// scanToken scans and returns the next real token (not trivia).
// Caller must ensure s.offset < len(s.src).
func (s *scanner) scanToken() Token {
	pos := s.offset
	ch := s.src[s.offset]

	switch ch {
	case '+':
		s.offset++
		return Token{Kind: PLUS, Pos: pos, Raw: s.src[pos:s.offset]}
	case '-':
		s.offset++
		return Token{Kind: MINUS, Pos: pos, Raw: s.src[pos:s.offset]}
	case '*':
		s.offset++
		return Token{Kind: STAR, Pos: pos, Raw: s.src[pos:s.offset]}
	case '/':
		s.offset++
		return Token{Kind: SLASH, Pos: pos, Raw: s.src[pos:s.offset]}
	case '(':
		s.offset++
		return Token{Kind: LPAREN, Pos: pos, Raw: s.src[pos:s.offset]}
	case ')':
		s.offset++
		return Token{Kind: RPAREN, Pos: pos, Raw: s.src[pos:s.offset]}
	case '{':
		if s.offset+1 < len(s.src) && s.src[s.offset+1] == '{' {
			s.offset += 2
			return Token{Kind: LBRACE2, Pos: pos, Raw: s.src[pos:s.offset]}
		}
		s.offset++
		return Token{Kind: LBRACE, Pos: pos, Raw: s.src[pos:s.offset]}
	case '}':
		if s.offset+1 < len(s.src) && s.src[s.offset+1] == '}' {
			s.offset += 2
			return Token{Kind: RBRACE2, Pos: pos, Raw: s.src[pos:s.offset]}
		}
		s.offset++
		return Token{Kind: RBRACE, Pos: pos, Raw: s.src[pos:s.offset]}
	case '@':
		if s.offset+1 < len(s.src) && s.src[s.offset+1] == '@' {
			s.offset += 2
			return Token{Kind: ATAT, Pos: pos, Raw: s.src[pos:s.offset]}
		}
		s.offset++
		return Token{Kind: AT, Pos: pos, Raw: s.src[pos:s.offset]}
	case '~':
		s.offset++
		return Token{Kind: TILDE, Pos: pos, Raw: s.src[pos:s.offset]}
	case ',':
		s.offset++
		return Token{Kind: COMMA, Pos: pos, Raw: s.src[pos:s.offset]}
	case ':':
		s.offset++
		return Token{Kind: COLON, Pos: pos, Raw: s.src[pos:s.offset]}
	case '!':
		s.offset++
		return Token{Kind: BANG, Pos: pos, Raw: s.src[pos:s.offset]}
	case '#':
		if s.offset+1 < len(s.src) && isTagLinkChar(s.src[s.offset+1]) {
			return s.scanTag()
		}
		s.offset++
		return Token{Kind: HASH, Pos: pos, Raw: s.src[pos:s.offset]}
	case '^':
		if s.offset+1 < len(s.src) && isTagLinkChar(s.src[s.offset+1]) {
			return s.scanLink()
		}
		s.offset++
		return Token{Kind: CARET, Pos: pos, Raw: s.src[pos:s.offset]}
	default:
		switch {
		case ch == '"':
			return s.scanString()
		case ch >= '0' && ch <= '9':
			return s.scanDateOrNumber()
		case ch == '.' && s.offset+1 < len(s.src) && s.src[s.offset+1] >= '0' && s.src[s.offset+1] <= '9':
			return s.scanNumber()
		case ch >= 'A' && ch <= 'Z':
			return s.scanUpperWord()
		case ch >= 'a' && ch <= 'z':
			return s.scanIdent()
		default:
			// Unrecognized character — emit ILLEGAL for a full UTF-8 rune
			// so a single multi-byte character does not get sliced into
			// per-byte ILLEGAL tokens.
			_, size := utf8.DecodeRuneInString(s.src[s.offset:])
			if size == 0 {
				size = 1
			}
			s.offset += size
			return Token{Kind: ILLEGAL, Pos: pos, Raw: s.src[pos:s.offset]}
		}
	}
}

// isDigit reports whether ch is an ASCII digit.
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// scanDateOrNumber tries to scan a DATE token; if the pattern doesn't match,
// it falls back to scanning a NUMBER. Called when current byte is a digit.
func (s *scanner) scanDateOrNumber() Token {
	pos := s.offset

	// Try to match date pattern: \d{4}[-/]\d{2}[-/]\d{2}
	// We need at least 10 characters: YYYY-MM-DD
	if s.offset+10 <= len(s.src) {
		candidate := s.src[s.offset : s.offset+10]
		if isDigit(candidate[0]) && isDigit(candidate[1]) && isDigit(candidate[2]) && isDigit(candidate[3]) &&
			(candidate[4] == '-' || candidate[4] == '/') &&
			isDigit(candidate[5]) && isDigit(candidate[6]) &&
			(candidate[7] == '-' || candidate[7] == '/') &&
			isDigit(candidate[8]) && isDigit(candidate[9]) {
			s.offset += 10
			return Token{Kind: DATE, Pos: pos, Raw: s.src[pos:s.offset]}
		}
	}

	return s.scanNumber()
}

// scanNumber consumes a number token. Called when current byte is a digit or '.'.
// Pattern: [0-9][0-9,]*(\.[0-9]*)? or \.[0-9]+
func (s *scanner) scanNumber() Token {
	pos := s.offset

	if s.src[s.offset] == '.' {
		// Leading dot: consume '.' then digits
		s.offset++
		for s.offset < len(s.src) && isDigit(s.src[s.offset]) {
			s.offset++
		}
		return Token{Kind: NUMBER, Pos: pos, Raw: s.src[pos:s.offset]}
	}

	// Consume digits and commas
	for s.offset < len(s.src) && (isDigit(s.src[s.offset]) || s.src[s.offset] == ',') {
		s.offset++
	}

	// Optional decimal part
	if s.offset < len(s.src) && s.src[s.offset] == '.' {
		s.offset++ // consume '.'
		for s.offset < len(s.src) && isDigit(s.src[s.offset]) {
			s.offset++
		}
	}

	return Token{Kind: NUMBER, Pos: pos, Raw: s.src[pos:s.offset]}
}

// isTagLinkChar reports whether ch is valid in a tag or link body: [A-Za-z0-9_-].
func isTagLinkChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-'
}

// isUpperWordChar reports whether ch is valid in an uppercase-starting word
// (superset of currency and account component chars): [A-Za-z0-9'._-].
func isUpperWordChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '\'' || ch == '.' || ch == '_' || ch == '-'
}

// isIdentChar reports whether ch is valid in an identifier: [A-Za-z0-9_-].
func isIdentChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-'
}

// IsAccountRoot reports whether word is a beancount account root type:
// one of "Assets", "Liabilities", "Equity", "Income", "Expenses".
func IsAccountRoot(word string) bool {
	switch word {
	case "Assets", "Liabilities", "Equity", "Income", "Expenses":
		return true
	}
	return false
}

// IsAccountComponentStart reports whether r is valid as the first character
// of an account component. For ASCII runes, only uppercase letters [A-Z]
// and digits [0-9] are permitted. Beyond ASCII, r must belong to one of
// the Unicode categories Lu, Lt, Lo, Lm, Nd, Nl, No.
func IsAccountComponentStart(r rune) bool {
	if r < 0x80 {
		return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	return unicode.In(r, unicode.Lu, unicode.Lt, unicode.Lo, unicode.Lm,
		unicode.Nd, unicode.Nl, unicode.No)
}

// IsAccountComponentCont reports whether r is valid as a continuation
// character in an account component. It extends the IsAccountComponentStart
// alphabet with ASCII lowercase letters [a-z], Unicode categories Ll, Mn,
// Mc, and the ASCII hyphen '-'. The Unicode "Other_ID_Continue" code points
// are also accepted so that separators conventionally embedded in
// identifiers — most notably U+30FB KATAKANA MIDDLE DOT — work in CJK
// account components.
func IsAccountComponentCont(r rune) bool {
	if r == '-' {
		return true
	}
	if r < 0x80 {
		return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
	}
	if isOtherIDContinue(r) {
		return true
	}
	return unicode.In(r, unicode.Lu, unicode.Lt, unicode.Lo, unicode.Lm,
		unicode.Nd, unicode.Nl, unicode.No,
		unicode.Ll, unicode.Mn, unicode.Mc)
}

// isOtherIDContinue reports whether r is one of the small fixed set of code
// points Unicode marks with the property Other_ID_Continue. They are
// normally outside the letter/mark/number categories but are promoted into
// XID_Continue so identifier syntax can keep working across scripts that
// embed separators (e.g. Japanese ・, Greek ano teleia, Ethiopic digits).
func isOtherIDContinue(r rune) bool {
	switch r {
	case 0x00B7, // MIDDLE DOT
		0x0387, // GREEK ANO TELEIA
		0x19DA, // NEW TAI LUE THAM DIGIT ONE
		0x30FB, // KATAKANA MIDDLE DOT
		0xFF65: // HALFWIDTH KATAKANA MIDDLE DOT
		return true
	}
	// ETHIOPIC DIGIT ONE..NINE
	if r >= 0x1369 && r <= 0x1371 {
		return true
	}
	return false
}

// isCurrency reports whether word matches the currency pattern:
// 1+ chars, all [A-Z0-9'._-], starts and ends with [A-Z0-9].
func isCurrency(word string) bool {
	if len(word) == 0 {
		return false
	}
	first := word[0]
	if !((first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	last := word[len(word)-1]
	if !((last >= 'A' && last <= 'Z') || (last >= '0' && last <= '9')) {
		return false
	}
	for i := 1; i < len(word)-1; i++ {
		ch := word[i]
		if !((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '\'' || ch == '.' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

// scanUpperWord scans a token starting with an uppercase letter [A-Z].
// It may produce ACCOUNT, CURRENCY, or IDENT.
func (s *scanner) scanUpperWord() Token {
	pos := s.offset

	// Consume the initial word: [A-Za-z0-9'._-]+
	s.offset++
	for s.offset < len(s.src) && isUpperWordChar(s.src[s.offset]) {
		s.offset++
	}
	word := s.src[pos:s.offset]

	// Check for account: root type followed by ':'
	if IsAccountRoot(word) && s.offset < len(s.src) && s.src[s.offset] == ':' {
		// Try to scan account components: (:Component)+
		// Each component: ':' then a valid start rune then valid continuation runes
		saved := s.offset
		componentCount := 0
		for s.offset < len(s.src) && s.src[s.offset] == ':' {
			next := s.offset + 1
			if next >= len(s.src) {
				break
			}
			r, size := utf8.DecodeRuneInString(s.src[next:])
			if !IsAccountComponentStart(r) {
				break
			}
			// Consume ':' and the first rune of the component
			s.offset = next + size
			for s.offset < len(s.src) {
				r, size = utf8.DecodeRuneInString(s.src[s.offset:])
				if IsAccountComponentCont(r) {
					s.offset += size
				} else {
					break
				}
			}
			componentCount++
		}
		if componentCount > 0 {
			return Token{Kind: ACCOUNT, Pos: pos, Raw: s.src[pos:s.offset]}
		}
		// No valid components found; restore offset
		s.offset = saved
	}

	// Check if it's a currency
	if isCurrency(word) {
		return Token{Kind: CURRENCY, Pos: pos, Raw: s.src[pos:s.offset]}
	}

	// Otherwise it's an identifier (e.g. titlecase words like "Assets" alone)
	return Token{Kind: IDENT, Pos: pos, Raw: s.src[pos:s.offset]}
}

// scanIdent scans a lowercase-starting identifier: [a-z][A-Za-z0-9_-]*
func (s *scanner) scanIdent() Token {
	pos := s.offset
	s.offset++
	for s.offset < len(s.src) && isIdentChar(s.src[s.offset]) {
		s.offset++
	}
	return Token{Kind: IDENT, Pos: pos, Raw: s.src[pos:s.offset]}
}

// scanTag scans a tag token: '#' followed by [A-Za-z0-9_-]+.
// Called when '#' is followed by a valid tag/link char.
func (s *scanner) scanTag() Token {
	pos := s.offset
	s.offset++ // consume '#'
	for s.offset < len(s.src) && isTagLinkChar(s.src[s.offset]) {
		s.offset++
	}
	return Token{Kind: TAG, Pos: pos, Raw: s.src[pos:s.offset]}
}

// scanLink scans a link token: '^' followed by [A-Za-z0-9_-]+.
// Called when '^' is followed by a valid tag/link char.
func (s *scanner) scanLink() Token {
	pos := s.offset
	s.offset++ // consume '^'
	for s.offset < len(s.src) && isTagLinkChar(s.src[s.offset]) {
		s.offset++
	}
	return Token{Kind: LINK, Pos: pos, Raw: s.src[pos:s.offset]}
}

// scanString consumes a double-quoted string token. Called when current byte is '"'.
// The token includes the opening and closing quotes. Handles \" escape.
// If EOF is reached before the closing quote, the token is emitted as-is.
func (s *scanner) scanString() Token {
	pos := s.offset
	s.offset++ // consume opening '"'

	for s.offset < len(s.src) {
		ch := s.src[s.offset]
		if ch == '\\' && s.offset+1 < len(s.src) {
			s.offset += 2 // skip escaped character
			continue
		}
		if ch == '"' {
			s.offset++ // consume closing '"'
			return Token{Kind: STRING, Pos: pos, Raw: s.src[pos:s.offset]}
		}
		s.offset++
	}

	// EOF before closing quote
	return Token{Kind: STRING, Pos: pos, Raw: s.src[pos:s.offset]}
}
