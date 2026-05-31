package std

import (
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// registerStrict registers a scalar overload whose body runs only when every
// argument is non-NULL. If any argument is NULL the overload yields a typed
// NULL of out without invoking body, mirroring upstream beanquery's uniform
// NULL short-circuit (its @function decorator returns None when any argument
// is None). Functions that must inspect NULL arguments to do their job
// (coalesce, getitem) deliberately do not use this helper.
func registerStrict(name string, in []types.Type, out types.Type, body func([]types.Value) (types.Value, error)) {
	env.Register(api.Function{
		Name:   name,
		In:     in,
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func(args []types.Value) (types.Value, error) {
			for _, a := range args {
				if a.IsNull() {
					return types.Null(out), nil
				}
			}
			return body(args)
		}),
	})
}
