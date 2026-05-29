// Package sprout registers the non-standard BQL built-in function library
// into the global [github.com/yugui/go-beancount/pkg/query/env] registry.
//
// It is the query-side counterpart of pkg/ext/postproc/sprout: where
// pkg/query/env/std provides the functions with beanquery upstream parity,
// sprout provides extensions that have no upstream equivalent. Like std it
// is a single package whose files register their overloads from init, so a
// consumer activates the whole library with one blank import:
//
//	import _ "github.com/yugui/go-beancount/pkg/query/env/sprout"
//
// std and sprout are independent: importing one does not import the other,
// and cmd/beanquery activates both.
//
// # Scalar contract
//
// Every scalar here is pure and deterministic: it neither retains nor
// mutates its arguments.
//
// # coalesce
//
// coalesce returns its first non-NULL argument, or a typed NULL of the
// shared argument type when every argument is NULL. BQL functions are not
// variadic, so coalesce is registered as fixed-arity overloads of one
// through five arguments; within an overload all arguments share a single
// type. Overloads exist for Int, Decimal, String, Amount, Bool, Date, Set,
// and Dict.
package sprout
