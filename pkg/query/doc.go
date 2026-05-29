// Package query is the public facade of the BQL query engine. It parses,
// compiles, and runs a Beancount Query Language SELECT over an
// already-loaded, validated, booked, pad-resolved
// [github.com/yugui/go-beancount/pkg/ast.Ledger].
//
// [Compile] turns query text into an immutable [Compiled] plan whose output
// schema is known before execution ([Compiled.Columns]); [Compiled.Run]
// executes it under a context and returns a [Result] of typed rows; [Query]
// is the one-call convenience form. The engine never reloads, re-books, or
// re-validates the ledger.
//
// A [Compiled] and its ledger are immutable and read-only during execution,
// so one plan may be run concurrently from many goroutines over one shared
// ledger with no locking. Without an ORDER BY clause, row order is the
// deterministic scan order but otherwise unspecified; add ORDER BY for a
// guaranteed order.
//
// Function overloads (scalar functions and aggregators) are resolved at
// compile time against the registry in
// [github.com/yugui/go-beancount/pkg/query/env]; the standard library
// registers them via a blank import.
package query
