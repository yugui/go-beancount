package std

import (
	"regexp"
	"strings"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func init() {
	registerStringScalar("upper", types.String, func(s string) types.Value {
		return types.NewString(strings.ToUpper(s))
	})
	registerStringScalar("lower", types.String, func(s string) types.Value {
		return types.NewString(strings.ToLower(s))
	})
	registerStringScalar("length", types.Int, func(s string) types.Value {
		return types.NewInt(int64(len([]rune(s))))
	})

	env.Register(api.Function{
		Name:   "substr",
		In:     []types.Type{types.String, types.Int, types.Int},
		Out:    types.String,
		Flavor: api.ScalarFlavor,
		Scalar: substr,
	})
	env.Register(api.Function{
		Name:   "grep",
		In:     []types.Type{types.String, types.String},
		Out:    types.String,
		Flavor: api.ScalarFlavor,
		Scalar: grep,
	})
}

// registerStringScalar registers a unary scalar over a String argument that
// returns out. A NULL (or non-String) argument yields a typed NULL of out.
func registerStringScalar(name string, out types.Type, fn func(string) types.Value) {
	env.Register(api.Function{
		Name:   name,
		In:     []types.Type{types.String},
		Out:    out,
		Flavor: api.ScalarFlavor,
		Scalar: func(args []types.Value) (types.Value, error) {
			s, ok := types.AsString(args[0])
			if !ok {
				return types.Null(out), nil
			}
			return fn(s), nil
		},
	})
}

func substr(args []types.Value) (types.Value, error) {
	s, ok := types.AsString(args[0])
	if !ok {
		return types.Null(types.String), nil
	}
	start, ok := types.AsInt(args[1])
	if !ok {
		return types.Null(types.String), nil
	}
	length, ok := types.AsInt(args[2])
	if !ok {
		return types.Null(types.String), nil
	}
	runes := []rune(s)
	n := int64(len(runes))
	lo := clamp(start, 0, n)
	hi := clamp(lo+max64(length, 0), lo, n)
	return types.NewString(string(runes[lo:hi])), nil
}

func grep(args []types.Value) (types.Value, error) {
	pattern, ok := types.AsString(args[0])
	if !ok {
		return types.Null(types.String), nil
	}
	s, ok := types.AsString(args[1])
	if !ok {
		return types.Null(types.String), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	m := re.FindString(s)
	if m == "" && !re.MatchString(s) {
		return types.Null(types.String), nil
	}
	return types.NewString(m), nil
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
