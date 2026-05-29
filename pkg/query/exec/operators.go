package exec

import (
	"fmt"
	"regexp"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/query/parser"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// divPrecision matches pkg/inventory / pkg/query/table's quo context so
// decimal division agrees with values the rest of the engine derives.
var divPrecision = apd.BaseContext.WithPrecision(34)

// isNumeric reports whether t participates in arithmetic and numeric
// comparison. An untyped NULL ([types.Invalid]) is accepted everywhere
// and resolves at eval time.
func isNumeric(t types.Type) bool {
	return t == types.Int || t == types.Decimal || t == types.Invalid
}

// numericResult returns the static result type of an int/decimal binary
// op given the two operand types: Decimal if either side is Decimal, else
// Int. div always returns Decimal (handled by its caller). An Invalid
// (untyped NULL) operand contributes nothing and defers to the other.
func numericResult(l, r types.Type) types.Type {
	if l == types.Decimal || r == types.Decimal {
		return types.Decimal
	}
	if l == types.Invalid && r == types.Invalid {
		return types.Invalid
	}
	return types.Int
}

// arithExpr implements + - * / % over numeric operands with Int→Decimal
// widening. A NULL operand propagates to a typed NULL result.
type arithExpr struct {
	op   parser.BinaryOp
	l, r cexpr
	out  types.Type
}

func (e *arithExpr) Type() types.Type { return e.out }

func (e *arithExpr) eval(ctx *evalCtx) (types.Value, error) {
	lv, err := e.l.eval(ctx)
	if err != nil {
		return nil, err
	}
	rv, err := e.r.eval(ctx)
	if err != nil {
		return nil, err
	}
	if lv.IsNull() || rv.IsNull() {
		return types.Null(e.out), nil
	}
	if e.out == types.Int {
		return e.evalInt(lv, rv)
	}
	return e.evalDecimal(lv, rv)
}

func (e *arithExpr) evalInt(lv, rv types.Value) (types.Value, error) {
	a, _ := types.AsInt(lv)
	b, _ := types.AsInt(rv)
	switch e.op {
	case parser.OpAdd:
		return types.NewInt(a + b), nil
	case parser.OpSub:
		return types.NewInt(a - b), nil
	case parser.OpMul:
		return types.NewInt(a * b), nil
	case parser.OpMod:
		if b == 0 {
			return nil, fmt.Errorf("exec: integer modulo by zero")
		}
		return types.NewInt(a % b), nil
	default:
		return nil, fmt.Errorf("exec: unreachable int op %s", e.op)
	}
}

func (e *arithExpr) evalDecimal(lv, rv types.Value) (types.Value, error) {
	a := toDecimal(lv)
	b := toDecimal(rv)
	out := new(apd.Decimal)
	switch e.op {
	case parser.OpAdd:
		_, err := apd.BaseContext.Add(out, a, b)
		return decimalOrErr(out, err)
	case parser.OpSub:
		_, err := apd.BaseContext.Sub(out, a, b)
		return decimalOrErr(out, err)
	case parser.OpMul:
		_, err := apd.BaseContext.Mul(out, a, b)
		return decimalOrErr(out, err)
	case parser.OpDiv:
		if b.Sign() == 0 {
			return nil, fmt.Errorf("exec: division by zero")
		}
		_, err := divPrecision.Quo(out, a, b)
		return decimalOrErr(out, err)
	case parser.OpMod:
		if b.Sign() == 0 {
			return nil, fmt.Errorf("exec: modulo by zero")
		}
		_, err := apd.BaseContext.Rem(out, a, b)
		return decimalOrErr(out, err)
	default:
		return nil, fmt.Errorf("exec: unreachable decimal op %s", e.op)
	}
}

func decimalOrErr(d *apd.Decimal, err error) (types.Value, error) {
	if err != nil {
		return nil, fmt.Errorf("exec: arithmetic error: %w", err)
	}
	return types.NewDecimal(*d), nil
}

// toDecimal returns an apd copy of an Int or Decimal value.
func toDecimal(v types.Value) *apd.Decimal {
	if n, ok := types.AsInt(v); ok {
		return apd.New(n, 0)
	}
	d, _ := types.AsDecimal(v)
	return &d
}

// cmpExpr implements = != < <= > >= via the total order in types.Compare,
// returning Bool with SQL 3-valued NULL propagation.
type cmpExpr struct {
	op   parser.BinaryOp
	l, r cexpr
}

func (*cmpExpr) Type() types.Type { return types.Bool }

func (e *cmpExpr) eval(ctx *evalCtx) (types.Value, error) {
	lv, err := e.l.eval(ctx)
	if err != nil {
		return nil, err
	}
	rv, err := e.r.eval(ctx)
	if err != nil {
		return nil, err
	}
	if lv.IsNull() || rv.IsNull() {
		return types.Null(types.Bool), nil
	}
	c := compareOperands(lv, rv)
	switch e.op {
	case parser.OpEq:
		return types.NewBool(c == 0), nil
	case parser.OpNe:
		return types.NewBool(c != 0), nil
	case parser.OpLt:
		return types.NewBool(c < 0), nil
	case parser.OpLe:
		return types.NewBool(c <= 0), nil
	case parser.OpGt:
		return types.NewBool(c > 0), nil
	case parser.OpGe:
		return types.NewBool(c >= 0), nil
	default:
		return nil, fmt.Errorf("exec: unreachable comparison op %s", e.op)
	}
}

// compareOperands compares two non-null values, widening a mixed
// Int/Decimal pair to Decimal so 1 and 1.0 compare equal.
func compareOperands(a, b types.Value) int {
	if a.Type() != b.Type() && isNumericValue(a) && isNumericValue(b) {
		return toDecimal(a).Cmp(toDecimal(b))
	}
	return a.Compare(b)
}

func isNumericValue(v types.Value) bool {
	return v.Type() == types.Int || v.Type() == types.Decimal
}

// matchExpr implements the regex operator `~`. A literal pattern is
// compiled once at build time; otherwise it is compiled per eval and a
// bad pattern is returned as a runtime error. A NULL operand yields NULL.
type matchExpr struct {
	l, r cexpr
	re   *regexp.Regexp
}

func (*matchExpr) Type() types.Type { return types.Bool }

func (e *matchExpr) eval(ctx *evalCtx) (types.Value, error) {
	lv, err := e.l.eval(ctx)
	if err != nil {
		return nil, err
	}
	rv, err := e.r.eval(ctx)
	if err != nil {
		return nil, err
	}
	if lv.IsNull() || rv.IsNull() {
		return types.Null(types.Bool), nil
	}
	s, _ := types.AsString(lv)
	re := e.re
	if re == nil {
		pat, _ := types.AsString(rv)
		re, err = regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("exec: invalid regular expression %q: %w", pat, err)
		}
	}
	return types.NewBool(re.MatchString(s)), nil
}

// boolExpr implements AND / OR / NOT with SQL 3-valued logic over Bool and
// NULL operands. For NOT, r is nil.
type boolExpr struct {
	op   parser.BinaryOp
	not  bool
	l, r cexpr
}

func (*boolExpr) Type() types.Type { return types.Bool }

func (e *boolExpr) eval(ctx *evalCtx) (types.Value, error) {
	lv, err := e.l.eval(ctx)
	if err != nil {
		return nil, err
	}
	lb, lok := types.AsBool(lv)
	if e.not {
		if !lok {
			return types.Null(types.Bool), nil
		}
		return types.NewBool(!lb), nil
	}
	rv, err := e.r.eval(ctx)
	if err != nil {
		return nil, err
	}
	rb, rok := types.AsBool(rv)
	if e.op == parser.OpAnd {
		return eval3And(lb, lok, rb, rok), nil
	}
	return eval3Or(lb, lok, rb, rok), nil
}

func eval3And(lb bool, lok bool, rb bool, rok bool) types.Value {
	if (lok && !lb) || (rok && !rb) {
		return types.NewBool(false)
	}
	if lok && rok {
		return types.NewBool(true)
	}
	return types.Null(types.Bool)
}

func eval3Or(lb bool, lok bool, rb bool, rok bool) types.Value {
	if (lok && lb) || (rok && rb) {
		return types.NewBool(true)
	}
	if lok && rok {
		return types.NewBool(false)
	}
	return types.Null(types.Bool)
}

// inListExpr tests x against an explicit list of compiled element exprs by
// Compare-equality, with SQL NULL semantics: TRUE on any equal element;
// otherwise NULL if x or any element is NULL; else FALSE.
type inListExpr struct {
	x     cexpr
	elems []cexpr
}

func (*inListExpr) Type() types.Type { return types.Bool }

func (e *inListExpr) eval(ctx *evalCtx) (types.Value, error) {
	xv, err := e.x.eval(ctx)
	if err != nil {
		return nil, err
	}
	sawNull := xv.IsNull()
	for _, el := range e.elems {
		ev, err := el.eval(ctx)
		if err != nil {
			return nil, err
		}
		if ev.IsNull() || xv.IsNull() {
			sawNull = true
			continue
		}
		if compareOperands(xv, ev) == 0 {
			return types.NewBool(true), nil
		}
	}
	if sawNull {
		return types.Null(types.Bool), nil
	}
	return types.NewBool(false), nil
}

// inSetExpr tests a String x for membership in a Set-valued operand. A
// NULL x yields NULL; a NULL set yields NULL.
type inSetExpr struct {
	x   cexpr
	set cexpr
}

func (*inSetExpr) Type() types.Type { return types.Bool }

func (e *inSetExpr) eval(ctx *evalCtx) (types.Value, error) {
	xv, err := e.x.eval(ctx)
	if err != nil {
		return nil, err
	}
	sv, err := e.set.eval(ctx)
	if err != nil {
		return nil, err
	}
	if xv.IsNull() || sv.IsNull() {
		return types.Null(types.Bool), nil
	}
	s, _ := types.AsString(xv)
	set, _ := types.AsSet(sv)
	return types.NewBool(set.Contains(s)), nil
}

// negExpr implements unary minus over a numeric operand; unary plus
// compiles to its operand directly (in compile.go). A NULL yields NULL.
type negExpr struct {
	x   cexpr
	out types.Type
}

func (e *negExpr) Type() types.Type { return e.out }

func (e *negExpr) eval(ctx *evalCtx) (types.Value, error) {
	v, err := e.x.eval(ctx)
	if err != nil {
		return nil, err
	}
	if v.IsNull() {
		return types.Null(e.out), nil
	}
	if n, ok := types.AsInt(v); ok {
		return types.NewInt(-n), nil
	}
	d, _ := types.AsDecimal(v)
	out := new(apd.Decimal)
	if _, err := apd.BaseContext.Neg(out, &d); err != nil {
		return nil, fmt.Errorf("exec: negation error: %w", err)
	}
	return types.NewDecimal(*out), nil
}
