package syntax

// ParseBalanceAmount parses src as a balance directive body of the form
//
//	<arith-expr> [~ <arith-expr>] <CURRENCY>
//
// returning the resulting BalanceAmountNode subtree and any parse errors.
// The returned Node is never nil; trailing input is reported as an error
// and consumed into the node.
func ParseBalanceAmount(src string) (*Node, []Error) {
	return parseStandalone(src, func(p *parser) *Node { return p.parseBalanceAmount() })
}

// ParseAmountExpression parses src as a single amount of the form
//
//	<arith-expr> <CURRENCY>
//
// returning an AmountNode subtree and any parse errors. The returned Node
// is never nil; trailing input is reported as an error and consumed into
// the node.
func ParseAmountExpression(src string) (*Node, []Error) {
	return parseStandalone(src, func(p *parser) *Node { return p.parseAmount() })
}

// parseStandalone drives production over src and treats any leftover
// tokens after EOF as a single "unexpected trailing input" error,
// draining them into the returned node so callers can still inspect
// the partial parse (error recovery).
func parseStandalone(src string, production func(*parser) *Node) (*Node, []Error) {
	p := &parser{scanner: newScanner(src), src: src}
	p.advance()
	node := production(p)
	if p.peek() != EOF {
		p.errorf("unexpected trailing input")
		for p.peek() != EOF {
			tok := p.advance()
			node.AddToken(&tok)
		}
	}
	return node, p.errors
}
