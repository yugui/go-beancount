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
		filename:   filename,
		file:       &File{Filename: filename},
		activeTags: make(map[string]struct{}),
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
	filename   string
	file       *File
	activeTags map[string]struct{} // tags pushed via pushtag, not yet popped
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
		l.handlePushtag(n)
	case syntax.PoptagDirective:
		l.handlePoptag(n)
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
		l.lowerCustom(n)
	case syntax.TransactionDirective:
		l.lowerTransaction(n)
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

// lowerMetadata extracts all MetadataLineNode children from a CST node
// and returns a Metadata map.
func (l *lowerer) lowerMetadata(n *syntax.Node) Metadata {
	metaNodes := n.FindAllNodes(syntax.MetadataLineNode)
	if len(metaNodes) == 0 {
		return Metadata{}
	}
	meta := Metadata{Props: make(map[string]MetaValue)}
	for _, mn := range metaNodes {
		key, val, ok := l.lowerMetadataLine(mn)
		if ok {
			meta.Props[key] = val
		}
	}
	return meta
}

// lowerMetadataLine converts a MetadataLineNode into a key-value pair.
func (l *lowerer) lowerMetadataLine(n *syntax.Node) (string, MetaValue, bool) {
	// First child token is the key (IDENT).
	keyTok := n.FindToken(syntax.IDENT)
	if keyTok == nil {
		l.addDiagnostic(n, "metadata line missing key")
		return "", MetaValue{}, false
	}
	key := keyTok.Raw

	// Find the value token (after COLON).
	var valueTok *syntax.Token
	foundColon := false
	for _, c := range n.Children {
		if c.Token != nil {
			if c.Token.Kind == syntax.COLON {
				foundColon = true
				continue
			}
			if foundColon {
				valueTok = c.Token
				break
			}
		}
	}

	if valueTok == nil {
		l.addDiagnostic(n, "metadata line missing value")
		return "", MetaValue{}, false
	}

	val := l.tokenToMetaValue(n, valueTok)
	return key, val, true
}

// tokenToMetaValue converts a token into a MetaValue based on its kind.
// The node parameter is used for diagnostic reporting on parse errors.
func (l *lowerer) tokenToMetaValue(n *syntax.Node, t *syntax.Token) MetaValue {
	// Booleans TRUE/FALSE may be lexed as either CURRENCY or IDENT tokens.
	if (t.Kind == syntax.CURRENCY || t.Kind == syntax.IDENT) && (t.Raw == "TRUE" || t.Raw == "FALSE") {
		return MetaValue{Kind: MetaBool, Bool: t.Raw == "TRUE"}
	}

	switch t.Kind {
	case syntax.STRING:
		return MetaValue{Kind: MetaString, String: unquoteString(t)}
	case syntax.ACCOUNT:
		return MetaValue{Kind: MetaAccount, String: t.Raw}
	case syntax.CURRENCY:
		return MetaValue{Kind: MetaCurrency, String: t.Raw}
	case syntax.DATE:
		d, err := parseDate(t)
		if err != nil {
			l.addDiagnostic(n, fmt.Sprintf("invalid metadata date %q: %v", t.Raw, err))
			return MetaValue{Kind: MetaString, String: t.Raw}
		}
		return MetaValue{Kind: MetaDate, Date: d}
	case syntax.TAG:
		return MetaValue{Kind: MetaTag, String: t.Raw}
	case syntax.LINK:
		return MetaValue{Kind: MetaLink, String: t.Raw}
	case syntax.NUMBER:
		num, err := parseNumber(t)
		if err != nil {
			l.addDiagnostic(n, fmt.Sprintf("invalid metadata number %q: %v", t.Raw, err))
			return MetaValue{Kind: MetaString, String: t.Raw}
		}
		return MetaValue{Kind: MetaNumber, Number: num}
	case syntax.IDENT:
		return MetaValue{Kind: MetaString, String: t.Raw}
	default:
		return MetaValue{Kind: MetaString, String: t.Raw}
	}
}

// handlePushtag adds a tag to the active tag set.
func (l *lowerer) handlePushtag(n *syntax.Node) {
	tagTok := n.FindToken(syntax.TAG)
	if tagTok == nil {
		l.addDiagnostic(n, "pushtag missing tag")
		return
	}
	tag := tagTok.Raw[1:] // strip # prefix
	l.activeTags[tag] = struct{}{}
}

// handlePoptag removes a tag from the active tag set.
func (l *lowerer) handlePoptag(n *syntax.Node) {
	tagTok := n.FindToken(syntax.TAG)
	if tagTok == nil {
		l.addDiagnostic(n, "poptag missing tag")
		return
	}
	tag := tagTok.Raw[1:] // strip # prefix
	if _, ok := l.activeTags[tag]; !ok {
		l.addDiagnostic(n, fmt.Sprintf("poptag: tag %q was not previously pushed", tag))
		return
	}
	delete(l.activeTags, tag)
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
		Account:    Account(acctTok.Raw),
		Currencies: currencies,
		Booking:    booking,
		Meta:       l.lowerMetadata(n),
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
		Account: Account(acctTok.Raw),
		Meta:    l.lowerMetadata(n),
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
		Meta:     l.lowerMetadata(n),
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

	// The balance directive body is a BalanceAmountNode containing:
	//   ArithExprNode            (main number)
	//   [TILDE ArithExprNode]    (optional tolerance number)
	//   CURRENCY                 (single, shared currency)
	body := n.FindNode(syntax.BalanceAmountNode)
	if body == nil {
		l.addDiagnostic(n, "balance directive missing amount")
		return
	}
	exprNodes := body.FindAllNodes(syntax.ArithExprNode)
	if len(exprNodes) == 0 {
		l.addDiagnostic(n, "balance directive missing amount")
		return
	}
	currTok := body.FindToken(syntax.CURRENCY)
	if currTok == nil {
		l.addDiagnostic(n, "balance directive missing currency")
		return
	}
	num, ok := l.evalExpr(exprNodes[0])
	if !ok {
		return
	}

	bal := &Balance{
		Span:    l.spanFromNode(n),
		Date:    date,
		Account: Account(acctTok.Raw),
		Amount:  Amount{Number: num, Currency: currTok.Raw},
		Meta:    l.lowerMetadata(n),
	}

	// Optional tolerance: the second ArithExprNode after the TILDE token.
	if len(exprNodes) >= 2 && body.FindToken(syntax.TILDE) != nil {
		tol, ok := l.evalExpr(exprNodes[1])
		if ok {
			bal.Tolerance = &tol
		}
	}

	l.addDirective(bal)
}

// lowerAmount converts an AmountNode CST node into an Amount. It requires
// the currency token to be present; use lowerAmountOptionalCurrency when the
// currency may be absent.
func (l *lowerer) lowerAmount(n *syntax.Node) (Amount, bool) {
	amt, ok := l.lowerAmountOptionalCurrency(n)
	if !ok {
		return Amount{}, false
	}
	if amt.Currency == "" {
		l.addDiagnostic(n, "amount missing currency")
		return Amount{}, false
	}
	return amt, true
}

// lowerAmountOptionalCurrency converts an AmountNode whose currency token may
// be absent. It is used for the per-unit side of a combined cost spec
// `{X # Y CUR}`, where the per-unit amount may omit its currency and inherit
// it from the total side. The returned Amount has an empty Currency field
// when the source omitted the currency token.
func (l *lowerer) lowerAmountOptionalCurrency(n *syntax.Node) (Amount, bool) {
	exprNode := n.FindNode(syntax.ArithExprNode)
	if exprNode == nil {
		l.addDiagnostic(n, "amount missing expression")
		return Amount{}, false
	}
	num, ok := l.evalExpr(exprNode)
	if !ok {
		return Amount{}, false
	}
	amt := Amount{Number: num}
	if currTok := n.FindToken(syntax.CURRENCY); currTok != nil {
		amt.Currency = currTok.Raw
	}
	return amt, true
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
		Account:    Account(acctTokens[0].Raw),
		PadAccount: Account(acctTokens[1].Raw),
		Meta:       l.lowerMetadata(n),
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
		Account: Account(acctTok.Raw),
		Comment: unquoteString(strTokens[0]),
		Meta:    l.lowerMetadata(n),
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
		Account: Account(acctTok.Raw),
		Path:    unquoteString(strTokens[0]),
		Meta:    l.lowerMetadata(n),
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
		Meta:  l.lowerMetadata(n),
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
		Meta: l.lowerMetadata(n),
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
		Meta:      l.lowerMetadata(n),
	})
}

// lowerPosting converts a PostingNode CST node into a Posting.
func (l *lowerer) lowerPosting(n *syntax.Node) (Posting, bool) {
	p := Posting{
		Span: l.spanFromNode(n),
	}

	// Optional flag
	if n.FindToken(syntax.STAR) != nil {
		p.Flag = '*'
	} else if n.FindToken(syntax.BANG) != nil {
		p.Flag = '!'
	}

	// Account (required)
	acctTok := n.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		l.addDiagnostic(n, "posting missing account")
		return Posting{}, false
	}
	p.Account = Account(acctTok.Raw)

	// Optional amount
	amountNode := n.FindNode(syntax.AmountNode)
	if amountNode != nil {
		amt, ok := l.lowerAmount(amountNode)
		if !ok {
			return Posting{}, false
		}
		p.Amount = &amt
	}

	// Optional cost spec
	costNode := n.FindNode(syntax.CostSpecNode)
	if costNode != nil {
		cs, ok := l.lowerCostSpec(costNode)
		if !ok {
			return Posting{}, false
		}
		p.Cost = &cs
	}

	// Optional price annotation
	priceNode := n.FindNode(syntax.PriceAnnotNode)
	if priceNode != nil {
		pa, ok := l.lowerPriceAnnotation(priceNode)
		if !ok {
			return Posting{}, false
		}
		p.Price = &pa
	}

	// Metadata
	p.Meta = l.lowerMetadata(n)

	return p, true
}

// lowerCostSpec converts a CostSpecNode into a CostSpec.
func (l *lowerer) lowerCostSpec(n *syntax.Node) (CostSpec, bool) {
	cs := CostSpec{
		Span: l.spanFromNode(n),
	}

	// Determine per-unit vs total by checking for LBRACE2.
	isTotal := n.FindToken(syntax.LBRACE2) != nil

	// Detect combined form: a HASH child token signals "{ perUnit # total CUR }".
	if n.FindToken(syntax.HASH) != nil {
		// {{...#...}} should have been rejected by the parser; guard defensively.
		if isTotal {
			l.addDiagnostic(n, "'#' separator is not allowed inside total-cost braces {{...}}")
			return CostSpec{}, false
		}

		// Collect direct-child AmountNodes in source order.
		var amountNodes []*syntax.Node
		for _, c := range n.Children {
			if c.Node != nil && c.Node.Kind == syntax.AmountNode {
				amountNodes = append(amountNodes, c.Node)
			}
		}
		if len(amountNodes) < 2 {
			l.addDiagnostic(n, "combined cost form requires two amounts")
			return CostSpec{}, false
		}
		// The parser guarantees at most two AmountNodes in the combined form;
		// any extras are silently ignored.

		// Lower the total side first: it must have an explicit currency.
		total, ok := l.lowerAmount(amountNodes[1])
		if !ok {
			return CostSpec{}, false
		}
		if total.Currency == "" {
			// Should be unreachable: parser requires currency on the total side.
			l.addDiagnostic(amountNodes[1], "total amount in combined cost form requires a currency")
			return CostSpec{}, false
		}

		// Lower the per-unit side. The parser allows it to omit its currency,
		// in which case lowerAmount errors out. Use lowerAmountOptionalCurrency
		// to permit a currency-less amount and inherit from the total side.
		perUnit, ok := l.lowerAmountOptionalCurrency(amountNodes[0])
		if !ok {
			return CostSpec{}, false
		}
		if perUnit.Currency == "" {
			perUnit.Currency = total.Currency
		} else if perUnit.Currency != total.Currency {
			l.addDiagnostic(n, fmt.Sprintf("mismatched currencies in combined cost form: %q and %q", perUnit.Currency, total.Currency))
			return CostSpec{}, false
		}

		cs.PerUnit = &perUnit
		cs.Total = &total
	} else if amountNode := n.FindNode(syntax.AmountNode); amountNode != nil {
		// Single-amount form: { X CUR } or {{ X CUR }}.
		amt, ok := l.lowerAmount(amountNode)
		if !ok {
			return CostSpec{}, false
		}
		if isTotal {
			cs.Total = &amt
		} else {
			cs.PerUnit = &amt
		}
	}

	// Extract optional date.
	dateTok := n.FindToken(syntax.DATE)
	if dateTok != nil {
		d, err := parseDate(dateTok)
		if err != nil {
			l.addDiagnostic(n, fmt.Sprintf("invalid cost date %q: %v", dateTok.Raw, err))
			return CostSpec{}, false
		}
		cs.Date = &d
	}

	// Extract optional label.
	strTok := n.FindToken(syntax.STRING)
	if strTok != nil {
		cs.Label = unquoteString(strTok)
	}

	return cs, true
}

// lowerPriceAnnotation converts a PriceAnnotNode into a PriceAnnotation.
func (l *lowerer) lowerPriceAnnotation(n *syntax.Node) (PriceAnnotation, bool) {
	pa := PriceAnnotation{
		Span: l.spanFromNode(n),
	}

	// Check for @@ (total) vs @ (per-unit).
	pa.IsTotal = n.FindToken(syntax.ATAT) != nil

	// Extract the amount.
	amountNode := n.FindNode(syntax.AmountNode)
	if amountNode == nil {
		l.addDiagnostic(n, "price annotation missing amount")
		return PriceAnnotation{}, false
	}
	amt, ok := l.lowerAmount(amountNode)
	if !ok {
		return PriceAnnotation{}, false
	}
	pa.Amount = amt

	return pa, true
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

// lowerTransaction converts a TransactionDirective CST node into a Transaction AST directive.
func (l *lowerer) lowerTransaction(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "transaction missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}

	txn := &Transaction{
		Span: l.spanFromNode(n),
		Date: date,
	}

	// Extract flag, payee/narration, tags, links from direct child tokens.
	var strs []*syntax.Token
	for _, c := range n.Children {
		if c.Token == nil {
			continue
		}
		switch c.Token.Kind {
		case syntax.STAR:
			txn.Flag = '*'
		case syntax.BANG:
			txn.Flag = '!'
		case syntax.IDENT:
			// Only "txn" is a valid flag keyword; other IDENTs (e.g.,
			// directive keywords consumed by the parser) are ignored.
			if c.Token.Raw == "txn" {
				txn.Flag = '*'
			}
		case syntax.STRING:
			strs = append(strs, c.Token)
		case syntax.TAG:
			// TAG token Raw is "#tag-name" — strip the # prefix.
			txn.Tags = append(txn.Tags, c.Token.Raw[1:])
		case syntax.LINK:
			// LINK token Raw is "^link-name" — strip the ^ prefix.
			txn.Links = append(txn.Links, c.Token.Raw[1:])
		}
	}

	// Resolve payee vs narration.
	if len(strs) == 1 {
		txn.Narration = unquoteString(strs[0])
	} else if len(strs) >= 2 {
		txn.Payee = unquoteString(strs[0])
		txn.Narration = unquoteString(strs[1])
	}

	// Merge active (pushed) tags into this transaction.
	for tag := range l.activeTags {
		// Avoid duplicates: only add if not already present.
		found := false
		for _, t := range txn.Tags {
			if t == tag {
				found = true
				break
			}
		}
		if !found {
			txn.Tags = append(txn.Tags, tag)
		}
	}

	// Lower postings.
	for _, postingNode := range n.FindAllNodes(syntax.PostingNode) {
		p, ok := l.lowerPosting(postingNode)
		if ok {
			txn.Postings = append(txn.Postings, p)
		}
	}

	// Transaction-level metadata.
	txn.Meta = l.lowerMetadata(n)

	l.addDirective(txn)
}

// lowerCustom converts a CustomDirective CST node into a Custom AST directive.
func (l *lowerer) lowerCustom(n *syntax.Node) {
	dateTok := n.FindToken(syntax.DATE)
	if dateTok == nil {
		l.addDiagnostic(n, "custom directive missing date")
		return
	}
	date, err := parseDate(dateTok)
	if err != nil {
		l.addDiagnostic(n, fmt.Sprintf("invalid date %s: %v", dateTok.Raw, err))
		return
	}

	// Find the type name (first STRING child) and its index.
	typeNameIdx := -1
	var typeName string
	for i, c := range n.Children {
		if c.Token != nil && c.Token.Kind == syntax.STRING {
			typeNameIdx = i
			typeName = unquoteString(c.Token)
			break
		}
	}
	if typeNameIdx < 0 {
		l.addDiagnostic(n, "custom directive missing type name")
		return
	}

	// Skip the type name and collect the remaining children as values.
	var values []MetaValue
	for _, c := range n.Children[typeNameIdx+1:] {
		if c.Token != nil {
			values = append(values, l.tokenToMetaValue(n, c.Token))
		} else if c.Node != nil && c.Node.Kind == syntax.AmountNode {
			amt, ok := l.lowerAmount(c.Node)
			if ok {
				values = append(values, MetaValue{Kind: MetaAmount, Amount: amt})
			}
		}
	}

	l.addDirective(&Custom{
		Span:     l.spanFromNode(n),
		Date:     date,
		TypeName: typeName,
		Values:   values,
		Meta:     l.lowerMetadata(n),
	})
}
