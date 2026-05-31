package types_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestTypeString(t *testing.T) {
	cases := map[types.Type]string{
		types.Invalid:   "invalid",
		types.Bool:      "bool",
		types.Int:       "int",
		types.Decimal:   "decimal",
		types.String:    "string",
		types.Date:      "date",
		types.Amount:    "amount",
		types.Position:  "position",
		types.Inventory: "inventory",
		types.Interval:  "interval",
		types.SetType:   "set",
		types.DictType:  "dict",
		types.Entry:     "entry",
	}
	for ty, want := range cases {
		if got := ty.String(); got != want {
			t.Errorf("Type(%d).String() = %q, want %q", int(ty), got, want)
		}
	}
}

// The relative ordinal order is part of the cross-type tiebreak contract.
func TestTypeOrdinalOrdering(t *testing.T) {
	ascending := []types.Type{
		types.Bool, types.Int, types.Decimal, types.String, types.Date,
		types.Amount, types.Position, types.Inventory, types.Interval,
		types.SetType, types.DictType, types.Entry,
	}
	for i := 0; i+1 < len(ascending); i++ {
		if !(ascending[i] < ascending[i+1]) {
			t.Errorf("ordinal order broken: %v not < %v", ascending[i], ascending[i+1])
		}
	}
}
