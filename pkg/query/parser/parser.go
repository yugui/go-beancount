package parser

import (
	"fmt"
	"strconv"
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

// Parse parses a single SELECT statement and returns its untyped syntax tree.
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
	sel, err := p.parseSelect()
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

func (p *parser) parseFrom() (*FromClause, error) {
	kw := p.tok
	if err := p.advance(); err != nil {
		return nil, err
	}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	from := &FromClause{Expr: expr, Pos: kw.pos}
	if ref, ok := expr.(*ColumnRef); ok {
		from.IsBareName = true
		from.Name = ref.Name
	}
	return from, nil
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
//	parseComparison  -> parseAdd [ (= != < <= > >= ~) parseAdd | IN parseAdd ]   (non-associative)
//	parseAdd         -> parseMul   ((+ -) parseMul)*
//	parseMul         -> parseUnary ((* / %) parseUnary)*
//	parseUnary       -> (- +) parseUnary | parsePrimary
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

// parseComparison parses a single non-associative comparison. Chained
// comparisons (a = b = c) are rejected, matching beanquery's grammar.
func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if op, ok := comparisonOps[p.tok.kind]; ok {
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		if _, chained := comparisonOps[p.tok.kind]; chained || p.tok.kind == tokIn {
			return nil, p.errf(p.tok.pos, "chained comparison %s is not allowed", p.tok.kind)
		}
		return &Binary{Op: op, L: left, R: right, Position: pos}, nil
	}
	if p.tok.kind == tokIn {
		pos := p.tok.pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return &In{X: left, List: right, Position: pos}, nil
	}
	return left, nil
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
		return p.parsePrimary()
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
	case tokLParen:
		return p.parseParen(t)
	default:
		return nil, p.errf(t.pos, "expected expression, found %s", t.kind)
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
