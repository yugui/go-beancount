package syntax

import (
	"fmt"
	"io"
	"os"
)

// Parse parses a beancount source string into a concrete syntax tree.
func Parse(src string) *File {
	p := &parser{
		scanner: newScanner(src),
		src:     src,
	}
	p.advance() // read first token into p.tok
	return p.parseFile()
}

// ParseReader reads the entire contents of r and parses it as beancount
// source. Read errors are returned unwrapped; parse errors are surfaced
// through the returned File's Errors field.
func ParseReader(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Parse(string(data)), nil
}

// ParseFile opens path and parses its contents as beancount source. The file
// is closed before ParseFile returns. Open/read errors are returned unwrapped;
// parse errors are surfaced through the returned File's Errors field.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseReader(f)
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
		return p.parseTransaction(&date)
	}
	if p.peek() != IDENT {
		return p.parseDatedUnrecognized(&date)
	}

	// Now p.peek() == IDENT for certain
	switch p.tok.Raw {
	case "txn":
		return p.parseTransaction(&date)
	case "open":
		return p.parseOpen(&date)
	case "balance":
		return p.parseBalance(&date)
	case "pad":
		return p.parsePad(&date)
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
	case "custom":
		return p.parseCustom(&date)
	default:
		return p.parseDatedUnrecognized(&date)
	}
}

// parseTransaction parses a transaction directive:
// DATE (STAR | BANG | "txn") [payee] [narration] [tags/links].
func (p *parser) parseTransaction(date *Token) *Node {
	node := &Node{Kind: TransactionDirective}
	node.AddToken(date)

	// Consume flag: STAR, BANG, or IDENT("txn")
	flag := p.advance()
	node.AddToken(&flag)

	// Optional payee and narration (on the same line)
	if p.peek() == STRING && !p.isAtNextLine() {
		s1 := p.advance()
		if p.peek() == STRING && !p.isAtNextLine() {
			// Two strings: s1 is payee, s2 is narration
			node.AddToken(&s1)
			s2 := p.advance()
			node.AddToken(&s2)
		} else {
			// One string: s1 is narration
			node.AddToken(&s1)
		}
	}

	// Optional tags and links (on the same line)
	for !p.isAtNextLine() && p.peek() != EOF {
		if p.peek() == TAG {
			tag := p.advance()
			node.AddToken(&tag)
		} else if p.peek() == LINK {
			link := p.advance()
			node.AddToken(&link)
		} else {
			break
		}
	}

	// Postings and metadata on indented lines
	p.parsePostingsAndMetadata(node)

	return node
}

// parsePostingsAndMetadata parses indented posting and metadata lines
// after a transaction header. Metadata lines before any posting attach to
// the transaction node. Metadata lines after a posting attach to that posting.
func (p *parser) parsePostingsAndMetadata(txn *Node) {
	var lastPosting *Node

	for p.isAtNextLine() && p.isIndented() && p.peek() != EOF {
		if p.peek() == ACCOUNT || p.peek() == STAR || p.peek() == BANG {
			lastPosting = p.parsePosting()
			txn.AddNode(lastPosting)
		} else if p.peek() == IDENT {
			meta := p.tryParseMetadataLine()
			if meta != nil {
				if lastPosting != nil {
					lastPosting.AddNode(meta)
				} else {
					txn.AddNode(meta)
				}
			} else {
				return
			}
		} else {
			return
		}
	}
}

// parsePosting parses a posting line: [STAR|BANG] ACCOUNT [Amount].
func (p *parser) parsePosting() *Node {
	node := &Node{Kind: PostingNode}

	// Optional flag
	if p.peek() == STAR || p.peek() == BANG {
		flag := p.advance()
		node.AddToken(&flag)
	}

	// Account (required)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)

	// Optional amount (NUMBER, MINUS, PLUS, or LPAREN on the same line indicates an amount follows)
	if !p.isAtNextLine() && (p.peek() == NUMBER || p.peek() == MINUS || p.peek() == PLUS || p.peek() == LPAREN) {
		node.AddNode(p.parseAmount())

		// Optional cost spec: { ... } or {{ ... }}
		if !p.isAtNextLine() && (p.peek() == LBRACE || p.peek() == LBRACE2) {
			node.AddNode(p.parseCostSpec())
		}

		// Optional price annotation: @ or @@
		if !p.isAtNextLine() && (p.peek() == AT || p.peek() == ATAT) {
			node.AddNode(p.parsePriceAnnotation())
		}
	}

	return node
}

// parseCostSpec parses a cost specification: {Amount [, Date] [, Label]}, {{Amount}}, {}, or {{}}.
// The per-unit form `{...}` also accepts a combined "{X # Y CUR}" shape where the
// per-unit amount may omit its currency and inherit it from the total amount.
func (p *parser) parseCostSpec() *Node {
	node := &Node{Kind: CostSpecNode}

	if p.peek() == LBRACE2 {
		// Total cost: {{ ... }}
		open := p.advance()
		node.AddToken(&open)
		p.parseCostContents(node, true /*isTotalBraces*/)
		close := p.expect(RBRACE2)
		node.AddToken(&close)
	} else {
		// Per-unit cost: { ... }
		open := p.advance() // consume LBRACE
		node.AddToken(&open)
		p.parseCostContents(node, false /*isTotalBraces*/)
		close := p.expect(RBRACE)
		node.AddToken(&close)
	}

	return node
}

// parseCostContents parses comma-separated elements inside a cost spec.
// The leading element may be an Amount (with currency optional when the
// combined per-unit `#` total form is used and only outside `{{...}}`),
// a Date, or a String label; subsequent elements follow parseCostElement.
func (p *parser) parseCostContents(node *Node, isTotalBraces bool) {
	// Could be empty: {} or {{}}
	if p.peek() == RBRACE || p.peek() == RBRACE2 {
		return
	}

	// Parse first element. The first element may be an amount whose currency
	// is omitted when followed by `#` (the currency is then inherited from the
	// total-side amount). We therefore special-case the leading amount here so
	// we can accept a currency-less per-unit amount only in the combined form.
	switch p.peek() {
	case NUMBER, MINUS, PLUS, LPAREN:
		// Parse an amount that may optionally omit its currency.
		amt := p.parseAmountOptionalCurrency()
		node.AddNode(amt)

		// Combined form: { perUnit # total CUR }
		if p.peek() == HASH {
			if isTotalBraces {
				p.errorf("'#' separator is not allowed inside total-cost braces {{...}}")
				// Record the error but continue consuming '#' and the total amount
				// so later diagnostics are not spurious.
			}
			hash := p.advance()
			node.AddToken(&hash)

			// The total amount must be a regular Amount with a currency.
			if p.peek() != NUMBER && p.peek() != MINUS && p.peek() != PLUS && p.peek() != LPAREN {
				p.errorf("expected total amount after '#' in cost spec, got %s", p.tok.Kind)
				// Skip a stray token only if it cannot close the cost spec; otherwise
				// leave it for parseCostSpec to consume so we don't emit a spurious
				// "expected '}'" diagnostic on inputs like `{502.12 #}`.
				if p.peek() != RBRACE && p.peek() != RBRACE2 && p.peek() != EOF {
					tok := p.advance()
					node.AddToken(&tok)
				}
			} else {
				node.AddNode(p.parseAmount())
			}
		} else if amt.FindToken(CURRENCY) == nil {
			// A currency-less amount is only permitted before `#`.
			p.errorf("expected currency after amount in cost spec, got %s", p.tok.Kind)
		}
	case DATE:
		date := p.advance()
		node.AddToken(&date)
	case STRING:
		label := p.advance()
		node.AddToken(&label)
	default:
		p.errorf("expected amount, date, or label in cost spec, got %s", p.tok.Kind)
		tok := p.advance() // skip unexpected token to ensure forward progress
		node.AddToken(&tok)
	}

	// Parse comma-separated additional elements
	for p.peek() == COMMA {
		comma := p.advance()
		node.AddToken(&comma)
		p.parseCostElement(node)
	}
}

// parseCostElement parses a single trailing element inside a cost spec.
// Can be: Amount, Date, or String (label). This is used for elements after
// the first one (i.e. after a comma); the first element is handled inline by
// parseCostContents because it may participate in the combined `#` form.
func (p *parser) parseCostElement(node *Node) {
	switch p.peek() {
	case NUMBER, MINUS, PLUS, LPAREN:
		node.AddNode(p.parseAmount())
	case DATE:
		date := p.advance()
		node.AddToken(&date)
	case STRING:
		label := p.advance()
		node.AddToken(&label)
	default:
		p.errorf("expected amount, date, or label in cost spec, got %s", p.tok.Kind)
		tok := p.advance() // skip unexpected token to ensure forward progress
		node.AddToken(&tok)
	}
}

// parseAmountOptionalCurrency parses an amount whose currency is optional.
// It is used for the per-unit side of a combined cost spec `{X # Y CUR}`,
// where the per-unit amount may omit its currency and inherit it from the
// total side. Callers are responsible for enforcing currency presence when
// the optional form is not allowed.
func (p *parser) parseAmountOptionalCurrency() *Node {
	node := &Node{Kind: AmountNode}
	node.AddNode(p.parseExpr())
	if p.peek() == CURRENCY {
		cur := p.advance()
		node.AddToken(&cur)
	}
	return node
}

// parsePriceAnnotation parses a price annotation: @ Amount or @@ Amount.
func (p *parser) parsePriceAnnotation() *Node {
	node := &Node{Kind: PriceAnnotNode}
	op := p.advance() // consume AT or ATAT
	node.AddToken(&op)
	node.AddNode(p.parseAmount())
	return node
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

func (p *parser) parseOpen(date *Token) *Node {
	// YYYY-MM-DD open Account [Currency,...] ["BookingMethod"]
	node := &Node{Kind: OpenDirective}
	node.AddToken(date)
	kw := p.advance() // consume "open"
	node.AddToken(&kw)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)

	// Optional currency constraint list
	if p.peek() == CURRENCY && !p.isAtNextLine() {
		cur := p.advance()
		node.AddToken(&cur)
		for p.peek() == COMMA && !p.isAtNextLine() {
			comma := p.advance()
			node.AddToken(&comma)
			cur := p.expect(CURRENCY)
			node.AddToken(&cur)
		}
	}

	// Optional booking method
	if p.peek() == STRING && !p.isAtNextLine() {
		bm := p.advance()
		node.AddToken(&bm)
	}

	p.parseMetadata(node)
	return node
}

func (p *parser) parseBalance(date *Token) *Node {
	// YYYY-MM-DD balance Account Number [~ Number] Currency
	node := &Node{Kind: BalanceDirective}
	node.AddToken(date)
	kw := p.advance() // consume "balance"
	node.AddToken(&kw)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)
	node.AddNode(p.parseBalanceAmount())

	p.parseMetadata(node)
	return node
}

// parseBalanceAmount parses the balance directive body:
//
//	Number [~ Number] Currency
//
// The currency appears once at the end and applies to both the main number
// and the optional tolerance number.
func (p *parser) parseBalanceAmount() *Node {
	node := &Node{Kind: BalanceAmountNode}
	node.AddNode(p.parseExpr())
	if p.peek() == TILDE && !p.isAtNextLine() {
		tilde := p.advance()
		node.AddToken(&tilde)
		node.AddNode(p.parseExpr())
	}
	cur := p.expect(CURRENCY)
	node.AddToken(&cur)
	// Reject stray tokens after the currency on the same logical line.
	// This catches the old non-standard `Number Currency ~ Number Currency`
	// syntax: after consuming the first currency we're still on the same
	// line, so any further tokens are an error. Consume them into this
	// node to ensure forward progress and preserve trivia.
	if !p.isAtNextLine() && p.peek() != EOF {
		p.errorf("unexpected token %s after balance amount", p.tok.Kind)
		for !p.isAtNextLine() && p.peek() != EOF {
			tok := p.advance()
			node.AddToken(&tok)
		}
	}
	return node
}

func (p *parser) parsePad(date *Token) *Node {
	// YYYY-MM-DD pad Account AccountPad
	node := &Node{Kind: PadDirective}
	node.AddToken(date)
	kw := p.advance() // consume "pad"
	node.AddToken(&kw)
	acct := p.expect(ACCOUNT)
	node.AddToken(&acct)
	pad := p.expect(ACCOUNT)
	node.AddToken(&pad)
	p.parseMetadata(node)
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

func (p *parser) parseCustom(date *Token) *Node {
	// YYYY-MM-DD custom "type" Value...
	node := &Node{Kind: CustomDirective}
	node.AddToken(date)
	kw := p.advance() // consume "custom"
	node.AddToken(&kw)
	typeName := p.expect(STRING)
	node.AddToken(&typeName)

	// Variable-length value list on the same line
	for !p.isAtNextLine() && p.peek() != EOF {
		if p.at(STRING, DATE, ACCOUNT, CURRENCY, IDENT) {
			tok := p.advance()
			node.AddToken(&tok)
		} else if p.at(NUMBER, MINUS, PLUS, LPAREN) {
			node.AddNode(p.parseAmount())
		} else {
			break
		}
	}

	p.parseMetadata(node)
	return node
}

func (p *parser) parseAmount() *Node {
	// Amount = Expr Currency
	node := &Node{Kind: AmountNode}
	node.AddNode(p.parseExpr())
	cur := p.expect(CURRENCY)
	node.AddToken(&cur)
	return node
}

// parseExpr parses an additive expression: term (('+' | '-') term)*
func (p *parser) parseExpr() *Node {
	left := p.parseTerm()

	for p.peek() == PLUS || p.peek() == MINUS {
		node := &Node{Kind: ArithExprNode}
		node.AddNode(left)
		op := p.advance()
		node.AddToken(&op)
		right := p.parseTerm()
		node.AddNode(right)
		left = node
	}

	return left
}

// parseTerm parses a multiplicative expression: factor (('*' | '/') factor)*
func (p *parser) parseTerm() *Node {
	left := p.parseFactor()

	for p.peek() == STAR || p.peek() == SLASH {
		node := &Node{Kind: ArithExprNode}
		node.AddNode(left)
		op := p.advance()
		node.AddToken(&op)
		right := p.parseFactor()
		node.AddNode(right)
		left = node
	}

	return left
}

// parseFactor parses a primary expression: NUMBER, unary +/-, or '(' expr ')'
func (p *parser) parseFactor() *Node {
	switch p.peek() {
	case NUMBER:
		node := &Node{Kind: ArithExprNode}
		num := p.advance()
		node.AddToken(&num)
		return node
	case MINUS, PLUS:
		node := &Node{Kind: ArithExprNode}
		op := p.advance()
		node.AddToken(&op)
		operand := p.parseFactor()
		node.AddNode(operand)
		return node
	case LPAREN:
		node := &Node{Kind: ArithExprNode}
		lp := p.advance()
		node.AddToken(&lp)
		inner := p.parseExpr()
		node.AddNode(inner)
		rp := p.expect(RPAREN)
		node.AddToken(&rp)
		return node
	default:
		p.errorf("expected number or expression, got %s", p.tok.Kind)
		node := &Node{Kind: ArithExprNode}
		tok := p.advance()
		node.AddToken(&tok)
		return node
	}
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
