package sprout

import (
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// This file re-provides the convenient date renderings that the std library
// dropped when its weekday/quarter/yearmonth scalars were aligned to upstream
// beanquery return types (3-letter weekday abbreviation / "YYYY-Qn" string /
// month-truncated date). The variants live here, under distinct names, so
// callers who want a full weekday name, a 1..4 quarter ordinal, or a
// "YYYY-MM" label keep them without shadowing the upstream-parity std names.
func init() {
	registerDateScalar("weekday_name", types.String, func(t time.Time) types.Value {
		return types.NewString(t.Weekday().String())
	})
	registerDateScalar("quarter_index", types.Int, func(t time.Time) types.Value {
		return types.NewInt(int64((int(t.Month())-1)/3 + 1))
	})
	registerDateScalar("yearmonth_str", types.String, func(t time.Time) types.Value {
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
