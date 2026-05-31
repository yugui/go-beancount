package std

import (
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func init() {
	registerDateScalar("year", types.Int, func(t time.Time) types.Value {
		return types.NewInt(int64(t.Year()))
	})
	registerDateScalar("month", types.Int, func(t time.Time) types.Value {
		return types.NewInt(int64(t.Month()))
	})
	registerDateScalar("day", types.Int, func(t time.Time) types.Value {
		return types.NewInt(int64(t.Day()))
	})
	registerDateScalar("weekday", types.String, func(t time.Time) types.Value {
		return types.NewString(t.Weekday().String())
	})
	registerDateScalar("quarter", types.Int, func(t time.Time) types.Value {
		return types.NewInt(int64((int(t.Month())-1)/3 + 1))
	})
	registerDateScalar("yearmonth", types.String, func(t time.Time) types.Value {
		return types.NewString(fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month())))
	})
}

// registerDateScalar registers a unary scalar over a Date argument that
// returns out. A NULL (or non-Date) argument yields a typed NULL of out.
func registerDateScalar(name string, out types.Type, fn func(time.Time) types.Value) {
	env.Register(api.Function{
		Name:   name,
		In:     []types.Type{types.Date},
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: api.Pure(func(args []types.Value) (types.Value, error) {
			d, ok := types.AsDate(args[0])
			if !ok {
				return types.Null(out), nil
			}
			return fn(d), nil
		}),
	})
}
