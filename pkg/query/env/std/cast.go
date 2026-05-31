package std

import (
	"strconv"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// The cast scalars mirror upstream beanquery's type-conversion functions.
// They take a [types.Any] argument so a NULL literal and any value kind are
// accepted at the call site; a conversion that cannot succeed yields NULL
// (matching upstream, whose conversions catch ValueError/TypeError and
// return None) rather than a query error.
func init() {
	registerStrict("str", []types.Type{types.Any}, types.String, func(args []types.Value) (types.Value, error) {
		if b, ok := types.AsBool(args[0]); ok {
			if b {
				return types.NewString("TRUE"), nil
			}
			return types.NewString("FALSE"), nil
		}
		return types.NewString(args[0].Format()), nil
	})

	registerStrict("repr", []types.Type{types.Any}, types.String, func(args []types.Value) (types.Value, error) {
		return types.NewString(args[0].String()), nil
	})

	registerStrict("bool", []types.Type{types.Any}, types.Bool, func(args []types.Value) (types.Value, error) {
		return types.NewBool(truthy(args[0])), nil
	})

	registerStrict("int", []types.Type{types.Any}, types.Int, func(args []types.Value) (types.Value, error) {
		return toInt(args[0]), nil
	})

	registerStrict("decimal", []types.Type{types.Any}, types.Decimal, func(args []types.Value) (types.Value, error) {
		return toDecimalValue(args[0]), nil
	})

	registerStrict("date", []types.Type{types.Any}, types.Date, func(args []types.Value) (types.Value, error) {
		return toDate(args[0]), nil
	})
	registerStrict("date", []types.Type{types.Int, types.Int, types.Int}, types.Date, func(args []types.Value) (types.Value, error) {
		y, _ := types.AsInt(args[0])
		m, _ := types.AsInt(args[1])
		d, _ := types.AsInt(args[2])
		t := time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC)
		if t.Year() != int(y) || t.Month() != time.Month(m) || t.Day() != int(d) {
			return types.Null(types.Date), nil
		}
		return types.NewDate(t), nil
	})
}

func truthy(v types.Value) bool {
	switch v.Type() {
	case types.Bool:
		b, _ := types.AsBool(v)
		return b
	case types.Int:
		n, _ := types.AsInt(v)
		return n != 0
	case types.Decimal:
		d, _ := types.AsDecimal(v)
		return !d.IsZero()
	case types.String:
		s, _ := types.AsString(v)
		return s != ""
	default:
		return true
	}
}

func toInt(v types.Value) types.Value {
	switch v.Type() {
	case types.Int:
		return v
	case types.Bool:
		if b, _ := types.AsBool(v); b {
			return types.NewInt(1)
		}
		return types.NewInt(0)
	case types.Decimal:
		d, _ := types.AsDecimal(v)
		var truncated apd.Decimal
		ctx := apd.BaseContext.WithPrecision(uint32(d.NumDigits()) + 1)
		ctx.Rounding = apd.RoundDown
		if _, err := ctx.RoundToIntegralValue(&truncated, &d); err != nil {
			return types.Null(types.Int)
		}
		n, err := truncated.Int64()
		if err != nil {
			return types.Null(types.Int)
		}
		return types.NewInt(n)
	case types.String:
		s, _ := types.AsString(v)
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return types.Null(types.Int)
		}
		return types.NewInt(n)
	default:
		return types.Null(types.Int)
	}
}

func toDecimalValue(v types.Value) types.Value {
	switch v.Type() {
	case types.Decimal:
		return v
	case types.Int:
		n, _ := types.AsInt(v)
		return types.NewDecimal(*apd.New(n, 0))
	case types.Bool:
		if b, _ := types.AsBool(v); b {
			return types.NewDecimal(*apd.New(1, 0))
		}
		return types.NewDecimal(*apd.New(0, 0))
	case types.String:
		s, _ := types.AsString(v)
		d, _, err := apd.NewFromString(s)
		if err != nil {
			return types.Null(types.Decimal)
		}
		return types.NewDecimal(*d)
	default:
		return types.Null(types.Decimal)
	}
}

func toDate(v types.Value) types.Value {
	switch v.Type() {
	case types.Date:
		return v
	case types.String:
		s, _ := types.AsString(v)
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return types.Null(types.Date)
		}
		return types.NewDate(t)
	default:
		return types.Null(types.Date)
	}
}
