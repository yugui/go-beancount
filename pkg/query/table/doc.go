// Package table provides the BQL query engine's data sources: virtual
// tables over a read-only [*ast.Ledger]. Each table exposes named,
// statically-typed [Column]s and a lazy, re-runnable sequence of opaque
// row handles ([Table.Rows]). A column's accessor translates the data
// reachable from a row handle into a [types.Value], yielding a typed NULL
// where the underlying field is absent.
//
// Two tables ship in the lean engine:
//
//   - [Postings] (the default table): one row per posting of every
//     transaction; non-transaction directives are skipped.
//   - [Entries]: one row per directive over the full directive stream;
//     columns a directive type does not carry yield typed NULL.
//
// # Read-only concurrency
//
// A [Table] is immutable after construction. [Table.Rows] returns a fresh
// iterator on each call and shares no mutable state; column accessors are
// pure functions of the row handle and never mutate the ledger. Therefore
// many goroutines may iterate and read one table built over one shared
// immutable ledger with no locking.
//
// # Exactness
//
// Numeric coercions are exact (apd decimals); the package never converts a
// decimal to float. The only division performed is the total-price (`@@`)
// to per-unit price reduction in the postings `price` column, done with an
// apd context whose precision matches pkg/inventory's.
//
// # Dependencies
//
// table depends only on [github.com/yugui/go-beancount/pkg/query/types],
// [github.com/yugui/go-beancount/pkg/ast],
// [github.com/yugui/go-beancount/pkg/inventory], and the standard library.
// It does not import the parser, registry, or executor packages.
package table
