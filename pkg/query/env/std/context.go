package std

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/price"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func init() {
	registerContextScalar("open_date", []types.Type{types.String}, types.Date, openDate)
	registerContextScalar("close_date", []types.Type{types.String}, types.Date, closeDate)

	registerContextScalar("open_meta", []types.Type{types.String}, types.DictType, openMetaDict)
	registerContextScalar("open_meta", []types.Type{types.String, types.String}, types.Invalid, openMetaKey)

	registerContextScalar("currency_meta", []types.Type{types.String}, types.DictType, currencyMetaDict)
	registerContextScalar("currency_meta", []types.Type{types.String, types.String}, types.Invalid, currencyMetaKey)
	registerContextScalar("commodity_meta", []types.Type{types.String}, types.DictType, currencyMetaDict)
	registerContextScalar("commodity_meta", []types.Type{types.String, types.String}, types.Invalid, currencyMetaKey)

	registerContextScalar("account_sortkey", []types.Type{types.String}, types.String, accountSortkey)
	registerContextScalar("has_account", []types.Type{types.String}, types.Bool, hasAccount)

	registerContextScalar("possign", []types.Type{types.Decimal, types.String}, types.Decimal, possignDecimal)
	registerContextScalar("possign", []types.Type{types.Amount, types.String}, types.Amount, possignAmount)
	registerContextScalar("possign", []types.Type{types.Position, types.String}, types.Position, possignPosition)
	registerContextScalar("possign", []types.Type{types.Inventory, types.String}, types.Inventory, possignInventory)
}

func openDate(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	acct, ok := accountArg(ctx, args[0])
	if !ok {
		return types.Null(types.Date), nil
	}
	t, ok := ctx.Dirs.OpenDate(acct)
	if !ok {
		return types.Null(types.Date), nil
	}
	return types.NewDate(t), nil
}

func closeDate(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	acct, ok := accountArg(ctx, args[0])
	if !ok {
		return types.Null(types.Date), nil
	}
	t, ok := ctx.Dirs.CloseDate(acct)
	if !ok {
		return types.Null(types.Date), nil
	}
	return types.NewDate(t), nil
}

func openMetaDict(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	acct, ok := accountArg(ctx, args[0])
	if !ok {
		return types.Null(types.DictType), nil
	}
	d, ok := ctx.Dirs.OpenMeta(acct)
	if !ok {
		return types.Null(types.DictType), nil
	}
	return d, nil
}

func openMetaKey(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	acct, ok := accountArg(ctx, args[0])
	if !ok {
		return types.Null(types.Invalid), nil
	}
	key, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Invalid), nil
	}
	d, ok := ctx.Dirs.OpenMeta(acct)
	if !ok {
		return types.Null(types.Invalid), nil
	}
	v, found := d.Get(key)
	if !found {
		return types.Null(types.Invalid), nil
	}
	return v, nil
}

func currencyMetaDict(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	currency, ok := types.AsString(args[0])
	if !ok {
		return types.Null(types.DictType), nil
	}
	if ctx == nil || ctx.Dirs == nil {
		return types.Null(types.DictType), nil
	}
	d, ok := ctx.Dirs.CurrencyMeta(currency)
	if !ok {
		return types.Null(types.DictType), nil
	}
	return d, nil
}

func currencyMetaKey(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	currency, ok := types.AsString(args[0])
	if !ok {
		return types.Null(types.Invalid), nil
	}
	key, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Invalid), nil
	}
	if ctx == nil || ctx.Dirs == nil {
		return types.Null(types.Invalid), nil
	}
	d, ok := ctx.Dirs.CurrencyMeta(currency)
	if !ok {
		return types.Null(types.Invalid), nil
	}
	v, found := d.Get(key)
	if !found {
		return types.Null(types.Invalid), nil
	}
	return v, nil
}

func accountSortkey(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	acct, ok := accountArg(ctx, args[0])
	if !ok {
		return types.Null(types.String), nil
	}
	return types.NewString(ctx.Dirs.SortKey(acct)), nil
}

func hasAccount(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	acct, ok := accountArg(ctx, args[0])
	if !ok {
		return types.NewBool(false), nil
	}
	return types.NewBool(ctx.Dirs.HasAccount(acct)), nil
}

func possignDecimal(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	d, ok := types.AsDecimal(args[0])
	if !ok {
		return types.Null(types.Decimal), nil
	}
	sign, ok := dirSign(ctx, args[1])
	if !ok {
		return types.Null(types.Decimal), nil
	}
	if sign < 0 {
		return types.NewDecimal(negDecimal(d)), nil
	}
	return types.NewDecimal(d), nil
}

func possignAmount(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	a, ok := types.AsAmount(args[0])
	if !ok {
		return types.Null(types.Amount), nil
	}
	sign, ok := dirSign(ctx, args[1])
	if !ok {
		return types.Null(types.Amount), nil
	}
	if sign < 0 {
		return types.NewAmount(ast.Amount{Number: negDecimal(a.Number), Currency: a.Currency}), nil
	}
	return types.NewAmount(a), nil
}

func possignPosition(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	p, ok := types.AsPosition(args[0])
	if !ok {
		return types.Null(types.Position), nil
	}
	sign, ok := dirSign(ctx, args[1])
	if !ok {
		return types.Null(types.Position), nil
	}
	if sign < 0 {
		return types.NewPosition(inventory.Position{
			Units: ast.Amount{Number: negDecimal(p.Units.Number), Currency: p.Units.Currency},
			Cost:  p.Cost.Clone(),
		}), nil
	}
	return types.NewPosition(p), nil
}

func possignInventory(ctx *price.QueryContext, args []types.Value) (types.Value, error) {
	inv, ok := types.AsInventory(args[0])
	if !ok {
		return types.Null(types.Inventory), nil
	}
	sign, ok := dirSign(ctx, args[1])
	if !ok {
		return types.Null(types.Inventory), nil
	}
	if sign >= 0 {
		return types.NewInventory(inv), nil
	}
	out := inventory.NewInventory()
	for pos := range inv.All() {
		negPos := inventory.Position{
			Units: ast.Amount{Number: negDecimal(pos.Units.Number), Currency: pos.Units.Currency},
			Cost:  pos.Cost.Clone(),
		}
		if err := out.Add(negPos); err != nil {
			return types.Null(types.Inventory), nil
		}
	}
	return types.NewInventory(out), nil
}

// negDecimal returns -d; exact for all finite decimal inputs.
func negDecimal(d apd.Decimal) apd.Decimal {
	var neg apd.Decimal
	_, _ = apd.BaseContext.Neg(&neg, &d)
	return neg
}

// accountArg extracts and validates a String arg as ast.Account, checking ctx.
func accountArg(ctx *price.QueryContext, v types.Value) (ast.Account, bool) {
	s, ok := types.AsString(v)
	if !ok {
		return "", false
	}
	if ctx == nil || ctx.Dirs == nil {
		return "", false
	}
	return ast.Account(s), true
}

// dirSign returns the beancount sign for the account named by v.
// ok is false when v is NULL/non-string, or ctx or ctx.Dirs is nil.
func dirSign(ctx *price.QueryContext, v types.Value) (int, bool) {
	acct, ok := accountArg(ctx, v)
	if !ok {
		return 0, false
	}
	return ctx.Dirs.Sign(acct), true
}
