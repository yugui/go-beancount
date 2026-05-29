package parser

import (
	"fmt"
	"strings"
)

// scanner turns a BQL query string into a stream of tokens. It is robust
// against malformed input: every error is reported via a positioned Error and
// the scanner never panics. Whitespace is insignificant; there are no comments.
type scanner struct {
	src    string
	offset int
	line   int
	col    int
}

func newScanner(src string) *scanner {
	return &scanner{src: src, line: 1, col: 1}
}

func (s *scanner) pos() Position {
	return Position{Offset: s.offset, Line: s.line, Column: s.col}
}

// advance consumes n bytes, none of which may be a newline.
func (s *scanner) advance(n int) {
	s.offset += n
	s.col += n
}

func (s *scanner) skipSpace() {
	for s.offset < len(s.src) {
		switch s.src[s.offset] {
		case ' ', '\t', '\r':
			s.advance(1)
		case '\n':
			s.offset++
			s.line++
			s.col = 1
		default:
			return
		}
	}
}

func (s *scanner) peekByte(ahead int) byte {
	i := s.offset + ahead
	if i < len(s.src) {
		return s.src[i]
	}
	return 0
}

// scan returns the next token. On a lexical error it returns a tokIllegal
// token and a positioned Error; callers stop on the first such error.
func (s *scanner) scan() (token, error) {
	s.skipSpace()
	if s.offset >= len(s.src) {
		return token{kind: tokEOF, pos: s.pos()}, nil
	}

	start := s.pos()
	ch := s.src[s.offset]

	switch ch {
	case '(':
		s.advance(1)
		return token{kind: tokLParen, text: "(", pos: start}, nil
	case ')':
		s.advance(1)
		return token{kind: tokRParen, text: ")", pos: start}, nil
	case ',':
		s.advance(1)
		return token{kind: tokComma, text: ",", pos: start}, nil
	case '*':
		s.advance(1)
		return token{kind: tokStar, text: "*", pos: start}, nil
	case '+':
		s.advance(1)
		return token{kind: tokPlus, text: "+", pos: start}, nil
	case '-':
		s.advance(1)
		return token{kind: tokMinus, text: "-", pos: start}, nil
	case '/':
		s.advance(1)
		return token{kind: tokSlash, text: "/", pos: start}, nil
	case '%':
		s.advance(1)
		return token{kind: tokPercent, text: "%", pos: start}, nil
	case '~':
		s.advance(1)
		return token{kind: tokTilde, text: "~", pos: start}, nil
	case ';':
		s.advance(1)
		return token{kind: tokSemi, text: ";", pos: start}, nil
	case '=':
		s.advance(1)
		return token{kind: tokEq, text: "=", pos: start}, nil
	case '!':
		if s.peekByte(1) == '=' {
			s.advance(2)
			return token{kind: tokNe, text: "!=", pos: start}, nil
		}
		s.advance(1)
		return token{kind: tokIllegal, text: "!", pos: start},
			&Error{Pos: start, Msg: "unexpected character '!' (did you mean '!='?)"}
	case '<':
		if s.peekByte(1) == '=' {
			s.advance(2)
			return token{kind: tokLe, text: "<=", pos: start}, nil
		}
		s.advance(1)
		return token{kind: tokLt, text: "<", pos: start}, nil
	case '>':
		if s.peekByte(1) == '=' {
			s.advance(2)
			return token{kind: tokGe, text: ">=", pos: start}, nil
		}
		s.advance(1)
		return token{kind: tokGt, text: ">", pos: start}, nil
	case '\'', '"':
		return s.scanString(ch, start)
	}

	switch {
	case isDigit(ch):
		return s.scanDateOrNumber(start)
	case ch == '.' && isDigit(s.peekByte(1)):
		return s.scanNumber(start)
	case isIdentStart(ch):
		return s.scanIdent(start)
	default:
		s.advance(1)
		return token{kind: tokIllegal, text: string(ch), pos: start},
			&Error{Pos: start, Msg: fmt.Sprintf("unexpected character %q", string(ch))}
	}
}

// scanString consumes a quoted string. Both single- and double-quoted forms
// are accepted; the quote character is escaped by doubling it (” or "") or
// with a backslash. The returned text excludes the quotes and has escapes
// resolved.
func (s *scanner) scanString(quote byte, start Position) (token, error) {
	s.advance(1) // opening quote
	var b strings.Builder
	for s.offset < len(s.src) {
		ch := s.src[s.offset]
		switch ch {
		case '\\':
			next := s.peekByte(1)
			if next == 0 {
				s.advance(1)
				return token{kind: tokIllegal, pos: start},
					&Error{Pos: start, Msg: "unterminated string literal"}
			}
			b.WriteByte(next)
			s.advance(2)
		case quote:
			if s.peekByte(1) == quote { // doubled quote escape
				b.WriteByte(quote)
				s.advance(2)
				continue
			}
			s.advance(1) // closing quote
			return token{kind: tokString, text: b.String(), pos: start}, nil
		case '\n':
			b.WriteByte(ch)
			s.offset++
			s.line++
			s.col = 1
		default:
			b.WriteByte(ch)
			s.advance(1)
		}
	}
	return token{kind: tokIllegal, pos: start},
		&Error{Pos: start, Msg: "unterminated string literal"}
}

// scanDateOrNumber resolves the date-vs-arithmetic ambiguity: a run matching
// exactly \d{4}-\d{2}-\d{2} is a single DATE token, otherwise the run is a
// number (and a following '-' is a separate subtraction operator).
func (s *scanner) scanDateOrNumber(start Position) (token, error) {
	if s.matchesDate() {
		text := s.src[s.offset : s.offset+10]
		s.advance(10)
		return token{kind: tokDate, text: text, pos: start}, nil
	}
	return s.scanNumber(start)
}

func (s *scanner) matchesDate() bool {
	if s.offset+10 > len(s.src) {
		return false
	}
	c := s.src[s.offset : s.offset+10]
	if !(isDigit(c[0]) && isDigit(c[1]) && isDigit(c[2]) && isDigit(c[3]) &&
		c[4] == '-' && isDigit(c[5]) && isDigit(c[6]) &&
		c[7] == '-' && isDigit(c[8]) && isDigit(c[9])) {
		return false
	}
	// A trailing digit would make it a longer run, not a date.
	return !isDigit(s.peekByte(10))
}

// scanNumber consumes an integer or decimal. The presence of a '.' makes it a
// decimal; otherwise it is an integer.
func (s *scanner) scanNumber(start Position) (token, error) {
	begin := s.offset
	hasDot := false
	if s.src[s.offset] == '.' {
		hasDot = true
		s.advance(1)
		for s.offset < len(s.src) && isDigit(s.src[s.offset]) {
			s.advance(1)
		}
	} else {
		for s.offset < len(s.src) && isDigit(s.src[s.offset]) {
			s.advance(1)
		}
		if s.offset < len(s.src) && s.src[s.offset] == '.' {
			hasDot = true
			s.advance(1)
			for s.offset < len(s.src) && isDigit(s.src[s.offset]) {
				s.advance(1)
			}
		}
	}
	text := s.src[begin:s.offset]
	kind := tokInt
	if hasDot {
		kind = tokDecimal
	}
	return token{kind: kind, text: text, pos: start}, nil
}

func (s *scanner) scanIdent(start Position) (token, error) {
	begin := s.offset
	s.advance(1)
	for s.offset < len(s.src) && isIdentCont(s.src[s.offset]) {
		s.advance(1)
	}
	text := s.src[begin:s.offset]
	if kw, ok := keywords[strings.ToLower(text)]; ok {
		return token{kind: kw, text: text, pos: start}, nil
	}
	return token{kind: tokIdent, text: text, pos: start}, nil
}

func isDigit(ch byte) bool { return ch >= '0' && ch <= '9' }

func isIdentStart(ch byte) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isIdentCont(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch)
}
