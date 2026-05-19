// Package csvimp is the reference CSV/TSV importer. It registers a
// process-global [*Importer] under the canonical short name "csv" and
// under its Go fully-qualified package path; either lookup returns the
// same instance.
//
// # Configuration
//
// The importer is encoding-agnostic: its [*Importer.Configure] method
// takes a decoder closure supplied by the caller (typically the CLI's
// TOML loader). The configuration schema is a top-level table named
// shape whose entries are themselves tables keyed by shape name:
//
//	[shape.mybank]
//	match            = "mybank.*\\.csv$"   # optional path regex
//	delimiter        = ","                  # or "\t"; default ","
//	skip_lines       = 1                    # lines before the header; default 0
//	date_col         = "Date"
//	date_format      = "2006-01-02"
//	payee_col        = "Payee"              # optional
//	currency_col     = "Currency"           # optional
//	default_currency = "JPY"                # optional
//	narration_cols      = ["Description", "Memo"]
//	narration_separator = " / "
//	account          = "Assets:MyBank"      # optional; overridden by Hints["account"]
//
//	# Single signed column: one entry with negate=false.
//	[[shape.mybank.amount]]
//	col    = "Amount"
//	negate = false
//
// A debit/credit-split shape uses two amount entries with opposite
// negate flags so the sum is a single signed amount on the emitted
// posting:
//
//	[[shape.mybank.amount]]
//	col    = "Withdrawal"
//	negate = true
//
//	[[shape.mybank.amount]]
//	col    = "Deposit"
//	negate = false
//
// Shapes are walked in lexicographic order of shape name during
// [*Importer.Identify].
//
// # Identity metadata
//
// Every emitted Transaction is stamped with the metadata key
// "csvimp-rowhash": the first 8 bytes of SHA-256 over
// shape-name || RS || trimmed-field0 || US || trimmed-field1 || US || …,
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
// Individual method calls (Configure, Identify, Extract) are
// goroutine-safe via an internal mutex. An Identify→Extract sequence on
// the same Input is not atomic: a concurrent Configure or Identify with a
// different Input may invalidate the cached shape selection between the
// calls. Callers that depend on the cache must serialise externally.
package csvimp

import "github.com/yugui/go-beancount/pkg/importer"

func init() {
	imp := &Importer{}
	importer.Register("csv", imp)
	importer.Register("github.com/yugui/go-beancount/pkg/importer/std/csvimp", imp)
}
