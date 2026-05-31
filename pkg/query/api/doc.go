// Package api defines the stable descriptor for BQL query functions.
//
// A BQL function is polymorphic: one name may carry several overloads
// distinguished by their ordered input type signature, resolved once at
// compile time. The [Function] descriptor names one such overload — its
// signature, result type, flavor, and implementation. The [Scalar],
// [Accumulator], and [NewAccumulator] types implement the two function
// flavors the engine evaluates; every [Scalar] receives the query-wide
// context, and context-free functions are adapted with [Pure].
//
// This package depends only on pkg/query/types and pkg/query/price so that
// an out-of-tree goplug plugin (loaded via pkg/ext/goplug) can compile
// against the descriptor without pulling in the registry. Registration and
// overload resolution live in pkg/query/env, mirroring the pkg/ext/postproc
// api-vs-runner split.
package api
