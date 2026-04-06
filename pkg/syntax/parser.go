package syntax

import "fmt"

// Parse parses a beancount source string into a concrete syntax tree.
func Parse(src string) *File {
	p := &parser{
		scanner: newScanner(src),
		src:     src,
	}
	p.advance() // read first token into p.tok
	return p.parseFile()
}

type parser struct {
	scanner *scanner
	src     string
	tok     Token // current token (lookahead)
	errors  []Error
}

// advance consumes the current token and reads the next one.
func (p *parser) advance() Token {
	prev := p.tok
	p.tok = p.scanner.scan()
	return prev
}

// peek returns the current token kind without consuming it.
func (p *parser) peek() TokenKind { return p.tok.Kind }

// expect consumes the current token if it matches kind, otherwise records an error.
// Returns the consumed token (or a synthetic token on mismatch).
func (p *parser) expect(kind TokenKind) Token {
	if p.tok.Kind == kind {
		return p.advance()
	}
	p.errorf("expected %s, got %s", kind, p.tok.Kind)
	return Token{Kind: ILLEGAL, Pos: p.tok.Pos}
}

// at reports whether the current token is one of the given kinds.
func (p *parser) at(kinds ...TokenKind) bool {
	for _, k := range kinds {
		if p.tok.Kind == k {
			return true
		}
	}
	return false
}

// consumeIf consumes and returns the token if it matches, otherwise returns nil.
func (p *parser) consumeIf(kind TokenKind) *Token {
	if p.tok.Kind == kind {
		tok := p.advance()
		return &tok
	}
	return nil
}

// errorf records a parse error at the current token position.
func (p *parser) errorf(format string, args ...any) {
	p.errors = append(p.errors, Error{
		Pos: p.tok.Pos,
		Msg: fmt.Sprintf(format, args...),
	})
}

// isAtLineStart reports whether the current token is at the beginning of a line.
func (p *parser) isAtLineStart() bool {
	if p.tok.Pos == 0 && len(p.tok.LeadingTrivia) == 0 {
		return true // first token in file
	}
	for _, t := range p.tok.LeadingTrivia {
		if t.Kind == NewlineTrivia {
			return true
		}
	}
	return false
}

// isAtNextLine reports whether the current token is on a different line
// (has a newline in its leading trivia).
func (p *parser) isAtNextLine() bool {
	for _, t := range p.tok.LeadingTrivia {
		if t.Kind == NewlineTrivia {
			return true
		}
	}
	return false
}

func (p *parser) parseFile() *File {
	root := &Node{Kind: FileNode}

	for p.peek() != EOF {
		node := p.parseTopLevel()
		if node != nil {
			root.AddNode(node)
		}
	}

	// Add EOF token to root so its trivia is preserved.
	eof := p.advance()
	root.AddToken(&eof)

	return &File{Root: root, Errors: p.errors}
}

func (p *parser) parseTopLevel() *Node {
	switch {
	case p.peek() == DATE:
		// Dated directive -- will be handled in later steps.
		return p.parseUnrecognizedLine()
	case p.peek() == IDENT:
		switch p.tok.Raw {
		case "option":
			return p.parseOption()
		case "plugin":
			return p.parsePlugin()
		case "include":
			return p.parseInclude()
		case "pushtag":
			return p.parsePushtag()
		case "poptag":
			return p.parsePoptag()
		default:
			return p.parseUnrecognizedLine()
		}
	default:
		return p.parseUnrecognizedLine()
	}
}

func (p *parser) parseOption() *Node {
	// option "key" "value"
	node := &Node{Kind: OptionDirective}
	kw := p.advance()
	node.AddToken(&kw)
	key := p.expect(STRING)
	node.AddToken(&key)
	val := p.expect(STRING)
	node.AddToken(&val)
	return node
}

func (p *parser) parsePlugin() *Node {
	// plugin "name" ["config"]
	node := &Node{Kind: PluginDirective}
	kw := p.advance()
	node.AddToken(&kw)
	name := p.expect(STRING)
	node.AddToken(&name)
	if p.peek() == STRING && !p.isAtNextLine() {
		config := p.advance()
		node.AddToken(&config)
	}
	return node
}

func (p *parser) parseInclude() *Node {
	// include "path"
	node := &Node{Kind: IncludeDirective}
	kw := p.advance()
	node.AddToken(&kw)
	path := p.expect(STRING)
	node.AddToken(&path)
	return node
}

func (p *parser) parsePushtag() *Node {
	// pushtag #tag
	node := &Node{Kind: PushtagDirective}
	kw := p.advance()
	node.AddToken(&kw)
	tag := p.expect(TAG)
	node.AddToken(&tag)
	return node
}

func (p *parser) parsePoptag() *Node {
	// poptag #tag
	node := &Node{Kind: PoptagDirective}
	kw := p.advance()
	node.AddToken(&kw)
	tag := p.expect(TAG)
	node.AddToken(&tag)
	return node
}

func (p *parser) parseUnrecognizedLine() *Node {
	node := &Node{Kind: UnrecognizedLineNode}
	for p.peek() != EOF {
		if p.isAtLineStart() && len(node.Children) > 0 {
			break
		}
		tok := p.advance()
		node.AddToken(&tok)
	}
	return node
}
