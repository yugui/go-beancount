package exec

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/parser"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// tableCatalog maps a (case-insensitive) table name to its constructor over
// a ledger. A bare-identifier FROM whose name is a key here is a table
// reference; any other bare identifier is a single-column filter expression
// over the default postings table (Decision 7).
var tableCatalog = map[string]func(*ast.Ledger) *table.Table{
	"postings": table.Postings,
	"entries":  table.Entries,
}

// compileError reports a compile-time failure, carrying the source position
// when one is available (Line == 0 means none).
type compileError struct {
	pos parser.Position
	msg string
}

func (e *compileError) Error() string {
	if e.pos.Line == 0 {
		return "query/exec: " + e.msg
	}
	return fmt.Sprintf("query/exec: %d:%d: %s", e.pos.Line, e.pos.Column, e.msg)
}

func errf(pos parser.Position, format string, args ...any) error {
	return &compileError{pos: pos, msg: fmt.Sprintf(format, args...)}
}

// Compile type-checks and resolves sel against a virtual table chosen from
// ledger, producing an immutable [Compiled] plan. The FROM clause is
// classified per Decision 7: a bare identifier naming a catalog table
// selects that table and contributes no filter; otherwise the table is the
// default postings and the FROM expression is merged into the row predicate
// by AND with WHERE over the same columns (FROM ≡ WHERE). Compile resolves
// every column, operator, and function overload, classifies targets as
// scalar or aggregate, and returns a positioned error (never a panic) on any
// unknown column or table, type mismatch, missing/ambiguous overload,
// misplaced aggregate, or non-boolean predicate. ledger is held by reference
// and never mutated.
func Compile(sel *parser.Select, ledger *ast.Ledger) (*Compiled, error) {
	tbl, fromFilter, err := selectTable(sel, ledger)
	if err != nil {
		return nil, err
	}

	c := &compiler{tbl: tbl}

	predicate, err := c.compilePredicate(fromFilter, sel.Where)
	if err != nil {
		return nil, err
	}

	plan := &Compiled{
		tbl:       tbl,
		predicate: predicate,
		distinct:  sel.Distinct,
		limit:     sel.Limit,
	}

	if err := c.compileGroupBy(sel.GroupBy, plan); err != nil {
		return nil, err
	}
	if err := c.compileTargets(sel, plan); err != nil {
		return nil, err
	}
	if err := c.compileOrderBy(sel.OrderBy, plan); err != nil {
		return nil, err
	}
	// An aggregate may first appear in ORDER BY; promote the plan to
	// aggregate mode so Run takes the grouping path and the mixing check
	// below applies (otherwise an aggregate-ref would read a nil result set).
	plan.aggregate = plan.aggregate || len(c.slots) > 0
	if err := checkAggregateMixing(sel.GroupBy, plan); err != nil {
		return nil, err
	}

	plan.slots = c.slots
	return plan, nil
}

// selectTable applies Decision 7. It returns the selected table and the raw
// FROM filter expression (nil when FROM is absent or is a table reference).
func selectTable(sel *parser.Select, ledger *ast.Ledger) (*table.Table, parser.Expr, error) {
	from := sel.From
	if from != nil && from.IsBareName {
		if ctor, ok := tableCatalog[strings.ToLower(from.Name)]; ok {
			return ctor(ledger), nil, nil
		}
	}
	tbl := tableCatalog["postings"](ledger)
	if from == nil {
		return tbl, nil, nil
	}
	if from.IsBareName {
		if _, ok := tbl.Column(from.Name); !ok {
			return nil, nil, errf(from.Pos, "unknown table or column %q", from.Name)
		}
	}
	return tbl, from.Expr, nil
}

// compiler carries the selected table and the ordered aggregate-slot list
// accumulated while compiling targets and ORDER BY items.
type compiler struct {
	tbl   *table.Table
	slots []aggSlot
}

// compilePredicate compiles the AND of the FROM filter and WHERE (omitting
// absent operands) against the selected table's columns, and verifies the
// result is boolean (or an untyped NULL). It returns nil when neither
// operand is present.
func (c *compiler) compilePredicate(fromFilter, where parser.Expr) (cexpr, error) {
	parts := make([]parser.Expr, 0, 2)
	if fromFilter != nil {
		parts = append(parts, fromFilter)
	}
	if where != nil {
		parts = append(parts, where)
	}
	if len(parts) == 0 {
		return nil, nil
	}

	merged := parts[0]
	for _, p := range parts[1:] {
		merged = &parser.Binary{Op: parser.OpAnd, L: merged, R: p, Position: p.Pos()}
	}
	pred, err := c.compileScalar(merged)
	if err != nil {
		return nil, err
	}
	if t := pred.Type(); t != types.Bool && t != types.Invalid {
		return nil, errf(merged.Pos(), "predicate must be boolean, got %s", t)
	}
	return pred, nil
}

func (c *compiler) compileGroupBy(exprs []parser.Expr, plan *Compiled) error {
	for _, e := range exprs {
		ce, err := c.compileScalar(e)
		if err != nil {
			return err
		}
		plan.groupBy = append(plan.groupBy, ce)
	}
	plan.aggregate = len(plan.groupBy) > 0
	return nil
}

func (c *compiler) compileTargets(sel *parser.Select, plan *Compiled) error {
	if sel.Star {
		for _, col := range c.tbl.Columns {
			plan.cols = append(plan.cols, OutColumn{Name: col.Name, Type: col.Type})
			plan.targets = append(plan.targets, &columnExpr{col: col})
		}
		return nil
	}

	for i, tgt := range sel.Targets {
		ce, err := c.compileAggregable(tgt.Expr)
		if err != nil {
			return err
		}
		plan.targets = append(plan.targets, ce)
		plan.cols = append(plan.cols, OutColumn{
			Name: outName(tgt, i),
			Type: ce.Type(),
		})
	}
	plan.aggregate = plan.aggregate || len(c.slots) > 0
	return nil
}

func (c *compiler) compileOrderBy(items []parser.OrderItem, plan *Compiled) error {
	for _, item := range items {
		ce, err := c.compileOrderExpr(item.Expr, plan)
		if err != nil {
			return err
		}
		plan.orderBy = append(plan.orderBy, orderKey{expr: ce, desc: item.Desc})
	}
	return nil
}

// compileOrderExpr compiles one ORDER BY expression in the target scope. A
// bare identifier that matches a target alias or output column name reuses
// that target's compiled expression, so "ORDER BY total DESC" can sort by an
// aggregate aliased "total".
func (c *compiler) compileOrderExpr(e parser.Expr, plan *Compiled) (cexpr, error) {
	if ref, ok := e.(*parser.ColumnRef); ok {
		for i, oc := range plan.cols {
			if strings.EqualFold(oc.Name, ref.Name) {
				return plan.targets[i], nil
			}
		}
	}
	return c.compileAggregable(e)
}

// checkAggregateMixing enforces, in aggregate mode, that every column read by
// a target or ORDER BY expression is either a grouped column or sits inside an
// aggregate call. It walks the COMPILED targets, where aggregate calls have
// already been replaced by aggRef placeholders (their column reads moved into
// the slots), so a columnExpr surviving in a target is genuinely an
// ungrouped, unaggregated reference. Grouped columns are matched by the bare
// column name referenced in GROUP BY; a non-trivial grouped expression (e.g.
// GROUP BY year(date)) does not cover the bare column it derives from. This is
// the lean rule, documented in doc.go.
func checkAggregateMixing(groupBy []parser.Expr, plan *Compiled) error {
	if !plan.aggregate {
		return nil
	}
	grouped := map[string]bool{}
	for _, e := range groupBy {
		if ref, ok := e.(*parser.ColumnRef); ok {
			grouped[strings.ToLower(ref.Name)] = true
		}
	}
	for _, tgt := range plan.targets {
		if name := ungroupedColumn(tgt, grouped); name != "" {
			return errf(parser.Position{}, "column %q must appear in GROUP BY or be aggregated", name)
		}
	}
	for _, ok := range plan.orderBy {
		if name := ungroupedColumn(ok.expr, grouped); name != "" {
			return errf(parser.Position{}, "column %q must appear in GROUP BY or be aggregated", name)
		}
	}
	return nil
}

// ungroupedColumn returns the name of the first column read by ce that is not
// a grouped column, or "" if every column reference is grouped. Aggregate
// references carry no column reads (their arguments live in slots), so they
// never contribute.
func ungroupedColumn(ce cexpr, grouped map[string]bool) string {
	switch x := ce.(type) {
	case *columnExpr:
		if !grouped[strings.ToLower(x.col.Name)] {
			return x.col.Name
		}
		return ""
	case *scalarExpr:
		return firstUngrouped(x.args, grouped)
	case *arithExpr:
		return firstUngrouped([]cexpr{x.l, x.r}, grouped)
	case *cmpExpr:
		return firstUngrouped([]cexpr{x.l, x.r}, grouped)
	case *matchExpr:
		return firstUngrouped([]cexpr{x.l, x.r}, grouped)
	case *boolExpr:
		if x.r == nil {
			return ungroupedColumn(x.l, grouped)
		}
		return firstUngrouped([]cexpr{x.l, x.r}, grouped)
	case *negExpr:
		return ungroupedColumn(x.x, grouped)
	case *inListExpr:
		return firstUngrouped(append([]cexpr{x.x}, x.elems...), grouped)
	case *inSetExpr:
		return firstUngrouped([]cexpr{x.x, x.set}, grouped)
	default:
		return ""
	}
}

func firstUngrouped(exprs []cexpr, grouped map[string]bool) string {
	for _, e := range exprs {
		if name := ungroupedColumn(e, grouped); name != "" {
			return name
		}
	}
	return ""
}

// compileScalar compiles e forbidding aggregate function calls; an aggregate
// in WHERE/FROM/GROUP BY is a compile error.
func (c *compiler) compileScalar(e parser.Expr) (cexpr, error) {
	return c.compileExpr(e, false)
}

// compileAggregable compiles e in a scope where aggregate calls are allowed
// (targets and ORDER BY in aggregate mode). A nested aggregate is rejected.
func (c *compiler) compileAggregable(e parser.Expr) (cexpr, error) {
	return c.compileExpr(e, true)
}

func (c *compiler) compileExpr(e parser.Expr, aggOK bool) (cexpr, error) {
	switch x := e.(type) {
	case *parser.ColumnRef:
		col, ok := c.tbl.Column(x.Name)
		if !ok {
			return nil, errf(x.Position, "unknown column %q", x.Name)
		}
		return &columnExpr{col: col}, nil
	case *parser.IntLit:
		return &literalExpr{typ: types.Int, val: types.NewInt(x.Value)}, nil
	case *parser.DecimalLit:
		return &literalExpr{typ: types.Decimal, val: types.NewDecimal(x.Value)}, nil
	case *parser.StringLit:
		return &literalExpr{typ: types.String, val: types.NewString(x.Value)}, nil
	case *parser.DateLit:
		return &literalExpr{typ: types.Date, val: types.NewDate(x.Value)}, nil
	case *parser.BoolLit:
		return &literalExpr{typ: types.Bool, val: types.NewBool(x.Value)}, nil
	case *parser.NullLit:
		return &literalExpr{typ: types.Invalid, val: types.Null(types.Invalid)}, nil
	case *parser.Unary:
		return c.compileUnary(x, aggOK)
	case *parser.Binary:
		return c.compileBinary(x, aggOK)
	case *parser.In:
		return c.compileIn(x, aggOK)
	case *parser.FuncCall:
		return c.compileCall(x, aggOK)
	case *parser.ListLit:
		return nil, errf(x.Position, "list literal is only valid as the right operand of IN")
	default:
		return nil, errf(e.Pos(), "unsupported expression")
	}
}

func (c *compiler) compileUnary(x *parser.Unary, aggOK bool) (cexpr, error) {
	operand, err := c.compileExpr(x.X, aggOK)
	if err != nil {
		return nil, err
	}
	switch x.Op {
	case parser.OpPos:
		if !isNumeric(operand.Type()) {
			return nil, errf(x.Position, "unary + requires a numeric operand, got %s", operand.Type())
		}
		return operand, nil
	case parser.OpNeg:
		if !isNumeric(operand.Type()) {
			return nil, errf(x.Position, "unary - requires a numeric operand, got %s", operand.Type())
		}
		out := operand.Type()
		if out == types.Invalid {
			out = types.Int
		}
		return &negExpr{x: operand, out: out}, nil
	case parser.OpNot:
		if t := operand.Type(); t != types.Bool && t != types.Invalid {
			return nil, errf(x.Position, "NOT requires a boolean operand, got %s", t)
		}
		return &boolExpr{not: true, l: operand}, nil
	default:
		return nil, errf(x.Position, "unsupported unary operator %s", x.Op)
	}
}

func (c *compiler) compileBinary(x *parser.Binary, aggOK bool) (cexpr, error) {
	switch x.Op {
	case parser.OpAnd, parser.OpOr:
		return c.compileBool(x, aggOK)
	case parser.OpAdd, parser.OpSub, parser.OpMul, parser.OpDiv, parser.OpMod:
		return c.compileArith(x, aggOK)
	case parser.OpEq, parser.OpNe, parser.OpLt, parser.OpLe, parser.OpGt, parser.OpGe:
		return c.compileCompare(x, aggOK)
	case parser.OpMatch:
		return c.compileMatch(x, aggOK)
	default:
		return nil, errf(x.Position, "unsupported binary operator %s", x.Op)
	}
}

func (c *compiler) compileBool(x *parser.Binary, aggOK bool) (cexpr, error) {
	l, err := c.compileExpr(x.L, aggOK)
	if err != nil {
		return nil, err
	}
	r, err := c.compileExpr(x.R, aggOK)
	if err != nil {
		return nil, err
	}
	if t := l.Type(); t != types.Bool && t != types.Invalid {
		return nil, errf(x.L.Pos(), "%s requires boolean operands, got %s", x.Op, t)
	}
	if t := r.Type(); t != types.Bool && t != types.Invalid {
		return nil, errf(x.R.Pos(), "%s requires boolean operands, got %s", x.Op, t)
	}
	return &boolExpr{op: x.Op, l: l, r: r}, nil
}

func (c *compiler) compileArith(x *parser.Binary, aggOK bool) (cexpr, error) {
	l, err := c.compileExpr(x.L, aggOK)
	if err != nil {
		return nil, err
	}
	r, err := c.compileExpr(x.R, aggOK)
	if err != nil {
		return nil, err
	}
	if !isNumeric(l.Type()) {
		return nil, errf(x.L.Pos(), "%s requires numeric operands, got %s", x.Op, l.Type())
	}
	if !isNumeric(r.Type()) {
		return nil, errf(x.R.Pos(), "%s requires numeric operands, got %s", x.Op, r.Type())
	}
	out := numericResult(l.Type(), r.Type())
	if x.Op == parser.OpDiv {
		out = types.Decimal
	}
	return &arithExpr{op: x.Op, l: l, r: r, out: out}, nil
}

func (c *compiler) compileCompare(x *parser.Binary, aggOK bool) (cexpr, error) {
	l, err := c.compileExpr(x.L, aggOK)
	if err != nil {
		return nil, err
	}
	r, err := c.compileExpr(x.R, aggOK)
	if err != nil {
		return nil, err
	}
	if err := checkComparable(x.Position, l.Type(), r.Type()); err != nil {
		return nil, err
	}
	return &cmpExpr{op: x.Op, l: l, r: r}, nil
}

// checkComparable accepts equal types, a numeric pair (which widens), and
// any pairing involving an untyped NULL (Invalid). Other mixed types are a
// compile error.
func checkComparable(pos parser.Position, l, r types.Type) error {
	switch {
	case l == types.Invalid || r == types.Invalid:
		return nil
	case l == r:
		return nil
	case isNumeric(l) && isNumeric(r):
		return nil
	default:
		return errf(pos, "cannot compare %s with %s", l, r)
	}
}

func (c *compiler) compileMatch(x *parser.Binary, aggOK bool) (cexpr, error) {
	l, err := c.compileExpr(x.L, aggOK)
	if err != nil {
		return nil, err
	}
	r, err := c.compileExpr(x.R, aggOK)
	if err != nil {
		return nil, err
	}
	if t := l.Type(); t != types.String && t != types.Invalid {
		return nil, errf(x.L.Pos(), "~ requires a string left operand, got %s", t)
	}
	if t := r.Type(); t != types.String && t != types.Invalid {
		return nil, errf(x.R.Pos(), "~ requires a string pattern, got %s", t)
	}
	m := &matchExpr{l: l, r: r}
	if lit, ok := r.(*literalExpr); ok && !lit.val.IsNull() {
		pat, _ := types.AsString(lit.val)
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, errf(x.R.Pos(), "invalid regular expression %q: %v", pat, err)
		}
		m.re = re
	}
	return m, nil
}

func (c *compiler) compileIn(x *parser.In, aggOK bool) (cexpr, error) {
	left, err := c.compileExpr(x.X, aggOK)
	if err != nil {
		return nil, err
	}
	if list, ok := x.List.(*parser.ListLit); ok {
		elems := make([]cexpr, len(list.Elems))
		for i, el := range list.Elems {
			ce, err := c.compileExpr(el, aggOK)
			if err != nil {
				return nil, err
			}
			elems[i] = ce
		}
		return &inListExpr{x: left, elems: elems}, nil
	}
	right, err := c.compileExpr(x.List, aggOK)
	if err != nil {
		return nil, err
	}
	if right.Type() != types.SetType {
		return nil, errf(x.Position, "IN requires a list literal or a set operand, got %s", right.Type())
	}
	if t := left.Type(); t != types.String && t != types.Invalid {
		return nil, errf(x.X.Pos(), "IN over a set requires a string left operand, got %s", t)
	}
	return &inSetExpr{x: left, set: right}, nil
}

// compileCall handles scalar and aggregate function calls and the meta()
// sugar.
func (c *compiler) compileCall(x *parser.FuncCall, aggOK bool) (cexpr, error) {
	if strings.EqualFold(x.Name, "meta") {
		return c.compileMeta(x, aggOK)
	}

	argExprs := make([]cexpr, len(x.Args))
	argTypes := make([]types.Type, len(x.Args))
	for i, a := range x.Args {
		ce, err := c.compileExpr(a, aggOK)
		if err != nil {
			return nil, err
		}
		argExprs[i] = ce
		argTypes[i] = ce.Type()
	}

	fn, err := env.Resolve(x.Name, argTypes)
	if err != nil {
		return nil, errf(x.Position, "%v", err)
	}

	if fn.Flavor == api.AggregatorFlavor {
		if !aggOK {
			return nil, errf(x.Position, "aggregate %q not allowed here", x.Name)
		}
		if c.containsAggregate(argExprs) {
			return nil, errf(x.Position, "nested aggregate %q not allowed", x.Name)
		}
		slot := len(c.slots)
		c.slots = append(c.slots, aggSlot{newAcc: fn.Aggregator, args: argExprs})
		return &aggRefExpr{slot: slot, typ: fn.Out}, nil
	}

	return &scalarExpr{fn: fn.Scalar, args: argExprs, out: fn.Out}, nil
}

// compileMeta rewrites meta('key'[, default]) into getitem(meta, key[,
// default]) by prepending the table's meta column as the first argument and
// resolving getitem via env (Decision A2). The rewrite is documented in
// doc.go.
func (c *compiler) compileMeta(x *parser.FuncCall, aggOK bool) (cexpr, error) {
	col, ok := c.tbl.Column("meta")
	if !ok {
		return nil, errf(x.Position, "table %q has no meta column", c.tbl.Name)
	}
	args := make([]cexpr, 0, len(x.Args)+1)
	argTypes := make([]types.Type, 0, len(x.Args)+1)
	args = append(args, &columnExpr{col: col})
	argTypes = append(argTypes, col.Type)
	for _, a := range x.Args {
		ce, err := c.compileExpr(a, aggOK)
		if err != nil {
			return nil, err
		}
		args = append(args, ce)
		argTypes = append(argTypes, ce.Type())
	}
	fn, err := env.Resolve("getitem", argTypes)
	if err != nil {
		return nil, errf(x.Position, "%v", err)
	}
	if fn.Flavor != api.ScalarFlavor {
		return nil, errf(x.Position, "getitem must be a scalar function")
	}
	return &scalarExpr{fn: fn.Scalar, args: args, out: fn.Out}, nil
}

func (c *compiler) containsAggregate(args []cexpr) bool {
	for _, a := range args {
		if hasAggRef(a) {
			return true
		}
	}
	return false
}

// hasAggRef reports whether ce or any of its compiled subexpressions is an
// aggregate-ref node, used to reject nested aggregates.
func hasAggRef(ce cexpr) bool {
	switch x := ce.(type) {
	case *aggRefExpr:
		return true
	case *scalarExpr:
		return anyHasAggRef(x.args)
	case *arithExpr:
		return hasAggRef(x.l) || hasAggRef(x.r)
	case *cmpExpr:
		return hasAggRef(x.l) || hasAggRef(x.r)
	case *matchExpr:
		return hasAggRef(x.l) || hasAggRef(x.r)
	case *boolExpr:
		if hasAggRef(x.l) {
			return true
		}
		return x.r != nil && hasAggRef(x.r)
	case *negExpr:
		return hasAggRef(x.x)
	case *inListExpr:
		return hasAggRef(x.x) || anyHasAggRef(x.elems)
	case *inSetExpr:
		return hasAggRef(x.x) || hasAggRef(x.set)
	default:
		return false
	}
}

func anyHasAggRef(exprs []cexpr) bool {
	for _, e := range exprs {
		if hasAggRef(e) {
			return true
		}
	}
	return false
}

// outName derives an output column name for a non-* target: the AS alias if
// present, else the column name for a bare column reference, else the
// function name for a top-level call, else a positional "columnN".
func outName(tgt parser.Target, idx int) string {
	if tgt.As != "" {
		return tgt.As
	}
	switch x := tgt.Expr.(type) {
	case *parser.ColumnRef:
		return x.Name
	case *parser.FuncCall:
		return x.Name
	default:
		return fmt.Sprintf("column%d", idx)
	}
}
