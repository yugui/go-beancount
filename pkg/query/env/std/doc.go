// Package std registers the lean BQL built-in function library into the
// global [github.com/yugui/go-beancount/pkg/query/env] registry.
//
// Unlike pkg/ext/postproc/std, which is a content-free umbrella over
// self-contained subpackages, this is a single package whose category
// files (date, str, account, value, getitem, aggregate) each register
// their overloads from an init function. A consumer activates the whole
// library with one blank import:
//
//	import _ "github.com/yugui/go-beancount/pkg/query/env/std"
//
// After this import, the date/string/account/extractor scalars, the
// getitem dict accessor (which backs meta lookups), and the
// count/sum/min/max/first/last aggregators are resolvable by the
// compiler. cmd/beanquery (a later step) is the intended activation point;
// no separate umbrella import exists.
//
// # Scalar contract
//
// Every scalar overload here is pure and deterministic: it neither retains
// nor mutates its arguments. NULL propagates — a NULL argument yields a
// typed NULL result of the overload's output kind — with one documented
// exception: getitem over a NULL dict returns NULL but a present key bound
// to a typed NULL returns that NULL as-is. Decimal arithmetic is exact
// (apd), never float. Errors are returned, never panicked (a malformed
// regex passed to grep is the only fallible scalar).
//
// # getitem is the only dynamically typed function
//
// getitem(dict, key) and getitem(dict, key, default) declare an output
// type of [types.Invalid] because a metadata value's kind is known only at
// runtime. The engine treats an Invalid-typed operand as compatible with
// any type (the NULL-literal rule), and scalar evaluation passes the
// stored value through with its own runtime type. The optional default is
// restricted to a String in the lean subset; this is sufficient for the
// meta('k') / meta('k','fallback') sugar, which the compiler rewrites to
// getitem(meta, 'k') / getitem(meta, 'k', 'fallback').
//
// # Aggregator contract
//
// Each aggregator implements the mergeable [api.Accumulator] law
// (Add-then-Merge ≡ Add-all), so the deferred parallel executor can fold
// partitions with no rework. count and min/max/first/last are registered
// once per applicable value type using a single type-generic accumulator;
// sum has int, decimal, and position overloads. NULL arguments are skipped
// by every aggregator. Over an empty group, sum(int)/sum(decimal) return 0
// and sum(position) returns an empty inventory, while count returns 0 and
// min/max/first/last return NULL.
//
// # Documented beanquery-parity divergences (follow-ups)
//
// The lean engine intentionally simplifies a few return shapes relative to
// beanquery; full parity is deferred and should be recorded in the Step 8
// architecture doc:
//
//   - weekday returns the English weekday name (e.g. "Monday") via
//     time.Weekday.String(); beanquery returns an integer index.
//   - quarter returns an int 1..4; beanquery returns a "YYYY-Qn" string.
//   - yearmonth returns a "YYYY-MM" string; beanquery returns a
//     month-truncated date.
package std
