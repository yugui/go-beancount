package syntax

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

// scanLeadingTrivia collects whitespace, comments, and newlines before a token.
func (s *scanner) scanLeadingTrivia() []Trivia {
	var trivia []Trivia
	for s.offset < len(s.src) {
		ch := s.src[s.offset]
		switch {
		case ch == ' ' || ch == '\t':
			trivia = append(trivia, s.scanWhitespace())
		case ch == '\n':
			trivia = append(trivia, Trivia{Kind: NewlineTrivia, Raw: s.src[s.offset : s.offset+1]})
			s.offset++
		case ch == '\r' && s.offset+1 < len(s.src) && s.src[s.offset+1] == '\n':
			trivia = append(trivia, Trivia{Kind: NewlineTrivia, Raw: s.src[s.offset : s.offset+2]})
			s.offset += 2
		case ch == '\r':
			// bare \r treated as newline
			trivia = append(trivia, Trivia{Kind: NewlineTrivia, Raw: s.src[s.offset : s.offset+1]})
			s.offset++
		case ch == ';':
			trivia = append(trivia, s.scanComment())
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
		s.offset++
		return Token{Kind: HASH, Pos: pos, Raw: s.src[pos:s.offset]}
	case '^':
		s.offset++
		return Token{Kind: CARET, Pos: pos, Raw: s.src[pos:s.offset]}
	default:
		// Unrecognized character — emit ILLEGAL for a single byte
		s.offset++
		return Token{Kind: ILLEGAL, Pos: pos, Raw: s.src[pos:s.offset]}
	}
}
