package sprout

import (
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// coalesceTypes are the per-overload argument-and-result types coalesce is
// registered for. coalesce returns its argument unchanged, so it needs no
// per-type value extraction; this set is just the types BQL exposes as
// first-class column values.
var coalesceTypes = []types.Type{
	types.Int, types.Decimal, types.String, types.Amount,
	types.Bool, types.Date, types.SetType, types.DictType,
}

const coalesceMaxArity = 5

func init() {
	for _, t := range coalesceTypes {
		fn := coalesce(t)
		for arity := 1; arity <= coalesceMaxArity; arity++ {
			in := make([]types.Type, arity)
			for i := range in {
				in[i] = t
			}
			registerScalar("coalesce", in, t, fn)
		}
	}
}

// coalesce returns a scalar that yields its first non-NULL argument, or a
// typed NULL of out when every argument is NULL. All arguments share type
// out, so each is returned as-is.
func coalesce(out types.Type) api.Scalar {
	return func(args []types.Value) (types.Value, error) {
		for _, a := range args {
			if !a.IsNull() {
				return a, nil
			}
		}
		return types.Null(out), nil
	}
}

func registerScalar(name string, in []types.Type, out types.Type, fn api.Scalar) {
	env.Register(api.Function{
		Name:   name,
		In:     in,
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: fn,
	})
}
