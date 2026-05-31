package api

import (
	"github.com/yugui/go-beancount/pkg/query/price"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// Flavor classifies how the engine evaluates a function overload. It
// selects which implementation field of a [Function] is set.
//
// There are two flavors, matching the two genuine execution modes: a
// per-row [ScalarFlavor] and a per-group [AggregatorFlavor]. Whether a
// scalar reads the query-wide context is an orthogonal property, not a
// flavor: every [Scalar] receives the context, and a context-free function
// is adapted with [Pure].
type Flavor int

const (
	// ScalarFlavor is a function evaluated once per row. Its implementation
	// is in [Function.Scalar].
	ScalarFlavor Flavor = iota

	// AggregatorFlavor is a per-group accumulator used by GROUP BY. Its
	// implementation is in [Function.Aggregator].
	AggregatorFlavor
)

// Scalar implements a [ScalarFlavor] overload, evaluated once per row. ctx is
// the query-wide, immutable context (price map, etc.); a context-free
// implementation ignores it, and [Pure] adapts one that does not take it.
//
// A Scalar must not retain or mutate ctx, args, or args' elements, and it
// must return the same result for equal inputs. args has the arity and types
// of the overload's [Function.In]. A non-nil error reports a per-row
// evaluation failure (e.g. a malformed regular expression) and never panics
// on bad data.
type Scalar func(ctx *price.QueryContext, args []types.Value) (types.Value, error)

// Pure adapts a context-free implementation into a [Scalar] by ignoring the
// context. It is the common case: most scalar functions do not consult the
// query context.
func Pure(f func(args []types.Value) (types.Value, error)) Scalar {
	return func(_ *price.QueryContext, args []types.Value) (types.Value, error) {
		return f(args)
	}
}

// Accumulator folds a stream of rows into one aggregate result. It is the
// per-group state of an [AggregatorFlavor] overload, created fresh by a
// [NewAccumulator].
//
// Mergeability is the binding contract: for any partition of the input
// rows into groups, accumulating each group into its own Accumulator and
// then combining them with Merge yields the same Result as folding every
// row into a single Accumulator ("Add-then-Merge ≡ Add-all"). Add and
// Merge are therefore associative and commutative with respect to the
// final Result, which lets a future parallel executor fold partitions
// independently and merge their partials with no rework.
type Accumulator interface {
	// Add folds one row's argument values into the accumulator. args has
	// the arity and types of the overload's [Function.In]. It must not
	// retain or mutate args or its elements. A non-nil error reports a
	// fold-time failure.
	Add(args []types.Value) error

	// Merge folds a partial computed by another Accumulator of the same
	// overload into this one. other is consumed for its state only and is
	// not retained. A non-nil error reports a merge-time failure.
	Merge(other Accumulator) error

	// Result finalizes the aggregate. It is called once after all Add and
	// Merge calls; behavior of a subsequent Add, Merge, or Result is
	// unspecified. A non-nil error reports a finalization failure.
	Result() (types.Value, error)
}

// NewAccumulator returns a fresh, zero-state [Accumulator] for one group
// or partition. Each call yields an independent accumulator that shares no
// mutable state with any other.
type NewAccumulator func() Accumulator

// Function describes one overload of a polymorphic BQL function.
//
// Name is matched case-insensitively; In is the ordered parameter type
// signature that distinguishes this overload from others of the same name;
// Out is the result type. Flavor selects the implementation: exactly one
// of Scalar (for [ScalarFlavor]) or Aggregator (for [AggregatorFlavor]) is
// non-nil, and it matches Flavor.
type Function struct {
	Name       string
	In         []types.Type
	Out        types.Type
	Flavor     Flavor
	Scalar     Scalar
	Aggregator NewAccumulator
}
