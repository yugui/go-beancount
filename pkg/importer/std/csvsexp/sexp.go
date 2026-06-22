package csvsexp

import (
	"fmt"
	"strconv"
	"strings"
)

// nodeKind classifies a parsed S-expression datum.
type nodeKind int

const (
	nodeList nodeKind = iota
	nodeSymbol
	nodeKeyword
	nodeString
	nodeInt
	nodeBool
)

// node is one parsed S-expression datum. Only the fields relevant to kind are
// populated: items for nodeList; text for nodeSymbol/nodeKeyword/nodeString
// (keyword text omits the leading colon); num for nodeInt; b for nodeBool. line
// is the 1-based source line where the datum began.
type node struct {
	kind  nodeKind
	items []node
	text  string
	num   int64
	b     bool
	line  int
}

// parseProgram parses src and returns the single top-level datum. It errors
// when src holds no datum or more than one. Errors are prefixed
// "csvsexp: parse: ".
func parseProgram(src string) (node, error) {
	r := &sexpReader{src: []rune(src), line: 1}
	first, ok, err := r.read()
	if err != nil {
		return node{}, err
	}
	if !ok {
		return node{}, fmt.Errorf("csvsexp: parse: empty program")
	}
	_, more, err := r.read()
	if err != nil {
		return node{}, err
	}
	if more {
		return node{}, fmt.Errorf("csvsexp: parse: more than one top-level form")
	}
	return first, nil
}

// sexpReader is a rune cursor over one program source, tracking the current
// 1-based line for diagnostics.
type sexpReader struct {
	src  []rune
	pos  int
	line int
}

func (r *sexpReader) peek() (rune, bool) {
	if r.pos >= len(r.src) {
		return 0, false
	}
	return r.src[r.pos], true
}

func (r *sexpReader) advance() rune {
	c := r.src[r.pos]
	r.pos++
	if c == '\n' {
		r.line++
	}
	return c
}

// skipTrivia consumes whitespace and ';' line comments.
func (r *sexpReader) skipTrivia() {
	for {
		c, ok := r.peek()
		if !ok {
			return
		}
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			r.advance()
		case c == ';':
			for {
				c, ok := r.peek()
				if !ok || c == '\n' {
					break
				}
				r.advance()
			}
		default:
			return
		}
	}
}

// read returns the next datum. The bool is false (with nil error) at end of
// input.
func (r *sexpReader) read() (node, bool, error) {
	r.skipTrivia()
	c, ok := r.peek()
	if !ok {
		return node{}, false, nil
	}
	line := r.line
	switch {
	case c == '(':
		n, err := r.readList()
		return n, err == nil, err
	case c == ')':
		return node{}, false, fmt.Errorf("csvsexp: parse: line %d: unexpected ')'", line)
	case c == '"':
		n, err := r.readString()
		return n, err == nil, err
	default:
		n, err := r.readAtom()
		return n, err == nil, err
	}
}

// readList parses a "(...)" form. The opening '(' has not yet been consumed.
func (r *sexpReader) readList() (node, error) {
	line := r.line
	r.advance() // '('
	var items []node
	for {
		r.skipTrivia()
		c, ok := r.peek()
		if !ok {
			return node{}, fmt.Errorf("csvsexp: parse: line %d: unterminated list", line)
		}
		if c == ')' {
			r.advance()
			return node{kind: nodeList, items: items, line: line}, nil
		}
		item, present, err := r.read()
		if err != nil {
			return node{}, err
		}
		if !present {
			return node{}, fmt.Errorf("csvsexp: parse: line %d: unterminated list", line)
		}
		items = append(items, item)
	}
}

// readString parses a "\"...\"" literal. The opening quote has not yet been
// consumed.
func (r *sexpReader) readString() (node, error) {
	line := r.line
	r.advance() // opening quote
	var b strings.Builder
	for {
		c, ok := r.peek()
		if !ok {
			return node{}, fmt.Errorf("csvsexp: parse: line %d: unterminated string", line)
		}
		r.advance()
		if c == '"' {
			return node{kind: nodeString, text: b.String(), line: line}, nil
		}
		if c == '\\' {
			esc, ok := r.peek()
			if !ok {
				return node{}, fmt.Errorf("csvsexp: parse: line %d: unterminated string escape", line)
			}
			r.advance()
			switch esc {
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			case 'r':
				b.WriteRune('\r')
			case '"', '\\':
				b.WriteRune(esc)
			default:
				return node{}, fmt.Errorf("csvsexp: parse: line %d: invalid string escape %q", line, string(esc))
			}
			continue
		}
		b.WriteRune(c)
	}
}

func isDelimiter(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '(', ')', ';', '"':
		return true
	}
	return false
}

// readAtom parses a bool, keyword, integer, or symbol token up to the next
// delimiter. No leading character has been consumed.
func (r *sexpReader) readAtom() (node, error) {
	line := r.line
	var b strings.Builder
	for {
		c, ok := r.peek()
		if !ok || isDelimiter(c) {
			break
		}
		b.WriteRune(r.advance())
	}
	tok := b.String()
	switch tok {
	case "#t", "#true":
		return node{kind: nodeBool, b: true, line: line}, nil
	case "#f", "#false":
		return node{kind: nodeBool, b: false, line: line}, nil
	}
	if strings.HasPrefix(tok, ":") {
		if len(tok) == 1 {
			return node{}, fmt.Errorf("csvsexp: parse: line %d: empty keyword", line)
		}
		return node{kind: nodeKeyword, text: tok[1:], line: line}, nil
	}
	if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
		return node{kind: nodeInt, num: n, line: line}, nil
	}
	return node{kind: nodeSymbol, text: tok, line: line}, nil
}
