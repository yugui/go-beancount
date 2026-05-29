package api_test

import (
	"fmt"
	"testing"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// sumAccumulator folds Int arguments into a running total, exercising the
// mergeable contract documented on api.Accumulator.
type sumAccumulator struct{ total int64 }

func newSumAccumulator() api.Accumulator { return &sumAccumulator{} }

func (a *sumAccumulator) Add(args []types.Value) error {
	if len(args) != 1 {
		return fmt.Errorf("sum: want 1 arg, got %d", len(args))
	}
	n, ok := types.AsInt(args[0])
	if !ok {
		return fmt.Errorf("sum: arg is not a non-null int")
	}
	a.total += n
	return nil
}

func (a *sumAccumulator) Merge(other api.Accumulator) error {
	o, ok := other.(*sumAccumulator)
	if !ok {
		return fmt.Errorf("sum: cannot merge %T", other)
	}
	a.total += o.total
	return nil
}

func (a *sumAccumulator) Result() (types.Value, error) {
	return types.NewInt(a.total), nil
}

func addAll(t *testing.T, acc api.Accumulator, rows []int64) {
	t.Helper()
	for _, n := range rows {
		if err := acc.Add([]types.Value{types.NewInt(n)}); err != nil {
			t.Fatalf("Add(%d): %v", n, err)
		}
	}
}

func result(t *testing.T, acc api.Accumulator) int64 {
	t.Helper()
	v, err := acc.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	n, ok := types.AsInt(v)
	if !ok {
		t.Fatalf("Result is not an int: %v", v)
	}
	return n
}

func TestNewAccumulator_StartsAtZeroState(t *testing.T) {
	var newAcc api.NewAccumulator = newSumAccumulator
	if got := result(t, newAcc()); got != 0 {
		t.Errorf("fresh accumulator Result = %d, want 0", got)
	}

	// Independence: state in one fresh accumulator does not leak into the
	// next from the same factory.
	first := newAcc()
	addAll(t, first, []int64{5})
	if got := result(t, newAcc()); got != 0 {
		t.Errorf("second fresh accumulator Result = %d, want 0", got)
	}
}

func TestAccumulator_AddThenMergeEqualsAddAll(t *testing.T) {
	var newAcc api.NewAccumulator = newSumAccumulator
	left := []int64{1, 2, 3}
	right := []int64{10, 20}

	whole := newAcc()
	addAll(t, whole, append(append([]int64{}, left...), right...))

	partA := newAcc()
	addAll(t, partA, left)
	partB := newAcc()
	addAll(t, partB, right)
	if err := partA.Merge(partB); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got, want := result(t, partA), result(t, whole); got != want {
		t.Errorf("Add-then-Merge = %d, Add-all = %d; want equal", got, want)
	}
}

func TestFunction_DescribesScalarOverload(t *testing.T) {
	fn := api.Function{
		Name:   "neg",
		In:     []types.Type{types.Int},
		Out:    types.Int,
		Flavor: api.ScalarFlavor,
		Scalar: func(args []types.Value) (types.Value, error) {
			n, ok := types.AsInt(args[0])
			if !ok {
				return nil, fmt.Errorf("neg: arg is not an int")
			}
			return types.NewInt(-n), nil
		},
	}

	got, err := fn.Scalar([]types.Value{types.NewInt(7)})
	if err != nil {
		t.Fatalf("Scalar: %v", err)
	}
	if n, _ := types.AsInt(got); n != -7 {
		t.Errorf("neg(7) = %v, want -7", got)
	}
}
