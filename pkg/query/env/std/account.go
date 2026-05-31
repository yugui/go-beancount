package std

import (
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func init() {
	registerAccountScalar("root", func(a ast.Account) types.Value {
		return types.NewString(string(a.Root()))
	})
	registerAccountScalar("parent", func(a ast.Account) types.Value {
		return types.NewString(string(a.Parent()))
	})
	registerAccountScalar("leaf", func(a ast.Account) types.Value {
		parts := a.Parts()
		if len(parts) == 0 {
			return types.Null(types.String)
		}
		return types.NewString(parts[len(parts)-1])
	})
}

// registerAccountScalar registers a unary scalar that reads the account
// column (a String) as an [ast.Account] and maps it through fn. A NULL (or
// non-String) argument yields a typed NULL string.
func registerAccountScalar(name string, fn func(ast.Account) types.Value) {
	env.Register(api.Function{
		Name:   name,
		In:     []types.Type{types.String},
		Out:    types.String,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func(args []types.Value) (types.Value, error) {
			s, ok := types.AsString(args[0])
			if !ok {
				return types.Null(types.String), nil
			}
			return fn(ast.Account(s)), nil
		}),
	})
}
