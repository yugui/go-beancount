// Package csvimp is the reference CSV/TSV importer. It registers an
// [importer.Factory] under the kind "csv"; each factory call produces
// one fully-configured [*Importer] for a single CSV/TSV shape.
//
// # Configuration
//
// Each instance is configured at construction time via the decode callback
// supplied to the factory. The configuration body uses grouped sub-tables
// per logical field plus an [[amount]] array of tables:
//
//	match       = "mybank.*\\.csv$"   # optional path regex
//	delimiter   = ","                  # default ","; one rune only
//	skip_lines  = 1                    # banner lines before the header; default 0
//
//	[date]
//	col    = "Date"
//	format = "2006-01-02"              # must include year
//
//	[account]
//	col     = "AccountName"            # optional; per-row source column
//	default = "Assets:Checking"        # optional fallback
//
//	# Configuring [account.map] switches account resolution into strict
//	# mode: an [account].col cell whose value is absent from this map
//	# emits DiagUnmappedAccount and skips the row. With no map (or with
//	# the map omitted), cell values are used verbatim.
//	[account.map]
//	"chk-1234" = "Assets:Checking"
//	"sav-5678" = "Assets:Savings"
//
//	[payee]
//	col = "Payee"                      # optional
//
//	[payee.map]                        # optional translation
//	"AMZN MKTPL" = "Amazon"
//
//	[currency]
//	col     = "Currency"               # optional
//	default = "JPY"                    # optional
//
//	[currency.map]                     # optional translation
//	"¥" = "JPY"
//	"$" = "USD"
//
//	[narration]
//	cols      = ["Description", "Memo"]
//	separator = " / "
//
//	[narration.map]                    # optional per-cell translation
//	"ATM W/D" = "ATM withdrawal"
//
//	# At least one [[amount]] entry is required. Use one entry for a
//	# single signed column, or multiple entries (with negate as needed)
//	# for a debit/credit split.
//	[[amount]]
//	col    = "Amount"
//	negate = false
//
// A debit/credit-split shape uses two amount entries:
//
//	[[amount]]
//	col    = "Withdrawal"
//	negate = true
//
//	[[amount]]
//	col    = "Deposit"
//	negate = false
//
// At least one of [account].col / [account].default must be set;
// similarly for [currency]. When [account].col is configured without an
// [account.map], cell values are used verbatim; configuring an
// [account.map] switches account resolution into strict mode (see
// "Resolution priorities" below). [account].default and every value in
// [account.map] are validated against the beancount account grammar at
// configure time.
//
// When any of [account].col, [payee].col, [currency].col, or every
// column in [narration].cols is configured, the column is required for
// Identify to return true and for Extract to succeed without
// DiagMissingColumn. Files whose header lacks one of these columns are
// skipped by Dispatch even when [account].default (etc.) could in
// principle process every row.
//
// A translation map cannot be configured without its corresponding
// source column: [account.map] requires [account].col, [payee.map]
// requires [payee].col, [currency.map] requires [currency].col, and
// [narration.map] requires a non-empty [narration].cols. The factory
// rejects such configurations at configure time.
//
// Multiple CSV shapes (e.g. one per bank account) are handled by
// constructing separate [*Importer] instances via the factory and
// registering them in an [importer.Registry]; [importer.Dispatch]
// walks instances in declaration order.
//
// # Resolution priorities
//
// Each row is resolved field-by-field. For every field the first rule
// that yields a non-empty value wins.
//
// Account:
//  1. Hints["account"] (CLI/caller override).
//  2. [account].col cell when non-blank:
//     - with [account.map] set: strict lookup; a miss emits
//     DiagUnmappedAccount and skips the row.
//     - without [account.map]: cell value is used verbatim.
//  3. [account].default.
//  4. Otherwise: DiagMissingAccount.
//
// Currency:
//  1. [currency].col cell when non-blank: [currency.map] lookup; on
//     miss the trimmed cell value is used verbatim. Unlike account
//     resolution, a currency map miss is never an error.
//  2. [currency].default.
//  3. Otherwise: DiagMissingCurrency.
//
// Payee:
//  1. [payee].col cell when non-blank: [payee.map] lookup or pass-through.
//     A [payee.map] entry mapped to "" suppresses the payee for that
//     value (the transaction's payee field is left blank).
//  2. Otherwise "".
//
// Narration:
//
// For each [narration].cols entry: trim the cell, apply [narration.map]
// (lookup or pass-through) when set, and skip blanks. A [narration.map]
// entry mapped to "" drops the cell from the concatenation (useful for
// masking noisy columns). The surviving values are joined with
// [narration].separator.
//
// # Diagnostics
//
// All diagnostics carry [ast.Error] severity. csvimp emits one diagnostic
// per failing row and skips that row; it never aborts the whole Extract
// on a per-row problem.
//
//   - DiagBadDate           — date cell did not parse under [date].format.
//   - DiagBadAmount         — an amount cell held a non-blank, unparseable value.
//   - DiagAllBlankAmount    — every amount cell on the row was blank.
//   - DiagMissingCurrency   — neither [currency].col cell nor [currency].default yielded a value.
//   - DiagMissingAccount    — no account source produced a value.
//   - DiagUnmappedAccount   — [account].col cell missing from [account.map] in strict mode.
//   - DiagMissingColumn     — a required column was absent from the header at Extract time.
//
// # Identity metadata
//
// Every emitted Transaction is stamped with the metadata key
// "csvimp-rowhash": the first 8 bytes of SHA-256 over
// instance-name || RS || trimmed-field0 || US || trimmed-field1 || …,
// hex-encoded as 16 lowercase characters. Callers using
// pkg/distribute/dedup may list this key in eqKeys for cross-run
// deduplication. The hash is computed over the raw CSV row before any
// translation map is applied, so toggling a map does not invalidate
// existing rowhashes.
//
// # Hints
//
// "account" — primary account override. When non-empty it takes
// precedence over every shape-side account resolution path.
//
// # Concurrency
//
// An Importer's state is frozen at construction. Identify and Extract
// are safe for concurrent invocation on the same value with no external
// synchronisation.
package csvimp

import "github.com/yugui/go-beancount/pkg/importer"

func init() {
	importer.RegisterFactory("csv", importer.FactoryFunc(newImporter))
}
