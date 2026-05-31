package std

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// divPrecisionSafe matches the working precision of the / operator so
// safediv yields the same quotient as a guarded division would.
const divPrecisionSafe = 28

func init() {
	registerStrict("abs", []types.Type{types.Decimal}, types.Decimal, func(args []types.Value) (types.Value, error) {
		d, _ := coerceDecimal(args[0])
		return types.NewDecimal(absDecimal(d)), nil
	})
	registerStrict("abs", []types.Type{types.Position}, types.Position, func(args []types.Value) (types.Value, error) {
		p, _ := types.AsPosition(args[0])
		return types.NewPosition(mapPositionNumber(p, absDecimal)), nil
	})
	registerStrict("abs", []types.Type{types.Inventory}, types.Inventory, func(args []types.Value) (types.Value, error) {
		inv, _ := types.AsInventory(args[0])
		out, err := mapInventoryNumber(inv, absDecimal)
		if err != nil {
			return nil, err
		}
		return types.NewInventory(out), nil
	})

	registerStrict("neg", []types.Type{types.Decimal}, types.Decimal, func(args []types.Value) (types.Value, error) {
		d, _ := coerceDecimal(args[0])
		return types.NewDecimal(negDecimal(d)), nil
	})
	registerStrict("neg", []types.Type{types.Amount}, types.Amount, func(args []types.Value) (types.Value, error) {
		a, _ := types.AsAmount(args[0])
		return types.NewAmount(ast.Amount{Number: negDecimal(a.Number), Currency: a.Currency}), nil
	})
	registerStrict("neg", []types.Type{types.Position}, types.Position, func(args []types.Value) (types.Value, error) {
		p, _ := types.AsPosition(args[0])
		return types.NewPosition(mapPositionNumber(p, negDecimal)), nil
	})
	registerStrict("neg", []types.Type{types.Inventory}, types.Inventory, func(args []types.Value) (types.Value, error) {
		inv, _ := types.AsInventory(args[0])
		out, err := mapInventoryNumber(inv, negDecimal)
		if err != nil {
			return nil, err
		}
		return types.NewInventory(out), nil
	})

	registerStrict("safediv", []types.Type{types.Decimal, types.Decimal}, types.Decimal, func(args []types.Value) (types.Value, error) {
		num, _ := coerceDecimal(args[0])
		den, _ := coerceDecimal(args[1])
		if den.IsZero() {
			return types.NewDecimal(apd.Decimal{}), nil
		}
		var out apd.Decimal
		ctx := apd.BaseContext.WithPrecision(divPrecisionSafe)
		if _, err := ctx.Quo(&out, &num, &den); err != nil {
			return nil, err
		}
		out.Reduce(&out)
		return types.NewDecimal(out), nil
	})

	registerStrict("round", []types.Type{types.Decimal}, types.Decimal, func(args []types.Value) (types.Value, error) {
		d, _ := coerceDecimal(args[0])
		return roundDecimal(d, 0)
	})
	registerStrict("round", []types.Type{types.Decimal, types.Int}, types.Decimal, func(args []types.Value) (types.Value, error) {
		d, _ := coerceDecimal(args[0])
		digits, _ := types.AsInt(args[1])
		return roundDecimal(d, digits)
	})
}

// coerceDecimal extracts an apd.Decimal from a Decimal or Int value. The Int
// case covers an argument bound to a Decimal parameter via Int->Decimal
// widening, whose runtime value is still an Int.
func coerceDecimal(v types.Value) (apd.Decimal, bool) {
	if d, ok := types.AsDecimal(v); ok {
		return d, true
	}
	if n, ok := types.AsInt(v); ok {
		return *apd.New(n, 0), true
	}
	return apd.Decimal{}, false
}

// absDecimal returns |d|. Abs is exact under apd.BaseContext, so it cannot
// fail; the unreachable error is ignored as elsewhere in this codebase.
func absDecimal(d apd.Decimal) apd.Decimal {
	var out apd.Decimal
	_, _ = apd.BaseContext.Abs(&out, &d)
	return out
}

// roundDecimal rounds d to digits places past the point using banker's
// rounding (half-to-even), matching Python's round that upstream beanquery
// calls. A negative digits rounds to the left of the point.
func roundDecimal(d apd.Decimal, digits int64) (types.Value, error) {
	prec := uint32(d.NumDigits()) + 2
	if digits > 0 {
		prec += uint32(digits)
	}
	ctx := apd.BaseContext.WithPrecision(prec)
	ctx.Rounding = apd.RoundHalfEven
	var out apd.Decimal
	if _, err := ctx.Quantize(&out, &d, -int32(digits)); err != nil {
		return nil, err
	}
	return types.NewDecimal(out), nil
}

// mapPositionNumber returns a copy of p with its units number mapped through
// f; the cost lot is preserved.
func mapPositionNumber(p inventory.Position, f func(apd.Decimal) apd.Decimal) inventory.Position {
	q := p.Clone()
	q.Units.Number = f(p.Units.Number)
	return q
}

// mapInventoryNumber returns a fresh inventory with f applied to each
// position's units number. Positions that collide after mapping merge under
// the inventory's normal augmentation rules.
func mapInventoryNumber(inv *inventory.Inventory, f func(apd.Decimal) apd.Decimal) (*inventory.Inventory, error) {
	out := inventory.NewInventory()
	for p := range inv.All() {
		if err := out.Add(mapPositionNumber(p, f)); err != nil {
			return nil, err
		}
	}
	return out, nil
}
