package exec

import (
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// evalCtx is the per-row (or per-group) state a [cexpr] reads during
// evaluation. row is the current input row (the representative row of a
// group in aggregate mode). aggResults holds the finalized aggregate
// values for the current group, indexed by aggregate slot; it is nil
// outside aggregate mode.
type evalCtx struct {
	row        table.Row
	aggResults []types.Value
}

// cexpr is a compiled, statically-typed expression. Type reports the
// static result type fixed at compile time; eval computes the value for
// the row carried by ctx. An eval error is a runtime query failure (for
// example a malformed regular expression), never a panic.
type cexpr interface {
	Type() types.Type
	eval(ctx *evalCtx) (types.Value, error)
}

// columnExpr reads a table column from the current row.
type columnExpr struct {
	col table.Column
}

func (e *columnExpr) Type() types.Type { return e.col.Type }

func (e *columnExpr) eval(ctx *evalCtx) (types.Value, error) {
	return e.col.Accessor(ctx.row), nil
}

// literalExpr is a constant value. An untyped NULL literal carries
// [types.Invalid] as its static type so operators treat it as compatible
// with any operand (see operators.go).
type literalExpr struct {
	typ types.Type
	val types.Value
}

func (e *literalExpr) Type() types.Type                   { return e.typ }
func (e *literalExpr) eval(*evalCtx) (types.Value, error) { return e.val, nil }

// aggRefExpr resolves to the finalized result of one aggregate slot.
type aggRefExpr struct {
	slot int
	typ  types.Type
}

func (e *aggRefExpr) Type() types.Type { return e.typ }

func (e *aggRefExpr) eval(ctx *evalCtx) (types.Value, error) {
	return ctx.aggResults[e.slot], nil
}

// scalarExpr applies a resolved scalar overload to its argument exprs.
type scalarExpr struct {
	fn   api.Scalar
	args []cexpr
	out  types.Type
}

func (e *scalarExpr) Type() types.Type { return e.out }

func (e *scalarExpr) eval(ctx *evalCtx) (types.Value, error) {
	vals := make([]types.Value, len(e.args))
	for i, a := range e.args {
		v, err := a.eval(ctx)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return e.fn(vals)
}
