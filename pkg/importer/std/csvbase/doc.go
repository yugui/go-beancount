// Package csvbase provides an [importer.Importer] for CSV/TSV files.
//
// [Driver] implements [importer.Importer]; callers supply per-row logic via
// [RowMapper] ([MapperFunc] adapts a plain function). [RowHash] enables
// idempotency stamping. [Gate], [DefaultGate], [PathMatch], and [AllGates]
// control which files the Driver claims in Identify. Underlying CSV/TSV
// parsing is handled by csvkit.
package csvbase
