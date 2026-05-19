// Package csvimp is the reference CSV/TSV importer. It registers an
// [importer.Factory] under the kind "csv"; each factory call produces
// one fully-configured [*Importer] for a single CSV/TSV shape.
//
// # Configuration
//
// Each instance is configured at construction time via the decode callback
// supplied to the factory. The configuration body carries a single shape
// description directly at the top level (no enclosing [shape.<name>] table):
//
//	delimiter        = ","                  # or "\t"; default ","
//	skip_lines       = 1                    # lines before the header; default 0
//	date_col         = "Date"
//	date_format      = "2006-01-02"
//	match            = "mybank.*\\.csv$"    # optional path regex
//	payee_col        = "Payee"              # optional
//	currency_col     = "Currency"           # optional
//	default_currency = "JPY"                # optional
//	narration_cols      = ["Description", "Memo"]
//	narration_separator = " / "
//	account          = "Assets:MyBank"      # optional; overridden by Hints["account"]
//
//	# Single signed column: one entry with negate=false.
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
// Multiple CSV shapes (e.g. one per bank account) are handled by
// constructing separate [*Importer] instances via the factory and
// registering them in an [importer.Registry]; [importer.Dispatch]
// walks instances in declaration order.
//
// # Identity metadata
//
// Every emitted Transaction is stamped with the metadata key
// "csvimp-rowhash": the first 8 bytes of SHA-256 over
// instance-name || RS || trimmed-field0 || US || trimmed-field1 || US || …,
// hex-encoded as 16 lowercase characters. Callers using
// pkg/distribute/dedup may list this key in eqKeys for cross-run
// deduplication.
//
// # Hints
//
// "account" — primary account override. When non-empty it takes
// precedence over the shape's account field.
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
