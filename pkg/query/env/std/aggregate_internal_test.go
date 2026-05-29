package std

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// The lean executor folds each group with a single accumulator and never
// calls Merge (it documents the future parallel-executor slot but does not
// build it), so the mergeable law has no query-level coverage. These tests
// assert Add-then-Merge ≡ Add-all directly on the accumulators — the
// CLAUDE.md exception for a contract with no exported path.

func decOf(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

// assertMergeLaw folds rows into one accumulator (Add-all) and into two
// partials combined via Merge (Add-then-Merge), and asserts the Results are
// equal. split is the index at which rows are partitioned between the two
// partials.
func assertMergeLaw(t *testing.T, newAcc api.NewAccumulator, rows [][]types.Value, split int) {
	t.Helper()

	all := newAcc()
	for _, r := range rows {
		if err := all.Add(r); err != nil {
			t.Fatalf("Add-all: %v", err)
		}
	}
	allRes, err := all.Result()
	if err != nil {
		t.Fatalf("Add-all Result: %v", err)
	}

	left, right := newAcc(), newAcc()
	for i, r := range rows {
		dst := left
		if i >= split {
			dst = right
		}
		if err := dst.Add(r); err != nil {
			t.Fatalf("partial Add: %v", err)
		}
	}
	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	mergedRes, err := left.Result()
	if err != nil {
		t.Fatalf("merged Result: %v", err)
	}

	if allRes.Compare(mergedRes) != 0 {
		t.Errorf("merge law violated: Add-all=%v, Add-then-Merge=%v", allRes, mergedRes)
	}
}

func TestMergeLawCount(t *testing.T) {
	rows := [][]types.Value{
		{types.NewInt(1)}, {types.Null(types.Int)}, {types.NewInt(3)}, {types.NewInt(4)},
	}
	assertMergeLaw(t, func() api.Accumulator { return &countAcc{} }, rows, 2)
}

func TestMergeLawSumInt(t *testing.T) {
	rows := [][]types.Value{
		{types.NewInt(10)}, {types.Null(types.Int)}, {types.NewInt(-5)}, {types.NewInt(7)},
	}
	assertMergeLaw(t, func() api.Accumulator { return &sumIntAcc{} }, rows, 2)
}

func TestMergeLawSumDecimal(t *testing.T) {
	rows := [][]types.Value{
		{types.NewDecimal(decOf(t, "1.5"))},
		{types.Null(types.Decimal)},
		{types.NewDecimal(decOf(t, "2.25"))},
		{types.NewDecimal(decOf(t, "-0.75"))},
	}
	assertMergeLaw(t, func() api.Accumulator { return &sumDecimalAcc{} }, rows, 1)
}

func TestMergeLawSumPosition(t *testing.T) {
	pos := func(n, cur string) types.Value {
		return types.NewPosition(inventory.Position{Units: ast.Amount{Number: decOf(t, n), Currency: cur}})
	}
	rows := [][]types.Value{
		{pos("10", "USD")}, {pos("5", "JPY")}, {pos("-4", "USD")}, {pos("1", "JPY")},
	}
	assertMergeLaw(t, func() api.Accumulator {
		return &sumPositionAcc{inv: inventory.NewInventory()}
	}, rows, 2)
}

func TestMergeLawMinMax(t *testing.T) {
	rows := [][]types.Value{
		{types.NewInt(5)}, {types.NewInt(-3)}, {types.Null(types.Int)}, {types.NewInt(9)}, {types.NewInt(1)},
	}
	assertMergeLaw(t, func() api.Accumulator { return &minMaxAcc{out: types.Int, dir: -1} }, rows, 2)
	assertMergeLaw(t, func() api.Accumulator { return &minMaxAcc{out: types.Int, dir: +1} }, rows, 2)
}

func TestMergeLawFirstLast(t *testing.T) {
	rows := [][]types.Value{
		{types.Null(types.Date)},
		{types.NewDate(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		{types.NewDate(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))},
		{types.NewDate(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC))},
	}
	assertMergeLaw(t, func() api.Accumulator { return &edgeAcc{out: types.Date} }, rows, 2)             // first
	assertMergeLaw(t, func() api.Accumulator { return &edgeAcc{out: types.Date, last: true} }, rows, 2) // last
}
