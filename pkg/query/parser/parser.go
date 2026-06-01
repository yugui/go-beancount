package parser

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
)

// Error is the error type returned by Parse for every lexical or syntactic
// failure. It carries the source Position at which the problem was detected.
// Parse never panics on malformed input; it always returns an *Error.
type Error struct {
	Pos Position
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Column, e.Msg)
}

// Parse parses a single statement and returns its untyped syntax tree. The
// statement is a SELECT, or one of the JOURNAL / BALANCES shortcuts, which are
// desugared into the equivalent SELECT (so the result is always a [*Select]).
//
// JOURNAL, BALANCES, and the AT modifier are recognized contextually by the
// leading identifier rather than as reserved keywords, so existing queries
// that use those words as table or column names (e.g. FROM balances) keep
// working.
//
// The input must contain exactly one statement; an optional trailing ';' is
// allowed, but any other tokens after the statement are an error. Parse does
// no type checking, column or table resolution, or overload resolution. On any
// lexical or syntactic problem it returns an *Error carrying a source Position
// and never panics, even on truncated or malformed input.
func Parse(input string) (*Select, error) {
	p := &parser{sc: newScanner(input)}
	if err := p.advance(); err != nil {
		return nil, err
	}
	var (
		sel *Select
		err error
	)
	switch {
	case p.isContextualKeyword("journal"):
		sel, err = p.parseJournal()
	case p.isContextualKeyword("balances"):
		sel, err = p.parseBalances()
	default:
		sel, err = p.parseSelect()
	}
	if err != nil {
		return nil, err
	}
	if p.tok.kind == tokSemi {
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if p.tok.kind != tokEOF {
		return nil, p.errf(p.tok.pos, "unexpected %s after statement", p.tok.kind)
	}
	return sel, nil
}

type parser struct {
	sc  *scanner
	tok token
}

func (p *parser) advance() error {
	t, err := p.sc.scan()
	if err != nil {
		return err
	}
	p.tok = t
	return nil
}

func (p *parser) errf(pos Position, format string, args ...any) error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// expect consumes the current token when it has kind k, returning it; otherwise
// it returns a positioned error.
func (p *parser) expect(k tokenKind) (token, error) {
	if p.tok.kind != k {
		return token{}, p.errf(p.tok.pos, "expected %s, found %s", k, p.tok.kind)
	}
	t := p.tok
	if err := p.advance(); err != nil {
		return token{}, err
	}
	return t, nil
}

func (p *parser) parseSelect() (*Select, error) {
	kw, err := p.expect(tokSelect)
	if err != nil {
		return nil, err
	}
	sel := &Select{Pos: kw.pos}

	if p.tok.kind == tokDistinct {
		sel.Distinct = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	}

	if p.tok.kind == tokStar {
		sel.Star = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	} else {
		targets, err := p.parseTargetList()
		if err != nil {
			return nil, err
		}
		sel.Targets = targets
	}

	if p.tok.kind == tokFrom {
		from, err := p.parseFrom()
		if err != nil {
			return nil, err
		}
		sel.From = from
	}

	if p.tok.kind == tokWhere {
		if err := p.advance(); err != nil {
			return nil, err
		}
		where, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		sel.Where = where
	}

	if p.tok.kind == tokGroup {
		groupBy, err := p.parseGroupBy()
		if err != nil {
			return nil, err
		}
		sel.GroupBy = groupBy
	}

	if p.tok.kind == tokOrder {
		orderBy, err := p.parseOrderBy()
		if err != nil {
			return nil, err
		}
		sel.OrderBy = orderBy
	}

	if p.tok.kind == tokLimit {
		limit, err := p.parseLimit()
		if err != nil {
			return nil, err
		}
		sel.Limit = limit
	}

	return sel, nil
}

// isContextualKeyword reports whether the current token is a bare identifier
// equal (case-insensitively) to word. Used for the statement and AT keywords,
// which are not reserved.
func (p *parser) isContextualKeyword(word string) bool {
	return p.tok.kind == tokIdent && strings.EqualFold(p.tok.text, word)
}

// parseJournal parses the JOURNAL shortcut and desugars it into a SELECT over
// postings:
//
//	JOURNAL ["account-regex"] [AT func] [FROM ...]
//	  -> SELECT date, flag, MAXWIDTH(payee, 48), MAXWIDTH(narration, 80),
//	            account, func(position), func(balance)
//	     [WHERE account ~ "account-regex"] [FROM ...]
//
// When AT is absent the position and balance columns are projected directly.
func (p *parser) parseJournal() (*Select, error) {
	pos := p.tok.pos
	if err := p.advance(); err != nil {
		return nil, err
	}

	var account *string
	if p.tok.kind == tokString {
		s := p.tok.text
		account = &s
		if err := p.advance(); err != nil {
			return nil, err
		}
	}

	summary, err := p.parseSummaryFunc()
	if err != nil {
		return nil, err
	}

	from, err := p.parseOptFrom()
	if err != nil {
		return nil, err
	}

	sel := &Select{
		Targets: []Target{
			{Expr: &ColumnRef{Name: "date", Position: pos}, Pos: pos},
			{Expr: &ColumnRef{Name: "flag", Position: pos}, Pos: pos},
			{Expr: maxwidthCall("payee", 48, pos), Pos: pos},
			{Expr: maxwidthCall("narration", 80, pos), Pos: pos},
			{Expr: &ColumnRef{Name: "account", Position: pos}, Pos: pos},
			{Expr: summaryWrap(summary, "position", pos), Pos: pos},
			{Expr: summaryWrap(summary, "balance", pos), Pos: pos},
		},
		From: from,
		Pos:  pos,
	}
	if account != nil {
		sel.Where = &Binary{
			Op:       OpMatch,
			L:        &ColumnRef{Name: "account", Position: pos},
			R:        &StringLit{Value: *account, Position: pos},
			Position: pos,
		}
	}
	return sel, nil
}

// parseBalances parses the BALANCES shortcut and desugars it into a grouped
// SELECT over postings:
//
//	BALANCES [AT func] [FROM ...] [WHERE ...]
//	  -> SELECT account, SUM(func(position))
//	     [FROM ...] [WHERE ...]
//	     GROUP BY account, ACCOUNT_SORTKEY(account)
//	     ORDER BY ACCOUNT_SORTKEY(account)
//
// When AT is absent the position column is summed directly.
func (p *parser) parseBalances() (*Select, error) {
	pos := p.tok.pos
	if err := p.advance(); err != nil {
		return nil, err
	}

	summary, err := p.parseSummaryFunc()
	if err != nil {
		return nil, err
	}

	from, err := p.parseOptFrom()
	if err != nil {
		return nil, err
	}

	var where Expr
	if p.tok.kind == tokWhere {
		if err := p.advance(); err != nil {
			return nil, err
		}
		where, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}

	sumTarget := &FuncCall{
		Name:     "sum",
		Args:     []Expr{summaryWrap(summary, "position", pos)},
		Position: pos,
	}
	return &Select{
		Targets: []Target{
			{Expr: &ColumnRef{Name: "account", Position: pos}, Pos: pos},
			{Expr: sumTarget, Pos: pos},
		},
		From:    from,
		Where:   where,
		GroupBy: []Expr{&ColumnRef{Name: "account", Position: pos}, accountSortkey(pos)},
		OrderBy: []OrderItem{{Expr: accountSortkey(pos), Pos: pos}},
		Pos:     pos,
	}, nil
}

// parseSummaryFunc parses an optional "AT <func>" modifier, returning the
// function name, or "" when absent. AT is a contextual keyword.
func (p *parser) parseSummaryFunc() (string, error) {
	if !p.isContextualKeyword("at") {
		return "", nil
	}
	if err := p.advance(); err != nil {
		return "", err
	}
	name, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	return name.text, nil
}

// parseOptFrom parses a FROM clause when present, returning nil otherwise.
func (p *parser) parseOptFrom() (*FromClause, error) {
	if p.tok.kind != tokFrom {
		return nil, nil
	}
	return p.parseFrom()
}

// summaryWrap returns func(col) when summary is non-empty, else the bare column.
func summaryWrap(summary, col string, pos Position) Expr {
	c := &ColumnRef{Name: col, Position: pos}
	if summary == "" {
		return c
	}
	return &FuncCall{Name: summary, Args: []Expr{c}, Position: pos}
}

func maxwidthCall(col string, width int64, pos Position) Expr {
	return &FuncCall{
		Name:     "maxwidth",
		Args:     []Expr{&ColumnRef{Name: col, Position: pos}, &IntLit{Value: width, Position: pos}},
		Position: pos,
	}
}

func accountSortkey(pos Position) Expr {
	return &FuncCall{
		Name:     "account_sortkey",
		Args:     []Expr{&ColumnRef{Name: "account", Position: pos}},
		Position: pos,
	}
}

func (p *parser) parseTargetList() ([]Target, error) {
	var targets []Target
	for {
		pos := p.tok.pos
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		t := Target{Expr: expr, Pos: pos}
		if p.tok.kind == tokAs {
			if err := p.advance(); err != nil {
				return nil, err
			}
			alias, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			t.As = alias.text
		}
		targets = append(targets, t)
		if p.tok.kind != tokComma {
			return targets, nil
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
}

var scopingStarters = map[tokenKind]bool{
	tokOpen:  true,
	tokClose: true,
	tokClear: true,
}

var clauseEnders = map[tokenKind]bool{
	tokWhere: true,
	tokGroup: true,
	tokOrder: true,
	tokLimit: true,
	tokSemi:  true,
	tokEOF:   true,
}

func (p *parser) parseFrom() (*FromClause, error) {
	kw := p.tok
	if err := p.advance(); err != nil {
		return nil, err
	}

	if clauseEnders[p.tok.kind] {
		return nil, p.errf(p.tok.pos, "expected expression or scoping keyword after FROM, found %s", p.tok.kind)
	}

	from := &FromClause{Pos: kw.pos}

	if !scopingStarters[p.tok.kind] {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		from.Expr = expr
		if ref, ok := expr.(*ColumnRef); ok {
			from.IsBareName = true
			from.Name = ref.Name
		}
	}

	if scopingStarters[p.tok.kind] {
		scoping, err := p.parseScoping()
		if err != nil {
			return nil, err
		}
		from.Scoping = scoping
	}

	return from, nil
}

func (p *parser) parseScoping() (*Scoping, error) {
	sc := &Scoping{Pos: p.tok.pos}

	if p.tok.kind == tokOpen {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind != tokOn {
			return nil, p.errf(p.tok.pos, "expected ON after OPEN, found %s", p.tok.kind)
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		tm, err := p.parseDate("OPEN ON")
		if err != nil {
			return nil, err
		}
		sc.Open = &tm
	}

	if p.tok.kind == tokClose {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind != tokOn {
			return nil, p.errf(p.tok.pos, "expected ON after CLOSE, found %s", p.tok.kind)
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		tm, err := p.parseDate("CLOSE ON")
		if err != nil {
			return nil, err
		}
		sc.Close = &tm
	}

	if p.tok.kind == tokClear {
		clearPos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind == tokOn {
			return nil, p.errf(clearPos, "unexpected ON after CLEAR")
		}
		sc.Clear = true
	}

	switch p.tok.kind {
	case tokOpen:
		if sc.Open != nil {
			return nil, p.errf(p.tok.pos, "duplicate OPEN")
		}
		return nil, p.errf(p.tok.pos, "out-of-order OPEN (must precede CLOSE and CLEAR)")
	case tokClose:
		if sc.Close != nil {
			return nil, p.errf(p.tok.pos, "duplicate CLOSE")
		}
		return nil, p.errf(p.tok.pos, "out-of-order CLOSE (must precede CLEAR)")
	case tokClear:
		return nil, p.errf(p.tok.pos, "duplicate CLEAR")
	}

	return sc, nil
}

// parseDate consumes a date literal token. ctx is a short label included in
// error messages identifying the surrounding clause (e.g. "OPEN ON").
func (p *parser) parseDate(ctx string) (time.Time, error) {
	if p.tok.kind != tokDate {
		return time.Time{}, p.errf(p.tok.pos, "expected date after %s, found %s", ctx, p.tok.kind)
	}
	dateTok := p.tok
	if err := p.advance(); err != nil {
		return time.Time{}, err
	}
	tm, err := time.Parse("2006-01-02", dateTok.text)
	if err != nil {
		return time.Time{}, p.errf(dateTok.pos, "invalid date literal %q", dateTok.text)
	}
	return tm, nil
}

func (p *parser) parseGroupBy() ([]Expr, error) {
	if err := p.advance(); err != nil { // GROUP
		return nil, err
	}
	if _, err := p.expect(tokBy); err != nil {
		return nil, err
	}
	return p.parseExprList()
}

func (p *parser) parseOrderBy() ([]OrderItem, error) {
	if err := p.advance(); err != nil { // ORDER
		return nil, err
	}
	if _, err := p.expect(tokBy); err != nil {
		return nil, err
	}
	var items []OrderItem
	for {
		pos := p.tok.pos
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		item := OrderItem{Expr: expr, Pos: pos}
		switch p.tok.kind {
		case tokAsc:
			if err := p.advance(); err != nil {
				return nil, err
			}
		case tokDesc:
			item.Desc = true
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
		if p.tok.kind != tokComma {
			return items, nil
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
}

func (p *parser) parseLimit() (*int64, error) {
	if err := p.advance(); err != nil { // LIMIT
		return nil, err
	}
	t, err := p.expect(tokInt)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(t.text, 10, 64)
	if err != nil {
		return nil, p.errf(t.pos, "limit %q out of range", t.text)
	}
	return &n, nil
}

func (p *parser) parseExprList() ([]Expr, error) {
	var exprs []Expr
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
		if p.tok.kind != tokComma {
			return exprs, nil
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
}

// Expression grammar, lowest precedence first:
//
//	parseExpr        -> OR
//	parseOr          -> parseAnd   (OR parseAnd)*
//	parseAnd         -> parseNot   (AND parseNot)*
//	parseNot         -> NOT parseNot | parseComparison
//	parseComparison  -> parseAdd [ (= != < <= > >= ~) parseAdd
//	                              | [NOT] IN parseAdd
//	                              | [NOT] BETWEEN parseAdd AND parseAdd
//	                              | IS [NOT] NULL ]                   (non-associative)
//	parseAdd         -> parseMul   ((+ -) parseMul)*
//	parseMul         -> parseUnary ((* / %) parseUnary)*
//	parseUnary       -> (- +) parseUnary | parsePostfix
//	parsePostfix     -> parsePrimary (. ident | '[' expr ']')*
//	parsePrimary     -> literal | columnRef | funcCall | '(' expr ')' | '(' exprList ')'
func (p *parser) parseExpr() (Expr, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.tok.kind == tokOr {
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: OpOr, L: left, R: right, Position: pos}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.tok.kind == tokAnd {
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: OpAnd, L: left, R: right, Position: pos}
	}
	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.tok.kind == tokNot {
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: OpNot, X: x, Position: pos}, nil
	}
	return p.parseComparison()
}

var comparisonOps = map[tokenKind]BinaryOp{
	tokEq:    OpEq,
	tokNe:    OpNe,
	tokLt:    OpLt,
	tokLe:    OpLe,
	tokGt:    OpGt,
	tokGe:    OpGe,
	tokTilde: OpMatch,
}

// isComparisonContinuation reports whether k would start another comparison
// immediately after one already parsed, which the non-associative grammar
// rejects as a chained comparison.
func isComparisonContinuation(k tokenKind) bool {
	if _, ok := comparisonOps[k]; ok {
		return true
	}
	switch k {
	case tokIn, tokBetween, tokIs:
		return true
	}
	return false
}

// parseComparison parses a single non-associative comparison, optionally an
// IN / NOT IN membership test, a BETWEEN / NOT BETWEEN range test, or an
// IS [NOT] NULL test. Chained comparisons (a = b = c, a < b BETWEEN ...) are
// rejected, matching beanquery's grammar.
func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	switch {
	case isComparisonOp(p.tok.kind):
		op := comparisonOps[p.tok.kind]
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		if isComparisonContinuation(p.tok.kind) {
			return nil, p.errf(p.tok.pos, "chained comparison %s is not allowed", p.tok.kind)
		}
		return &Binary{Op: op, L: left, R: right, Position: pos}, nil
	case p.tok.kind == tokIn:
		return p.parseInTail(left, false)
	case p.tok.kind == tokBetween:
		return p.parseBetweenTail(left, false)
	case p.tok.kind == tokIs:
		return p.parseIsNullTail(left)
	case p.tok.kind == tokNot:
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		switch p.tok.kind {
		case tokIn:
			return p.parseInTail(left, true)
		case tokBetween:
			return p.parseBetweenTail(left, true)
		default:
			return nil, p.errf(pos, "expected IN or BETWEEN after NOT, found %s", p.tok.kind)
		}
	}
	return left, nil
}

func isComparisonOp(k tokenKind) bool {
	_, ok := comparisonOps[k]
	return ok
}

// parseInTail parses the right-hand side of an IN (or NOT IN) test; the
// current token is IN.
func (p *parser) parseInTail(left Expr, neg bool) (Expr, error) {
	pos := p.tok.pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	right, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	return &In{X: left, List: right, Neg: neg, Position: pos}, nil
}

// parseBetweenTail parses "BETWEEN lo AND hi" and desugars it into the
// equivalent comparison conjunction; the current token is BETWEEN. The AND
// separating the bounds is consumed here, so a following AND binds as the
// boolean operator. NOT BETWEEN desugars to the De Morgan dual.
func (p *parser) parseBetweenTail(left Expr, neg bool) (Expr, error) {
	pos := p.tok.pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	lo, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokAnd); err != nil {
		return nil, err
	}
	hi, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if isComparisonContinuation(p.tok.kind) {
		return nil, p.errf(p.tok.pos, "chained comparison %s is not allowed", p.tok.kind)
	}
	// left is aliased into both bounds; the AST is read-only after Parse, so
	// the shared node is never mutated downstream.
	if neg {
		return &Binary{
			Op:       OpOr,
			L:        &Binary{Op: OpLt, L: left, R: lo, Position: pos},
			R:        &Binary{Op: OpGt, L: left, R: hi, Position: pos},
			Position: pos,
		}, nil
	}
	return &Binary{
		Op:       OpAnd,
		L:        &Binary{Op: OpGe, L: left, R: lo, Position: pos},
		R:        &Binary{Op: OpLe, L: left, R: hi, Position: pos},
		Position: pos,
	}, nil
}

// parseIsNullTail parses "IS [NOT] NULL"; the current token is IS.
func (p *parser) parseIsNullTail(left Expr) (Expr, error) {
	pos := p.tok.pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	neg := p.tok.kind == tokNot
	if neg {
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(tokNull); err != nil {
		return nil, err
	}
	if isComparisonContinuation(p.tok.kind) {
		return nil, p.errf(p.tok.pos, "chained comparison %s is not allowed", p.tok.kind)
	}
	return &IsNull{X: left, Neg: neg, Position: pos}, nil
}

func (p *parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.tok.kind {
		case tokPlus:
			op = OpAdd
		case tokMinus:
			op = OpSub
		default:
			return left, nil
		}
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, L: left, R: right, Position: pos}
	}
}

func (p *parser) parseMul() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.tok.kind {
		case tokStar:
			op = OpMul
		case tokSlash:
			op = OpDiv
		case tokPercent:
			op = OpMod
		default:
			return left, nil
		}
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, L: left, R: right, Position: pos}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	var op UnaryOp
	switch p.tok.kind {
	case tokMinus:
		op = OpNeg
	case tokPlus:
		op = OpPos
	default:
		return p.parsePostfix()
	}
	pos := p.tok.pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	x, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	return &Unary{Op: op, X: x, Position: pos}, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.tok
	switch t.kind {
	case tokInt:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return p.makeIntLit(t)
	case tokDecimal:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return p.makeDecimalLit(t)
	case tokString:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &StringLit{Value: t.text, Position: t.pos}, nil
	case tokDate:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return p.makeDateLit(t)
	case tokTrue:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &BoolLit{Value: true, Position: t.pos}, nil
	case tokFalse:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &BoolLit{Value: false, Position: t.pos}, nil
	case tokNull:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &NullLit{Position: t.pos}, nil
	case tokIdent:
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind == tokLParen {
			return p.parseFuncCall(t)
		}
		return &ColumnRef{Name: t.text, Position: t.pos}, nil
	case tokOpen, tokClose, tokClear:
		// soft keywords: column references (e.g. the accounts table's
		// open/close columns) outside the FROM scoping clause, where
		// parseFrom recognizes them by their own production.
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &ColumnRef{Name: t.text, Position: t.pos}, nil
	case tokLParen:
		return p.parseParen(t)
	default:
		return nil, p.errf(t.pos, "expected expression, found %s", t.kind)
	}
}

// parsePostfix wraps a primary in zero or more postfix accessors: `.attr`
// attribute access and `[expr]` subscript, left-associative and binding
// tighter than any prefix or infix operator.
func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.tok.kind {
		case tokDot:
			pos := p.tok.pos
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.tok.kind != tokIdent {
				return nil, p.errf(p.tok.pos, "expected attribute name after '.', found %s", p.tok.kind)
			}
			attr := p.tok.text
			if err := p.advance(); err != nil {
				return nil, err
			}
			expr = &AttributeAccess{Expr: expr, Attr: attr, Position: pos}
		case tokLBracket:
			pos := p.tok.pos
			if err := p.advance(); err != nil {
				return nil, err
			}
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tokRBracket); err != nil {
				return nil, err
			}
			expr = &IndexAccess{Expr: expr, Index: idx, Position: pos}
		default:
			return expr, nil
		}
	}
}

func (p *parser) parseFuncCall(name token) (Expr, error) {
	if err := p.advance(); err != nil { // '('
		return nil, err
	}
	call := &FuncCall{Name: name.text, Position: name.pos}
	if p.tok.kind == tokRParen {
		if err := p.advance(); err != nil {
			return nil, err
		}
		return call, nil
	}
	args, err := p.parseExprList()
	if err != nil {
		return nil, err
	}
	call.Args = args
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return call, nil
}

// parseParen handles both a parenthesized expression and a parenthesized list
// (the IN right-hand side). A single element yields the bare expression; two or
// more elements yield a *ListLit.
func (p *parser) parseParen(open token) (Expr, error) {
	if err := p.advance(); err != nil { // '('
		return nil, err
	}
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.tok.kind != tokComma {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return first, nil
	}
	elems := []Expr{first}
	for p.tok.kind == tokComma {
		if err := p.advance(); err != nil {
			return nil, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elems = append(elems, e)
	}
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return &ListLit{Elems: elems, Position: open.pos}, nil
}

func (p *parser) makeIntLit(t token) (Expr, error) {
	n, err := strconv.ParseInt(t.text, 10, 64)
	if err != nil {
		return nil, p.errf(t.pos, "integer literal %q out of range", t.text)
	}
	return &IntLit{Value: n, Position: t.pos}, nil
}

func (p *parser) makeDecimalLit(t token) (Expr, error) {
	var d apd.Decimal
	if _, _, err := d.SetString(t.text); err != nil {
		return nil, p.errf(t.pos, "invalid decimal literal %q", t.text)
	}
	return &DecimalLit{Value: d, Position: t.pos}, nil
}

func (p *parser) makeDateLit(t token) (Expr, error) {
	tm, err := time.Parse("2006-01-02", t.text)
	if err != nil {
		return nil, p.errf(t.pos, "invalid date literal %q", t.text)
	}
	return &DateLit{Value: tm, Position: t.pos}, nil
}
