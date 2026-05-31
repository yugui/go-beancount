package std

import (
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/price"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// Price/valuation functions read the query-wide price map from the context.
// They consult the map nearest-on-or-before the optional date argument, or the
// latest rate when no date is given, and return a typed NULL when no rate is
// available or an argument is NULL. Conversion follows beanquery: convert
// coerces to a named currency, value coerces a position to its cost currency.
func init() {
	registerContextScalar("getprice", []types.Type{types.String, types.String}, types.Decimal, getprice)
	registerContextScalar("getprice", []types.Type{types.String, types.String, types.Date}, types.Decimal, getprice)

	registerContextScalar("convert", []types.Type{types.Amount, types.String}, types.Amount, convertAmount)
	registerContextScalar("convert", []types.Type{types.Amount, types.String, types.Date}, types.Amount, convertAmount)
	registerContextScalar("convert", []types.Type{types.Position, types.String}, types.Amount, convertPosition)
	registerContextScalar("convert", []types.Type{types.Position, types.String, types.Date}, types.Amount, convertPosition)
	registerContextScalar("convert", []types.Type{types.Inventory, types.String}, types.Inventory, convertInventory)
	registerContextScalar("convert", []types.Type{types.Inventory, types.String, types.Date}, types.Inventory, convertInventory)

	registerContextScalar("value", []types.Type{types.Position}, types.Amount, valuePosition)
	registerContextScalar("value", []types.Type{types.Position, types.Date}, types.Amount, valuePosition)
	registerContextScalar("value", []types.Type{types.Inventory}, types.Inventory, valueInventory)
	registerContextScalar("value", []types.Type{types.Inventory, types.Date}, types.Inventory, valueInventory)
}

func registerContextScalar(name string, in []types.Type, out types.Type, fn api.Scalar) {
	env.Register(api.Function{
		Name:   name,
		In:     in,
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: fn,
	})
}

func getprice(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	base, ok := types.AsString(args[0])
	if !ok {
		return types.Null(types.Decimal), nil
	}
	quote, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Decimal), nil
	}
	date, hasDate, ok := optDate(args, 2)
	if !ok {
		return types.Null(types.Decimal), nil
	}
	rate, ok := lookup(ctx, strings.ToUpper(base), strings.ToUpper(quote), date, hasDate)
	if !ok {
		return types.Null(types.Decimal), nil
	}
	return types.NewDecimal(rate), nil
}

func convertAmount(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	a, ok := types.AsAmount(args[0])
	if !ok {
		return types.Null(types.Amount), nil
	}
	target, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Amount), nil
	}
	date, hasDate, ok := optDate(args, 2)
	if !ok {
		return types.Null(types.Amount), nil
	}
	out, ok, err := convertUnits(ctx, a, strings.ToUpper(target), date, hasDate)
	if err != nil || !ok {
		return types.Null(types.Amount), err
	}
	return types.NewAmount(out), nil
}

func convertPosition(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	p, ok := types.AsPosition(args[0])
	if !ok {
		return types.Null(types.Amount), nil
	}
	target, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Amount), nil
	}
	date, hasDate, ok := optDate(args, 2)
	if !ok {
		return types.Null(types.Amount), nil
	}
	out, ok, err := convertUnits(ctx, p.Units, strings.ToUpper(target), date, hasDate)
	if err != nil || !ok {
		return types.Null(types.Amount), err
	}
	return types.NewAmount(out), nil
}

func convertInventory(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	inv, ok := types.AsInventory(args[0])
	if !ok {
		return types.Null(types.Inventory), nil
	}
	target, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Inventory), nil
	}
	date, hasDate, ok := optDate(args, 2)
	if !ok {
		return types.Null(types.Inventory), nil
	}
	upper := strings.ToUpper(target)
	out := inventory.NewInventory()
	for pos := range inv.All() {
		if err := addConverted(out, pos, ctx, upper, date, hasDate); err != nil {
			return nil, err
		}
	}
	return types.NewInventory(out), nil
}

func valuePosition(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	p, ok := types.AsPosition(args[0])
	if !ok {
		return types.Null(types.Amount), nil
	}
	date, hasDate, ok := optDate(args, 1)
	if !ok {
		return types.Null(types.Amount), nil
	}
	if p.Cost == nil {
		return types.NewAmount(p.Units), nil
	}
	out, ok, err := convertUnits(ctx, p.Units, p.Cost.Currency, date, hasDate)
	if err != nil || !ok {
		return types.Null(types.Amount), err
	}
	return types.NewAmount(out), nil
}

func valueInventory(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	inv, ok := types.AsInventory(args[0])
	if !ok {
		return types.Null(types.Inventory), nil
	}
	date, hasDate, ok := optDate(args, 1)
	if !ok {
		return types.Null(types.Inventory), nil
	}
	out := inventory.NewInventory()
	for pos := range inv.All() {
		target := pos.Units.Currency
		if pos.Cost != nil {
			target = pos.Cost.Currency
		}
		if err := addConverted(out, pos, ctx, target, date, hasDate); err != nil {
			return nil, err
		}
	}
	return types.NewInventory(out), nil
}

// addConverted folds pos into out after converting its units to target,
// passing the original position through unchanged when no rate is available.
func addConverted(out *inventory.Inventory, pos inventory.Position, ctx *price.QueryContext, target string, date time.Time, hasDate bool) error {
	conv, ok, err := convertUnits(ctx, pos.Units, target, date, hasDate)
	if err != nil {
		return err
	}
	if !ok {
		return out.Add(pos.Clone())
	}
	return out.Add(inventory.Position{Units: conv})
}

// convertUnits coerces units to target using the context's price map. ok is
// false (with a nil error) when no rate is available; units.Currency == target
// is the identity. Multiplication is exact (apd.BaseContext).
func convertUnits(ctx *price.QueryContext, units ast.Amount, target string, date time.Time, hasDate bool) (ast.Amount, bool, error) {
	if units.Currency == target {
		return units, true, nil
	}
	rate, ok := lookup(ctx, units.Currency, target, date, hasDate)
	if !ok {
		return ast.Amount{}, false, nil
	}
	var out apd.Decimal
	if _, err := apd.BaseContext.Mul(&out, &units.Number, &rate); err != nil {
		return ast.Amount{}, false, err
	}
	return ast.Amount{Number: out, Currency: target}, true, nil
}

func lookup(ctx *price.QueryContext, base, quote string, date time.Time, hasDate bool) (apd.Decimal, bool) {
	if ctx == nil || ctx.Prices == nil {
		return apd.Decimal{}, false
	}
	if hasDate {
		return ctx.Prices.Get(base, quote, date)
	}
	return ctx.Prices.Latest(base, quote)
}

// optDate reads an optional trailing Date argument at idx. hasDate is false
// when the argument is absent; ok is false when it is present but NULL or not a
// Date, so the caller propagates a typed NULL.
func optDate(args []types.Value, idx int) (date time.Time, hasDate, ok bool) {
	if len(args) <= idx {
		return time.Time{}, false, true
	}
	d, isDate := types.AsDate(args[idx])
	if !isDate {
		return time.Time{}, false, false
	}
	return d, true, true
}
