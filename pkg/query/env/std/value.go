package std

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func init() {
	registerScalar("number", []types.Type{types.Amount}, types.Decimal, func(args []types.Value) (types.Value, error) {
		a, ok := types.AsAmount(args[0])
		if !ok {
			return types.Null(types.Decimal), nil
		}
		return types.NewDecimal(a.Number), nil
	})
	registerScalar("number", []types.Type{types.Position}, types.Decimal, func(args []types.Value) (types.Value, error) {
		p, ok := types.AsPosition(args[0])
		if !ok {
			return types.Null(types.Decimal), nil
		}
		return types.NewDecimal(p.Units.Number), nil
	})

	registerScalar("currency", []types.Type{types.Amount}, types.String, func(args []types.Value) (types.Value, error) {
		a, ok := types.AsAmount(args[0])
		if !ok {
			return types.Null(types.String), nil
		}
		return types.NewString(a.Currency), nil
	})
	registerScalar("currency", []types.Type{types.Position}, types.String, func(args []types.Value) (types.Value, error) {
		p, ok := types.AsPosition(args[0])
		if !ok {
			return types.Null(types.String), nil
		}
		return types.NewString(p.Units.Currency), nil
	})

	registerScalar("units", []types.Type{types.Position}, types.Amount, func(args []types.Value) (types.Value, error) {
		p, ok := types.AsPosition(args[0])
		if !ok {
			return types.Null(types.Amount), nil
		}
		return types.NewAmount(p.Units), nil
	})

	registerScalar("cost", []types.Type{types.Position}, types.Amount, func(args []types.Value) (types.Value, error) {
		p, ok := types.AsPosition(args[0])
		if !ok || p.Cost == nil {
			return types.Null(types.Amount), nil
		}
		return positionCost(p)
	})
}

func registerScalar(name string, in []types.Type, out types.Type, fn func([]types.Value) (types.Value, error)) {
	env.Register(api.Function{
		Name:   name,
		In:     in,
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(fn),
	})
}

// positionCost returns the position's total cost: units.Number × lot.Number
// in the lot currency, computed exactly. The caller guarantees p.Cost != nil.
func positionCost(p inventory.Position) (types.Value, error) {
	var total apd.Decimal
	if _, err := apd.BaseContext.Mul(&total, &p.Units.Number, &p.Cost.Number); err != nil {
		return nil, err
	}
	return types.NewAmount(ast.Amount{Number: total, Currency: p.Cost.Currency}), nil
}
