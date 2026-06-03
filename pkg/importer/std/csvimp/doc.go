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
//	encoding    = "Shift_JIS"          # optional IANA charset; default UTF-8/pass-through
//
//	# Optional numeric parsing rules applied to every [[amount]] cell.
//	# Absent, amounts parse exactly as apd does (commas rejected, '.'
//	# decimal point).
//	[number]
//	thousands_sep = ","                # stripped before parsing ("1,234" -> 1234)
//	decimal_sep   = "."                # normalised to '.' (e.g. "," for European decimals)
//	placeholders  = ["-"]              # cells equal to these parse as "no value", not an error
//
//	[date]
//	col    = "Date"
//	format = "2006-01-02"              # must include year
//
//	[account]
//	# col accepts a single column name or a list of columns. When a
//	# list is given, the trimmed cells are joined with separator (blank
//	# cells dropped) to form the key consulted against [account.map] or
//	# used verbatim.
//	col       = "AccountName"          # or ["AcctType", "AcctID"]
//	separator = "-"                    # used only when col is a list
//	default   = "Assets:Checking"      # optional fallback
//
//	# Configuring [account.map] switches account resolution into strict
//	# mode: an [account].col cell whose value is absent from this map
//	# emits DiagUnmappedAccount and skips the row. With no map (or with
//	# the map omitted), cell values are used verbatim.
//	[account.map]
//	"chk-1234" = "Assets:Checking"
//	"sav-5678" = "Assets:Savings"
//
//	# Optional balancing posting. When [counter_account] is configured,
//	# each emitted Transaction carries a second posting whose Amount is
//	# the primary posting's amount negated. Same schema as [account],
//	# minus the Hints["account"] override.
//	[counter_account]
//	col = "Category"                   # or ["Category", "Subcategory"]
//	separator = ":"
//	default   = "Equity:Unknown"       # optional fallback
//
//	[counter_account.map]
//	"Food" = "Expenses:Food"
//	"Rent" = "Expenses:Housing:Rent"
//
//	[payee]
//	# col accepts a single column or a list joined by separator before
//	# [payee.map] lookup.
//	col = "Payee"                      # optional
//
//	[payee.map]                        # optional translation
//	"AMZN MKTPL" = "Amazon"
//
//	[currency]
//	col     = "Currency"               # optional; scalar only
//	default = "JPY"                    # optional
//
//	[currency.map]                     # optional translation
//	"¥" = "JPY"
//	"$" = "USD"
//
//	[narration]
//	col       = ["Description", "Memo"] # scalar or list
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
// similarly for [currency]. [counter_account] is entirely optional —
// omitting it preserves the historical single-posting behavior. When
// [account].col is configured without an [account.map], cell values are
// used verbatim; configuring an [account.map] switches account
// resolution into strict mode (see "Resolution priorities" below).
// [account].default and every value in [account.map] are validated
// against the beancount account grammar at configure time. The same
// rules apply to [counter_account].
//
// When any of [account].col, [counter_account].col, [payee].col,
// [currency].col, or every column in [narration].col is configured, the
// column is required for Identify to return true and for Extract to
// succeed without DiagMissingColumn. Files whose header lacks one of
// these columns are skipped by Dispatch even when [account].default
// (etc.) could in principle process every row.
//
// A translation map cannot be configured without its corresponding
// source column: [account.map] requires [account].col, [payee.map]
// requires [payee].col, [currency.map] requires [currency].col,
// [narration.map] requires a non-empty [narration].col, and
// [counter_account.map] requires [counter_account].col. The factory
// rejects such configurations at configure time.
//
// Multiple CSV shapes (e.g. one per bank account) are handled by
// constructing separate [*Importer] instances via the factory and
// registering them in an [importer.Registry]; [importer.Dispatch]
// walks instances in declaration order.
//
// # Input encoding
//
// When encoding is set, the file's bytes are decoded to UTF-8 before CSV
// parsing. Any name resolvable by [ianaindex.IANA] is accepted, including
// registry aliases (e.g. "MS_Kanji" for Shift_JIS). Unset is equivalent
// to passing the bytes through unchanged, which works for UTF-8 and any
// ASCII-compatible single-byte encoding.
//
// A leading UTF-8 byte-order mark is always stripped before parsing, so a
// BOM never contaminates the first header column name.
//
// # Number format
//
// The optional [number] block tunes how every [[amount]] cell is parsed.
// thousands_sep is removed before parsing; decimal_sep (when not ".") is
// normalised to "."; and any cell equal to a placeholders entry is treated
// as "no value" (contributing nothing to the row's amount) rather than a
// parse error. When [number] is absent, amounts parse with apd's default
// semantics, which reject embedded separators such as "1,234".
//
// # Resolution priorities
//
// Each row is resolved field-by-field. For every field the first rule
// that yields a non-empty value wins.
//
// Account:
//  1. Hints["account"] (CLI/caller override).
//  2. joined [account].col cells when non-empty:
//     - with [account.map] set: strict lookup; a miss emits
//     DiagUnmappedAccount and skips the row.
//     - without [account.map]: joined value is used verbatim.
//  3. [account].default.
//  4. Otherwise: DiagMissingAccount.
//
// Counter account (only when [counter_account] is configured):
//  1. joined [counter_account].col cells when non-empty:
//     - with [counter_account.map] set: strict lookup; a miss emits
//     DiagUnmappedCounterAccount as a warning and falls back to a
//     single posting (the row is still emitted).
//     - without [counter_account.map]: joined value is used verbatim.
//  2. [counter_account].default.
//  3. Otherwise: no second posting is emitted (soft fallback — the
//     row produces a single posting, mirroring the original
//     unbalanced behavior).
//
// Hints["account"] is never consulted for the counter account.
//
// Currency:
//  1. [currency].col cell when non-blank: [currency.map] lookup; on
//     miss the trimmed cell value is used verbatim. Unlike account
//     resolution, a currency map miss is never an error.
//  2. [currency].default.
//  3. Otherwise: DiagMissingCurrency.
//
// Payee:
//  1. joined [payee].col cells when non-empty: [payee.map] lookup or
//     pass-through. A [payee.map] entry mapped to "" suppresses the
//     payee for that value (the transaction's payee field is left
//     blank).
//  2. Otherwise "".
//
// Narration:
//
// For each [narration].col entry: trim the cell, apply [narration.map]
// (lookup or pass-through) when set, and skip blanks. A [narration.map]
// entry mapped to "" drops the cell from the concatenation (useful for
// masking noisy columns). The surviving values are joined with
// [narration].separator.
//
// # Diagnostics
//
// Most diagnostics carry [ast.Error] severity and cause csvimp to skip
// the offending row; DiagUnmappedCounterAccount carries [ast.Warning]
// and the row is still emitted (with a single posting). csvimp never
// aborts the whole Extract on a per-row problem.
//
//   - DiagBadDate                 — date cell did not parse under [date].format.
//   - DiagBadAmount               — an amount cell held a non-blank, unparseable value.
//   - DiagAllBlankAmount          — every amount cell on the row was blank.
//   - DiagMissingCurrency         — neither [currency].col cell nor [currency].default yielded a value.
//   - DiagMissingAccount          — no account source produced a value.
//   - DiagUnmappedAccount         — [account].col cell missing from [account.map] in strict mode.
//   - DiagUnmappedCounterAccount  — [counter_account].col cell missing from [counter_account.map] in strict mode (warning; row kept).
//   - DiagMissingColumn           — a required column was absent from the header at Extract time.
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
// precedence over every shape-side account resolution path. The
// counter account is unaffected.
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
