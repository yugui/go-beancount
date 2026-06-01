package std

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// countTypes is the set of lean value types over which count is registered.
// count is type-generic: one overload per type using the same accumulator.
var countTypes = []types.Type{
	types.Bool, types.Int, types.Decimal, types.String, types.Date,
	types.Amount, types.Position, types.Inventory, types.SetType, types.DictType,
}

// orderedTypes is the set of comparable lean value types over which min, max,
// first, and last are registered, one overload per type.
var orderedTypes = []types.Type{
	types.Int, types.Decimal, types.String, types.Date,
	types.Amount, types.Position, types.Bool,
}

func init() {
	for _, t := range countTypes {
		registerAggregator("count", []types.Type{t}, types.Int, func() api.Accumulator {
			return &countAcc{}
		})
	}

	registerAggregator("sum", []types.Type{types.Int}, types.Int, func() api.Accumulator {
		return &sumIntAcc{}
	})
	registerAggregator("sum", []types.Type{types.Decimal}, types.Decimal, func() api.Accumulator {
		return &sumDecimalAcc{}
	})
	registerAggregator("sum", []types.Type{types.Position}, types.Inventory, func() api.Accumulator {
		return &sumPositionAcc{inv: inventory.NewInventory()}
	})
	registerAggregator("sum", []types.Type{types.Amount}, types.Inventory, func() api.Accumulator {
		return &sumAmountAcc{inv: inventory.NewInventory()}
	})

	for _, t := range orderedTypes {
		registerAggregator("min", []types.Type{t}, t, func() api.Accumulator {
			return &minMaxAcc{out: t, dir: -1}
		})
		registerAggregator("max", []types.Type{t}, t, func() api.Accumulator {
			return &minMaxAcc{out: t, dir: +1}
		})
		registerAggregator("first", []types.Type{t}, t, func() api.Accumulator {
			return &edgeAcc{out: t}
		})
		registerAggregator("last", []types.Type{t}, t, func() api.Accumulator {
			return &edgeAcc{out: t, last: true}
		})
	}
}

func registerAggregator(name string, in []types.Type, out types.Type, newAcc api.NewAccumulator) {
	env.Register(api.Function{
		Name:       name,
		In:         in,
		Out:        out,
		Flavor:     api.AggregatorFlavor,
		Aggregator: newAcc,
	})
}

// countAcc counts non-null argument values. It is type-generic: it holds no
// type-specific state, so a single implementation backs every count overload.
type countAcc struct{ n int64 }

func (a *countAcc) Add(args []types.Value) error {
	if !args[0].IsNull() {
		a.n++
	}
	return nil
}

func (a *countAcc) Merge(o api.Accumulator) error {
	a.n += o.(*countAcc).n
	return nil
}

func (a *countAcc) Result() (types.Value, error) { return types.NewInt(a.n), nil }

type sumIntAcc struct{ sum int64 }

func (a *sumIntAcc) Add(args []types.Value) error {
	if n, ok := types.AsInt(args[0]); ok {
		a.sum += n
	}
	return nil
}

func (a *sumIntAcc) Merge(o api.Accumulator) error {
	a.sum += o.(*sumIntAcc).sum
	return nil
}

func (a *sumIntAcc) Result() (types.Value, error) { return types.NewInt(a.sum), nil }

type sumDecimalAcc struct{ sum apd.Decimal }

func (a *sumDecimalAcc) Add(args []types.Value) error {
	d, ok := types.AsDecimal(args[0])
	if !ok {
		return nil
	}
	_, err := apd.BaseContext.Add(&a.sum, &a.sum, &d)
	return err
}

func (a *sumDecimalAcc) Merge(o api.Accumulator) error {
	other := o.(*sumDecimalAcc)
	_, err := apd.BaseContext.Add(&a.sum, &a.sum, &other.sum)
	return err
}

func (a *sumDecimalAcc) Result() (types.Value, error) { return types.NewDecimal(a.sum), nil }

// sumPositionAcc folds positions into an inventory via [inventory.Inventory.Add],
// which merges same-commodity, same-lot positions per Beancount's
// augmentation rule. The empty group yields an empty inventory.
type sumPositionAcc struct{ inv *inventory.Inventory }

func (a *sumPositionAcc) Add(args []types.Value) error {
	p, ok := types.AsPosition(args[0])
	if !ok {
		return nil
	}
	return a.inv.Add(p)
}

func (a *sumPositionAcc) Merge(o api.Accumulator) error {
	for p := range o.(*sumPositionAcc).inv.All() {
		if err := a.inv.Add(p); err != nil {
			return err
		}
	}
	return nil
}

func (a *sumPositionAcc) Result() (types.Value, error) { return types.NewInventory(a.inv), nil }

// sumAmountAcc folds bare amounts into an inventory as cash positions (no cost
// lot), so SUM over a valuation like cost(position) or value(position) yields
// an inventory.
type sumAmountAcc struct{ inv *inventory.Inventory }

func (a *sumAmountAcc) Add(args []types.Value) error {
	amt, ok := types.AsAmount(args[0])
	if !ok {
		return nil
	}
	return a.inv.Add(inventory.Position{Units: amt})
}

func (a *sumAmountAcc) Merge(o api.Accumulator) error {
	for p := range o.(*sumAmountAcc).inv.All() {
		if err := a.inv.Add(p); err != nil {
			return err
		}
	}
	return nil
}

func (a *sumAmountAcc) Result() (types.Value, error) { return types.NewInventory(a.inv), nil }

// minMaxAcc holds the extreme non-null value seen, ordered by
// [types.Value.Compare]. dir is +1 for max and -1 for min. NULLs are skipped;
// a group with no non-null value yields a typed NULL of out.
type minMaxAcc struct {
	out  types.Type
	dir  int
	best types.Value
}

func (a *minMaxAcc) Add(args []types.Value) error {
	a.consider(args[0])
	return nil
}

func (a *minMaxAcc) Merge(o api.Accumulator) error {
	a.consider(o.(*minMaxAcc).best)
	return nil
}

func (a *minMaxAcc) consider(v types.Value) {
	if v == nil || v.IsNull() {
		return
	}
	if a.best == nil || sign(v.Compare(a.best)) == a.dir {
		a.best = v
	}
}

func (a *minMaxAcc) Result() (types.Value, error) {
	if a.best == nil {
		return types.Null(a.out), nil
	}
	return a.best, nil
}

// edgeAcc holds the first or last non-null value in group fold order. Merge
// folds a later partial in: first keeps the earlier value, last the later
// one. A group with no non-null value yields a typed NULL of out.
type edgeAcc struct {
	out  types.Type
	last bool
	val  types.Value
}

func (a *edgeAcc) Add(args []types.Value) error {
	v := args[0]
	if v == nil || v.IsNull() {
		return nil
	}
	if a.last || a.val == nil {
		a.val = v
	}
	return nil
}

func (a *edgeAcc) Merge(o api.Accumulator) error {
	other := o.(*edgeAcc)
	if other.val == nil {
		return nil
	}
	if a.last || a.val == nil {
		a.val = other.val
	}
	return nil
}

func (a *edgeAcc) Result() (types.Value, error) {
	if a.val == nil {
		return types.Null(a.out), nil
	}
	return a.val, nil
}

func sign(c int) int {
	switch {
	case c < 0:
		return -1
	case c > 0:
		return +1
	default:
		return 0
	}
}
