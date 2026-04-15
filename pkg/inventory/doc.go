// Package inventory implements lot-based inventory tracking for Beancount
// ledgers.
//
// A [Position] is a pair of units (an [ast.Amount]) and an optional cost
// [Lot]. Positions with a non-nil [Cost] track commodity holdings at a
// specific acquisition cost; positions with a nil [Cost] represent plain
// cash or fungible commodities that do not require lot bookkeeping. A
// collection of positions for one or more accounts forms an
// [Inventory].
//
// # Scope
//
// The package supports the Beancount booking methods STRICT, FIFO, LIFO
// and NONE. Augmentations merge into equal existing lots; reductions
// select lots by a [CostMatcher] subject to the account's booking method.
// The AVERAGE booking method is recognised but rejected with
// [CodeInvalidBookingMethod]; support for it is not yet implemented.
//
// # Streaming-first design
//
// The primary API is Reducer.Walk(visitor): the ledger is replayed once
// and the visitor is invoked for each transaction with deep-copied
// before/after snapshots of the accounts that transaction touched.
// Memory cost is O(1) in the size of the ledger, which lets this package
// scale to multi-million-directive ledgers without retaining per-txn
// snapshots.
//
// For one-off trouble-shooting — the equivalent of upstream Beancount's
// `bean-doctor context` command — the Reducer also exposes an
// Inspect(txn) convenience that re-runs a walk up to the requested
// transaction and returns a captured before/after view. Inspect is
// O(N) per call and is intended for interactive use, not for scanning
// an entire ledger.
//
// # Beancount parity
//
// The resolved [Cost] type mirrors the upstream Beancount position model:
// a (Number, Currency, Date, Label) tuple. Augmenting postings that
// share an equal [Cost] merge into a single lot; reducing postings use
// a [CostMatcher] built from the raw [ast.CostSpec] plus any
// cost-currency hint derived from the posting's price annotation.
//
// # Package contents
//
// The package provides the foundational value types [Cost] and
// [Position] together with the [Error]/[Code] diagnostic model, the
// [Inventory] container, the [CostMatcher] used to select lots on
// reducing postings, the per-transaction booking algorithm, and the
// [Reducer] that drives them across a ledger.
package inventory
