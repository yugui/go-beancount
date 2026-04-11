package ast

import (
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// Lower converts a CST file into an AST file.
// Directives that contain syntax errors are skipped and recorded as diagnostics.
// Include resolution is not performed; Include directives appear as AST nodes.
func Lower(filename string, cst *syntax.File) *File {
	l := &lowerer{
		filename: filename,
		file:     &File{Filename: filename},
	}
	// Convert CST-level errors to diagnostics.
	for _, e := range cst.Errors {
		l.file.Diagnostics = append(l.file.Diagnostics, Diagnostic{
			Span:     l.spanFromOffset(e.Pos),
			Message:  e.Msg,
			Severity: Error,
		})
	}
	// Walk top-level children.
	if cst.Root != nil {
		for _, child := range cst.Root.Children {
			if child.Node != nil {
				l.lowerDirective(child.Node)
			}
			// Top-level tokens (like EOF) are ignored.
		}
	}
	return l.file
}

type lowerer struct {
	filename string
	file     *File
}

func (l *lowerer) lowerDirective(n *syntax.Node) {
	switch n.Kind {
	case syntax.ErrorNode, syntax.UnrecognizedLineNode:
		l.addDiagnostic(n, "syntax error")
	case syntax.OptionDirective:
		l.lowerOption(n)
	case syntax.PluginDirective:
		l.lowerPlugin(n)
	case syntax.IncludeDirective:
		l.lowerInclude(n)
	case syntax.PushtagDirective:
		// TODO: step 14
	case syntax.PoptagDirective:
		// TODO: step 14
	case syntax.OpenDirective:
		l.lowerOpen(n)
	case syntax.CloseDirective:
		l.lowerClose(n)
	case syntax.CommodityDirective:
		l.lowerCommodity(n)
	case syntax.BalanceDirective:
		l.lowerBalance(n)
	case syntax.PadDirective:
		l.lowerPad(n)
	case syntax.NoteDirective:
		l.lowerNote(n)
	case syntax.DocumentDirective:
		l.lowerDocument(n)
	case syntax.PriceDirective:
		l.lowerPrice(n)
	case syntax.EventDirective:
		l.lowerEvent(n)
	case syntax.QueryDirective:
		l.lowerQuery(n)
	case syntax.CustomDirective:
		// TODO: step 15
	case syntax.TransactionDirective:
		// TODO: step 12
	default:
		l.addDiagnostic(n, fmt.Sprintf("unknown directive kind: %s", n.Kind))
	}
}

// spanFromNode computes a Span covering the entire subtree of n,
// from the first token's position to the end of the last token.
func (l *lowerer) spanFromNode(n *syntax.Node) Span {
	var first, last *syntax.Token
	for t := range n.Tokens() {
		if first == nil {
			first = t
		}
		last = t
	}
	if first == nil {
		return Span{}
	}
	return Span{
		Start: l.posFromToken(first),
		End: Position{
			Filename: l.filename,
			Offset:   last.Pos + len(last.Raw),
		},
	}
}

// spanFromOffset creates a zero-width Span from a byte offset.
// Line and Column are left as zero; they may be populated in future steps.
func (l *lowerer) spanFromOffset(offset int) Span {
	pos := Position{
		Filename: l.filename,
		Offset:   offset,
	}
	return Span{Start: pos, End: pos}
}

// posFromToken creates a Position from a token.
func (l *lowerer) posFromToken(t *syntax.Token) Position {
	return Position{
		Filename: l.filename,
		Offset:   t.Pos,
	}
}

// addDiagnostic records an Error-severity diagnostic for the given node.
func (l *lowerer) addDiagnostic(n *syntax.Node, msg string) {
	l.file.Diagnostics = append(l.file.Diagnostics, Diagnostic{
		Span:     l.spanFromNode(n),
		Message:  msg,
		Severity: Error,
	})
}

// addDirective appends a directive to the file.
func (l *lowerer) addDirective(d Directive) {
	l.file.Directives = append(l.file.Directives, d)
}

// findTokens returns all direct child tokens with the given kind.
func findTokens(n *syntax.Node, kind syntax.TokenKind) []*syntax.Token {
	var result []*syntax.Token
	for _, c := range n.Children {
		if c.Token != nil && c.Token.Kind == kind {
			result = append(result, c.Token)
		}
	}
	return result
}

// unquoteString strips surrounding double quotes from a STRING token's Raw value.
// The caller must ensure t is a valid STRING token; if not, the raw value is returned unchanged.
func unquoteString(t *syntax.Token) string {
	s := t.Raw
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// parseDate parses a DATE token's Raw value into time.Time.
// Beancount dates are YYYY-MM-DD or YYYY/MM/DD.
func parseDate(t *syntax.Token) (time.Time, error) {
	s := t.Raw
	// Normalize separator
	s = strings.ReplaceAll(s, "/", "-")
	return time.Parse("2006-01-02", s)
}

// lowerOpen converts an OpenDirective CST node into an Open AST directive.
func (l *lowerer) lowerOpen(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "open directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	acctTok := n.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		l.addDiagnostic(n, "open directive missing account")
		return
	}

	// Collect optional constraint currencies.
	currTokens := findTokens(n, syntax.CURRENCY)
	var currencies []string
	for _, ct := range currTokens {
		currencies = append(currencies, ct.Raw)
	}

	// Optional booking method (STRING token after account).
	var booking string
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) > 0 {
		booking = unquoteString(strTokens[0])
	}

	l.addDirective(&Open{
		Span:       l.spanFromNode(n),
		Date:       date,
		Account:    acctTok.Raw,
		Currencies: currencies,
		Booking:    booking,
		// TODO: populate Meta when metadata lowering is implemented.
	})
}

// lowerClose converts a CloseDirective CST node into a Close AST directive.
func (l *lowerer) lowerClose(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "close directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	acctTok := n.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		l.addDiagnostic(n, "close directive missing account")
		return
	}
	l.addDirective(&Close{
		Span:    l.spanFromNode(n),
		Date:    date,
		Account: acctTok.Raw,
		// TODO: populate Meta when metadata lowering is implemented.
	})
}

// lowerOption converts an OptionDirective CST node into an Option AST directive.
func (l *lowerer) lowerOption(n *syntax.Node) {
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 2 {
		l.addDiagnostic(n, "option directive requires two string arguments")
		return
	}
	l.addDirective(&Option{
		Span:  l.spanFromNode(n),
		Key:   unquoteString(strTokens[0]),
		Value: unquoteString(strTokens[1]),
	})
}

// lowerPlugin converts a PluginDirective CST node into a Plugin AST directive.
func (l *lowerer) lowerPlugin(n *syntax.Node) {
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 1 {
		l.addDiagnostic(n, "plugin directive requires a string argument")
		return
	}
	p := &Plugin{
		Span: l.spanFromNode(n),
		Name: unquoteString(strTokens[0]),
	}
	if len(strTokens) >= 2 {
		p.Config = unquoteString(strTokens[1])
	}
	l.addDirective(p)
}

// lowerCommodity converts a CommodityDirective CST node into a Commodity AST directive.
func (l *lowerer) lowerCommodity(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "commodity directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	currTok := n.FindToken(syntax.CURRENCY)
	if currTok == nil {
		l.addDiagnostic(n, "commodity directive missing currency")
		return
	}
	l.addDirective(&Commodity{
		Span:     l.spanFromNode(n),
		Date:     date,
		Currency: currTok.Raw,
		// TODO: populate Meta when metadata lowering is implemented.
	})
}

// lowerBalance converts a BalanceDirective CST node into a Balance AST directive.
func (l *lowerer) lowerBalance(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "balance directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	acctTok := n.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		l.addDiagnostic(n, "balance directive missing account")
		return
	}

	// The balance directive has one or two AmountNode children.
	// First is the expected amount; second (after TILDE) is the tolerance.
	amountNodes := n.FindAllNodes(syntax.AmountNode)
	if len(amountNodes) == 0 {
		l.addDiagnostic(n, "balance directive missing amount")
		return
	}
	amt, ok := l.lowerAmount(amountNodes[0])
	if !ok {
		return
	}

	bal := &Balance{
		Span:    l.spanFromNode(n),
		Date:    date,
		Account: acctTok.Raw,
		Amount:  amt,
		// TODO: populate Meta when metadata lowering is implemented.
	}

	// Optional tolerance (second AmountNode, after TILDE token).
	if len(amountNodes) >= 2 {
		tol, ok := l.lowerAmount(amountNodes[1])
		if ok {
			bal.Tolerance = &tol
		}
	}

	l.addDirective(bal)
}

// lowerAmount converts an AmountNode CST node into an Amount.
func (l *lowerer) lowerAmount(n *syntax.Node) (Amount, bool) {
	exprNode := n.FindNode(syntax.ArithExprNode)
	if exprNode == nil {
		l.addDiagnostic(n, "amount missing expression")
		return Amount{}, false
	}
	currTok := n.FindToken(syntax.CURRENCY)
	if currTok == nil {
		l.addDiagnostic(n, "amount missing currency")
		return Amount{}, false
	}
	num, ok := l.evalExpr(exprNode)
	if !ok {
		return Amount{}, false
	}
	return Amount{Number: num, Currency: currTok.Raw}, true
}

// arithCtx is the decimal context used for arithmetic expression evaluation.
// 34 digits of precision (128-bit decimal) is standard for financial calculations.
var arithCtx = apd.BaseContext.WithPrecision(34)

// evalExpr evaluates an ArithExprNode into an apd.Decimal.
func (l *lowerer) evalExpr(n *syntax.Node) (apd.Decimal, bool) {
	var nodes []*syntax.Node
	var tokens []*syntax.Token
	for _, c := range n.Children {
		if c.Node != nil {
			nodes = append(nodes, c.Node)
		}
		if c.Token != nil {
			tokens = append(tokens, c.Token)
		}
	}

	// Case 1: single NUMBER token (primary)
	if len(nodes) == 0 && len(tokens) == 1 && tokens[0].Kind == syntax.NUMBER {
		d, err := parseNumber(tokens[0])
		if err != nil {
			l.addDiagnostic(n, err.Error())
			return apd.Decimal{}, false
		}
		return d, true
	}

	// Case 2: parenthesized (LPAREN expr RPAREN)
	if len(nodes) == 1 && len(tokens) == 2 && tokens[0].Kind == syntax.LPAREN {
		return l.evalExpr(nodes[0])
	}

	// Case 3: unary prefix (PLUS/MINUS + expr)
	if len(nodes) == 1 && len(tokens) == 1 {
		op := tokens[0]
		if op.Kind == syntax.PLUS || op.Kind == syntax.MINUS {
			val, ok := l.evalExpr(nodes[0])
			if !ok {
				return apd.Decimal{}, false
			}
			if op.Kind == syntax.MINUS {
				val.Negative = !val.Negative
			}
			return val, true
		}
	}

	// Case 4: binary (expr op expr)
	if len(nodes) == 2 && len(tokens) == 1 {
		op := tokens[0]
		left, ok := l.evalExpr(nodes[0])
		if !ok {
			return apd.Decimal{}, false
		}
		right, ok := l.evalExpr(nodes[1])
		if !ok {
			return apd.Decimal{}, false
		}
		var result apd.Decimal
		var cond apd.Condition
		// The second return value from apd arithmetic is the updated context,
		// which we intentionally discard; all error state is captured in cond.
		switch op.Kind {
		case syntax.PLUS:
			cond, _ = arithCtx.Add(&result, &left, &right)
		case syntax.MINUS:
			cond, _ = arithCtx.Sub(&result, &left, &right)
		case syntax.STAR:
			cond, _ = arithCtx.Mul(&result, &left, &right)
		case syntax.SLASH:
			cond, _ = arithCtx.Quo(&result, &left, &right)
		default:
			l.addDiagnostic(n, fmt.Sprintf("unexpected operator %s in expression", op.Kind))
			return apd.Decimal{}, false
		}
		if cond.Any() {
			l.addDiagnostic(n, fmt.Sprintf("arithmetic error: %s", cond))
			return apd.Decimal{}, false
		}
		return result, true
	}

	l.addDiagnostic(n, "malformed arithmetic expression")
	return apd.Decimal{}, false
}

// parseNumber parses a NUMBER token into apd.Decimal.
// NUMBER tokens may contain commas (e.g., "1,234.56").
func parseNumber(t *syntax.Token) (apd.Decimal, error) {
	s := strings.ReplaceAll(t.Raw, ",", "")
	var d apd.Decimal
	_, _, err := d.SetString(s)
	if err != nil {
		return apd.Decimal{}, fmt.Errorf("invalid number %q: %w", t.Raw, err)
	}
	return d, nil
}

// lowerPad converts a PadDirective CST node into a Pad AST directive.
func (l *lowerer) lowerPad(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "pad directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	acctTokens := findTokens(n, syntax.ACCOUNT)
	if len(acctTokens) < 2 {
		l.addDiagnostic(n, "pad directive requires two accounts")
		return
	}
	l.addDirective(&Pad{
		Span:       l.spanFromNode(n),
		Date:       date,
		Account:    acctTokens[0].Raw,
		PadAccount: acctTokens[1].Raw,
	})
}

// lowerNote converts a NoteDirective CST node into a Note AST directive.
func (l *lowerer) lowerNote(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "note directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	acctTok := n.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		l.addDiagnostic(n, "note directive missing account")
		return
	}
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 1 {
		l.addDiagnostic(n, "note directive missing comment string")
		return
	}
	l.addDirective(&Note{
		Span:    l.spanFromNode(n),
		Date:    date,
		Account: acctTok.Raw,
		Comment: unquoteString(strTokens[0]),
	})
}

// lowerDocument converts a DocumentDirective CST node into a Document AST directive.
func (l *lowerer) lowerDocument(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "document directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	acctTok := n.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		l.addDiagnostic(n, "document directive missing account")
		return
	}
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 1 {
		l.addDiagnostic(n, "document directive missing path string")
		return
	}
	l.addDirective(&Document{
		Span:    l.spanFromNode(n),
		Date:    date,
		Account: acctTok.Raw,
		Path:    unquoteString(strTokens[0]),
	})
}

// lowerEvent converts an EventDirective CST node into an Event AST directive.
func (l *lowerer) lowerEvent(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "event directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 2 {
		l.addDiagnostic(n, "event directive requires two string arguments")
		return
	}
	l.addDirective(&Event{
		Span:  l.spanFromNode(n),
		Date:  date,
		Name:  unquoteString(strTokens[0]),
		Value: unquoteString(strTokens[1]),
	})
}

// lowerQuery converts a QueryDirective CST node into a Query AST directive.
func (l *lowerer) lowerQuery(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "query directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 2 {
		l.addDiagnostic(n, "query directive requires two string arguments")
		return
	}
	l.addDirective(&Query{
		Span: l.spanFromNode(n),
		Date: date,
		Name: unquoteString(strTokens[0]),
		BQL:  unquoteString(strTokens[1]),
	})
}

// lowerPrice converts a PriceDirective CST node into a Price AST directive.
func (l *lowerer) lowerPrice(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "price directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}
	commodityTok := n.FindToken(syntax.CURRENCY)
	if commodityTok == nil {
		l.addDiagnostic(n, "price directive missing commodity")
		return
	}
	amountNode := n.FindNode(syntax.AmountNode)
	if amountNode == nil {
		l.addDiagnostic(n, "price directive missing amount")
		return
	}
	amt, ok := l.lowerAmount(amountNode)
	if !ok {
		return
	}
	l.addDirective(&Price{
		Span:      l.spanFromNode(n),
		Date:      date,
		Commodity: commodityTok.Raw,
		Amount:    amt,
		// TODO: populate Meta when metadata lowering is implemented.
	})
}

// lowerInclude converts an IncludeDirective CST node into an Include AST directive.
func (l *lowerer) lowerInclude(n *syntax.Node) {
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 1 {
		l.addDiagnostic(n, "include directive requires a string argument")
		return
	}
	l.addDirective(&Include{
		Span: l.spanFromNode(n),
		Path: unquoteString(strTokens[0]),
	})
}
