// Package types defines the BQL value and type model for the query
// engine: a sealed [Value] interface with one concrete variant per BQL
// value kind, an explicit NULL model, and a documented total-order
// comparison ([Value.Compare]) that ORDER BY, min/max, and first/last
// bind to.
//
// The package is self-contained: it knows nothing about tables, parsing,
// or execution. It depends only on [github.com/yugui/go-beancount/pkg/ast],
// [github.com/yugui/go-beancount/pkg/inventory], and
// [github.com/cockroachdb/apd/v3].
//
// # Value kinds
//
// Each kind is named by a [Type] constant and constructed by a single
// NewXxx constructor returning [Value]:
//
//	Bool       NewBool
//	Int        NewInt        (int64)
//	Decimal    NewDecimal    (apd.Decimal, by value, exact)
//	String     NewString
//	Date       NewDate       (time.Time)
//	Amount     NewAmount     (ast.Amount)
//	Position   NewPosition   (inventory.Position)
//	Inventory  NewInventory  (*inventory.Inventory)
//	Interval   NewInterval   (years/months/days calendar offset)
//	Set        NewSet        (string elements; tags/links)
//	Dict       NewDict        (string-keyed Value; metadata)
//
// [Entry] is a reserved kind for a future directive-as-value variant. It
// has a stable ordinal and a String form but is never constructed in this
// step.
//
// # NULL
//
// NULL is a single conceptual notion, but every NULL carries a [Type] so
// that an all-NULL column still orders and types deterministically. Build
// one with [Null]; [Value.IsNull] reports true and the typed accessors
// (AsBool, AsInt, …) report ok=false.
//
// # Immutability and concurrency
//
// Values are immutable after construction and safe to share across
// goroutines for read. Constructors that take a pointer, slice, or map
// ([NewInventory], [NewSet], [NewDict]) take a private copy or otherwise
// guarantee the caller cannot observe later mutation through the returned
// Value; see each constructor's contract.
//
// # Exactness
//
// Decimal values are compared and rendered exactly via apd; the package
// never converts a decimal to float.
package types
