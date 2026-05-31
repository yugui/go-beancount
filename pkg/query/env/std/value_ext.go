package std

import (
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// This file completes the inventory/value function family to upstream
// beanquery parity: the Inventory overloads of units and cost, the
// inventory accessors only/empty/filter_currency, the commodity alias of
// currency, the Set overload of length, and the n-component form of root.
func init() {
	registerStrict("units", []types.Type{types.Inventory}, types.Inventory, func(args []types.Value) (types.Value, error) {
		inv, _ := types.AsInventory(args[0])
		out := inventory.NewInventory()
		for p := range inv.All() {
			if err := out.Add(inventory.Position{Units: p.Units}); err != nil {
				return nil, err
			}
		}
		return types.NewInventory(out), nil
	})

	registerStrict("cost", []types.Type{types.Inventory}, types.Inventory, func(args []types.Value) (types.Value, error) {
		inv, _ := types.AsInventory(args[0])
		out := inventory.NewInventory()
		for p := range inv.All() {
			amt, err := positionCostAmount(p)
			if err != nil {
				return nil, err
			}
			if err := out.Add(inventory.Position{Units: amt}); err != nil {
				return nil, err
			}
		}
		return types.NewInventory(out), nil
	})

	registerStrict("only", []types.Type{types.String, types.Inventory}, types.Amount, func(args []types.Value) (types.Value, error) {
		currency, _ := types.AsString(args[0])
		inv, _ := types.AsInventory(args[1])
		var sum apd.Decimal
		for p := range inv.All() {
			if p.Units.Currency != currency {
				continue
			}
			if _, err := apd.BaseContext.Add(&sum, &sum, &p.Units.Number); err != nil {
				return nil, err
			}
		}
		return types.NewAmount(ast.Amount{Number: sum, Currency: currency}), nil
	})

	registerStrict("empty", []types.Type{types.Inventory}, types.Bool, func(args []types.Value) (types.Value, error) {
		inv, _ := types.AsInventory(args[0])
		return types.NewBool(inv.IsEmpty()), nil
	})

	registerStrict("filter_currency", []types.Type{types.Position, types.String}, types.Position, func(args []types.Value) (types.Value, error) {
		p, _ := types.AsPosition(args[0])
		currency, _ := types.AsString(args[1])
		if p.Units.Currency != currency {
			return types.Null(types.Position), nil
		}
		return types.NewPosition(p), nil
	})
	registerStrict("filter_currency", []types.Type{types.Inventory, types.String}, types.Inventory, func(args []types.Value) (types.Value, error) {
		inv, _ := types.AsInventory(args[0])
		currency, _ := types.AsString(args[1])
		out := inventory.NewInventory()
		for p := range inv.All() {
			if p.Units.Currency != currency {
				continue
			}
			if err := out.Add(p); err != nil {
				return nil, err
			}
		}
		return types.NewInventory(out), nil
	})

	registerStrict("commodity", []types.Type{types.Amount}, types.String, func(args []types.Value) (types.Value, error) {
		a, _ := types.AsAmount(args[0])
		return types.NewString(a.Currency), nil
	})

	registerStrict("length", []types.Type{types.SetType}, types.Int, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsSet(args[0])
		return types.NewInt(int64(s.Len())), nil
	})

	registerStrict("root", []types.Type{types.String, types.Int}, types.String, func(args []types.Value) (types.Value, error) {
		acc, _ := types.AsString(args[0])
		n, _ := types.AsInt(args[1])
		parts := ast.Account(acc).Parts()
		if n < 0 {
			n = 0
		}
		if int(n) < len(parts) {
			parts = parts[:n]
		}
		return types.NewString(strings.Join(parts, ":")), nil
	})
}

// positionCostAmount returns the position's total cost as an Amount: for a
// position with a lot it is units.Number × lot.Number in the lot currency;
// for a lot-free position it is the units amount unchanged. This mirrors
// upstream's convert.get_cost.
func positionCostAmount(p inventory.Position) (ast.Amount, error) {
	if p.Cost == nil {
		return p.Units, nil
	}
	var total apd.Decimal
	if _, err := apd.BaseContext.Mul(&total, &p.Units.Number, &p.Cost.Number); err != nil {
		return ast.Amount{}, err
	}
	return ast.Amount{Number: total, Currency: p.Cost.Currency}, nil
}
