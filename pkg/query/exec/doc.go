// Package exec compiles a parsed BQL SELECT into an immutable, executable
// plan and runs it over a virtual table.
//
// # Compilation
//
// [Compile] type-checks a [github.com/yugui/go-beancount/pkg/query/parser.Select]
// against a virtual table, resolves columns and function overloads, and
// returns a [Compiled] plan whose output schema ([Compiled.Columns]) is fixed
// at compile time. Compilation failures (unknown column or table, operator
// type mismatch, missing or ambiguous overload, a misplaced or nested
// aggregate, a non-boolean predicate) are returned as positioned errors, never
// panics.
//
// # Table selection and FROM ≡ WHERE
//
// The default table is postings; entries is also available. A FROM clause that
// is exactly one bare identifier naming a catalog table selects that table and
// contributes no filter. Any other FROM content is a filter expression over
// postings, AND-merged with WHERE against the same columns into one row
// predicate. There is no entry-vs-posting namespace: SELECT … FROM expr and
// SELECT … WHERE expr over postings are identical. A bare-identifier FROM that
// names neither a table nor a column is a compile error.
//
// # Operators
//
// Arithmetic (+ - * / %) requires numeric operands with Int → Decimal
// widening: Int with Int yields Int for + - * %, division always yields
// Decimal, and any Decimal operand yields Decimal. Comparison (= != < <= > >=)
// yields Bool via the total order in
// [github.com/yugui/go-beancount/pkg/query/types.Value.Compare]; operands must
// share a type or be a widening numeric pair. The regex operator ~ matches a
// String against a String pattern; a literal pattern is compiled once at
// compile time, a non-literal per evaluation, and a malformed pattern is a
// runtime query error, never a panic. Boolean AND/OR/NOT use SQL three-valued
// logic. IN tests membership against a list literal (by Compare-equality) or a
// set value (string membership).
//
// # NULL
//
// NULL is three-valued: a comparison or arithmetic operation with a NULL
// operand yields NULL, and a row passes the predicate only when it evaluates to
// TRUE (NULL and FALSE are excluded). The NULL literal is untyped: it carries
// the static type Invalid, which operators and type checks treat as compatible
// with any operand; the result takes the sibling operand's type, or
// Null(Invalid) when both sides are untyped.
//
// # meta sugar
//
// meta('key'[, default]) is rewritten at compile time to getitem(meta,
// 'key'[, default]): the selected table's meta (dict) column is prepended as
// the first argument and getitem is resolved via the function registry. The
// getitem function itself is registered elsewhere (the standard library or, in
// tests, directly).
//
// # Aggregation
//
// A query is in aggregate mode when it has a GROUP BY clause or any aggregate
// target. Each distinct aggregate call is an ordered slot with its own
// per-group accumulator. Rows passing the predicate are folded into groups
// keyed by the GROUP BY values (first-seen group order is preserved before any
// ORDER BY); each group then projects one output row, with aggregate
// references resolving to finalized results and group-key columns read from a
// representative row. Aggregate mode with no GROUP BY produces exactly one
// group, hence one output row even over zero input rows.
//
// The aggregate-mixing check matches grouped columns by the bare column name
// referenced in GROUP BY: a target or ORDER BY column reference must be a
// grouped column or sit inside an aggregate call, else it is a compile error.
// A non-trivial grouped expression (for example GROUP BY year(date)) does not
// cover the bare columns it derives from; this is the lean rule.
//
// # Output ordering
//
// The pipeline is predicate filter → (group+aggregate | row projection) →
// DISTINCT → ORDER BY → LIMIT. DISTINCT removes duplicate output rows by true
// value equality (every column compares 0, NULL == NULL). Without an ORDER BY
// clause the output order is the deterministic first-seen scan order (table
// row order for scalar queries, first-seen group order for aggregate queries);
// it is otherwise unspecified and callers must add ORDER BY for a guaranteed
// order.
//
// # Concurrency
//
// A [Compiled] is immutable; [Compiled.Run] allocates all per-execution state
// locally and never mutates the plan or the ledger, so one plan may be run
// concurrently from many goroutines over one shared immutable ledger with no
// locking (Decision 6). run.go documents the single seam at which a future
// parallel executor would partition the input scan.
package exec
