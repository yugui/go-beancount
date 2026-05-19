// Package importerutil offers small, pure per-directive transforms that
// importer authors call from inside their Extract methods. Nothing in
// pkg/importer or pkg/importer/hook invokes them; they exist solely to
// be composed by importer authors.
//
//   - BalanceWith adds a counterpart posting to a single-posting
//     transaction so it balances. No-op on non-Transaction directives,
//     already-balanced transactions, and nil input.
//
//   - StampMetadata sets a string metadata key on any directive that
//     carries a Meta field. Idempotent: re-stamping with the same value
//     costs no allocation. No-op on *Option, *Plugin, *Include, and nil
//     input.
//
// Every function is pure with no global state; concurrent calls are safe.
package importerutil
