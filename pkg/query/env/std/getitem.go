package std

import (
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func init() {
	env.Register(api.Function{
		Name:   "getitem",
		In:     []types.Type{types.DictType, types.String},
		Out:    types.Invalid,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(getitem),
	})
	env.Register(api.Function{
		Name:   "getitem",
		In:     []types.Type{types.DictType, types.String, types.String},
		Out:    types.Invalid,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(getitem),
	})
}

// getitem looks up a key in a dict and returns the stored value with its own
// runtime type. The optional third argument is the default returned when the
// dict is present but the key is absent (restricted to a String in the lean
// subset). A NULL (or non-dict) first argument yields a typed NULL of
// [types.Invalid] regardless of any default — the default applies only to a
// present dict's missing key. Without a default, a missing key also yields a
// typed NULL of [types.Invalid].
//
// getitem is the engine's only dynamically typed function: its declared Out
// is [types.Invalid] because a metadata value's kind is known only at
// runtime, and the compiler treats an Invalid-typed result as compatible
// with any operand. It backs the meta('k') / meta('k','fallback') sugar.
func getitem(args []types.Value) (types.Value, error) {
	d, ok := types.AsDict(args[0])
	if !ok {
		return types.Null(types.Invalid), nil
	}
	key, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.Invalid), nil
	}
	if v, found := d.Get(key); found {
		return v, nil
	}
	if len(args) == 3 {
		return args[2], nil
	}
	return types.Null(types.Invalid), nil
}
