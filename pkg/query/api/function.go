package api

import "github.com/yugui/go-beancount/pkg/query/types"

// Flavor classifies how the engine evaluates a function overload. It
// selects which implementation field of a [Function] is set.
type Flavor int

const (
	// ScalarFlavor is a pure function of its argument values, evaluated
	// once per row. Its implementation is in [Function.Scalar].
	ScalarFlavor Flavor = iota

	// AggregatorFlavor is a per-group accumulator used by GROUP BY. Its
	// implementation is in [Function.Aggregator].
	AggregatorFlavor

	// PassContextFlavor is reserved for functions that read a query-wide,
	// init-time, immutable context (the deferred price/directive functions
	// such as convert/getprice/open_date). The lean engine registers none;
	// the constant fixes a stable ordinal for the seam.
	PassContextFlavor
)

// Scalar implements a [ScalarFlavor] overload. It is a pure function of
// args: it must not retain or mutate the slice or its elements, and it
// must return the same result for equal inputs. args has the arity and
// types of the overload's [Function.In]. A non-nil error reports a
// per-row evaluation failure (e.g. a malformed regular expression) and
// never panics on bad data.
type Scalar func(args []types.Value) (types.Value, error)

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
// non-nil, and it matches Flavor. A [PassContextFlavor] descriptor sets
// neither and is not evaluated by the lean engine.
type Function struct {
	Name       string
	In         []types.Type
	Out        types.Type
	Flavor     Flavor
	Scalar     Scalar
	Aggregator NewAccumulator
}
