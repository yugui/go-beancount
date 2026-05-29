// Package api defines the stable descriptor for BQL query functions.
//
// A BQL function is polymorphic: one name may carry several overloads
// distinguished by their ordered input type signature, resolved once at
// compile time. The [Function] descriptor names one such overload — its
// signature, result type, flavor, and implementation. The [Scalar],
// [Accumulator], and [NewAccumulator] types are the two function flavors
// the lean engine evaluates ([PassContextFlavor] is reserved for the
// deferred price/directive functions).
//
// This package depends only on pkg/query/types so that a future
// pkg/query/goplug can compile against the descriptor without pulling in
// the registry. Registration and overload resolution live in
// pkg/query/env, mirroring the pkg/ext/postproc api-vs-runner split.
package api
