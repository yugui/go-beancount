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
		return p.parseDatedDirective()
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

// isIndented reports whether the current token is indented
// (has whitespace after the last newline in leading trivia).
func (p *parser) isIndented() bool {
	trivia := p.tok.LeadingTrivia
	sawNewline := false
	for _, t := range trivia {
		if t.Kind == NewlineTrivia {
			sawNewline = true
		} else if t.Kind == WhitespaceTrivia && sawNewline {
			return true
		}
	}
	return false
}

// parseMetadata consumes indented metadata lines and adds them as children of the given node.
func (p *parser) parseMetadata(parent *Node) {
	for p.isAtNextLine() && p.isIndented() {
		// Check if this looks like a metadata line: IDENT followed by COLON
		if p.peek() == IDENT {
			meta := p.tryParseMetadataLine()
			if meta != nil {
				parent.AddNode(meta)
				continue
			}
		}
		break
	}
}

// tryParseMetadataLine parses a metadata line: IDENT COLON value.
// On error (e.g. missing colon), it returns a partial node containing
// the tokens consumed so far, so trivia is preserved for round-tripping.
func (p *parser) tryParseMetadataLine() *Node {
	node := &Node{Kind: MetadataLineNode}
	key := p.advance() // consume IDENT (the metadata key)
	node.AddToken(&key)

	if p.peek() != COLON {
		p.errorf("expected ':' after metadata key %q", key.Raw)
		return node
	}

	colon := p.advance() // consume COLON
	node.AddToken(&colon)

	// Parse the value — can be various types, all on the same line
	if !p.isAtNextLine() && p.peek() != EOF {
		val := p.parseMetaValue()
		if val != nil {
			node.AddToken(val)
		}
	}

	return node
}

// parseMetaValue parses a metadata value token.
func (p *parser) parseMetaValue() *Token {
	switch p.peek() {
	case STRING, NUMBER, DATE, ACCOUNT, CURRENCY, TAG, LINK, IDENT:
		tok := p.advance()
		return &tok
	default:
		p.errorf("expected metadata value, got %s", p.tok.Kind)
		return nil
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
	p.parseMetadata(node)
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
	p.parseMetadata(node)
	return node
}

func (p *parser) parseInclude() *Node {
	// include "path"
	node := &Node{Kind: IncludeDirective}
	kw := p.advance()
	node.AddToken(&kw)
	path := p.expect(STRING)
	node.AddToken(&path)
	p.parseMetadata(node)
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

func (p *parser) parseDatedDirective() *Node {
	date := p.advance() // consume DATE

	if p.peek() == STAR || p.peek() == BANG {
		// Transaction — will be handled in Step 11
		return p.parseDatedUnrecognized(&date)
	}
	if p.peek() != IDENT {
		return p.parseDatedUnrecognized(&date)
	}

	// Now p.peek() == IDENT for certain
	switch p.tok.Raw {
	case "txn":
		// Transaction — will be handled in Step 11
		return p.parseDatedUnrecognized(&date)
	case "close":
		return p.parseClose(&date)
	case "commodity":
		return p.parseCommodity(&date)
	case "note":
		return p.parseNote(&date)
	case "document":
		return p.parseDocument(&date)
	case "event":
		return p.parseEvent(&date)
	case "query":
		return p.parseQuery(&date)
	case "price":
		return p.parsePrice(&date)
	default:
		return p.parseDatedUnrecognized(&date)
	}
}

func (p *parser) parseDatedUnrecognized(date *Token) *Node {
	node := &Node{Kind: UnrecognizedLineNode}
	node.AddToken(date)
	for p.peek() != EOF && !p.isAtNextLine() {
		tok := p.advance()
		node.AddToken(&tok)
	}
	return node
}

func (p *parser) parseClose(date *Token) *Node {
	// YYYY-MM-DD close Account
	node := &Node{Kind: CloseDirective}
	node.AddToken(date)
	kw := p.advance() // consume "close" IDENT
	node.AddToken(&kw)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)
	p.parseMetadata(node)
	return node
}

func (p *parser) parseCommodity(date *Token) *Node {
	// YYYY-MM-DD commodity Currency
	node := &Node{Kind: CommodityDirective}
	node.AddToken(date)
	kw := p.advance()
	node.AddToken(&kw)
	cur := p.expect(CURRENCY)
	node.AddToken(&cur)
	p.parseMetadata(node)
	return node
}

func (p *parser) parseNote(date *Token) *Node {
	// YYYY-MM-DD note Account "description"
	node := &Node{Kind: NoteDirective}
	node.AddToken(date)
	kw := p.advance()
	node.AddToken(&kw)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)
	desc := p.expect(STRING)
	node.AddToken(&desc)
	p.parseMetadata(node)
	return node
}

func (p *parser) parseDocument(date *Token) *Node {
	// YYYY-MM-DD document Account "path"
	node := &Node{Kind: DocumentDirective}
	node.AddToken(date)
	kw := p.advance()
	node.AddToken(&kw)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)
	path := p.expect(STRING)
	node.AddToken(&path)
	p.parseMetadata(node)
	return node
}

func (p *parser) parseEvent(date *Token) *Node {
	// YYYY-MM-DD event "name" "value"
	node := &Node{Kind: EventDirective}
	node.AddToken(date)
	kw := p.advance()
	node.AddToken(&kw)
	name := p.expect(STRING)
	node.AddToken(&name)
	val := p.expect(STRING)
	node.AddToken(&val)
	p.parseMetadata(node)
	return node
}

func (p *parser) parseQuery(date *Token) *Node {
	// YYYY-MM-DD query "name" "sql"
	node := &Node{Kind: QueryDirective}
	node.AddToken(date)
	kw := p.advance()
	node.AddToken(&kw)
	name := p.expect(STRING)
	node.AddToken(&name)
	sql := p.expect(STRING)
	node.AddToken(&sql)
	p.parseMetadata(node)
	return node
}

func (p *parser) parsePrice(date *Token) *Node {
	// YYYY-MM-DD price Commodity Amount
	node := &Node{Kind: PriceDirective}
	node.AddToken(date)
	kw := p.advance()
	node.AddToken(&kw)
	commodity := p.expect(CURRENCY)
	node.AddToken(&commodity)
	node.AddNode(p.parseAmount())
	p.parseMetadata(node)
	return node
}

func (p *parser) parseAmount() *Node {
	// Amount = Number Currency
	node := &Node{Kind: AmountNode}
	num := p.expect(NUMBER)
	node.AddToken(&num)
	cur := p.expect(CURRENCY)
	node.AddToken(&cur)
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
