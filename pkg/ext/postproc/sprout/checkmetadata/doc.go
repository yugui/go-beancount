// Package checkmetadata is the Go port of beansprout's check_metadata
// plugin — it diagnoses directives that are missing required metadata keys.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/check_metadata.py
//
// # Behavior
//
// The plugin reads a multi-line config string and emits one diagnostic per
// directive that is missing any of the required metadata keys.
//
// For account-based directives (open, close, balance, note, document) only
// leaf accounts are checked: an account is a leaf when no other account in
// the ledger has it as a strict ancestor (same definition as std/leafonly).
// For commodity directives every directive is checked regardless of account
// scoping.
//
// # Config DSL
//
// The config string passed to the plugin directive has the following grammar:
//
//	config     = first-line { "\n" meta-line }
//	first-line = directive-type [ " " account-prefix ]
//	directive-type = "open" | "close" | "balance" | "note" | "document" | "commodity"
//	               (case-insensitive)
//	account-prefix = beancount account name; only accounts that are equal to
//	               or under this prefix are checked
//	meta-line  = metadata-key-name
//	           (leading/trailing whitespace stripped; blank lines are ignored)
//
// Example — check all leaf Open directives for "region" and "tax_category":
//
//	plugin "beansprout.plugins.check_metadata" "open
//	  region
//	  tax_category"
//
// Example — check only leaf Open directives under Assets:Bank for "region":
//
//	plugin "beansprout.plugins.check_metadata" "open Assets:Bank
//	  region"
//
// When the config is empty or contains no metadata key lines the plugin
// returns no diagnostics.
//
// # Diagnostic codes
//
//   - "check-metadata-missing"        — a required metadata key is absent from
//     a checked directive
//   - "check-metadata-invalid-config" — the first line names an unsupported
//     directive type; the plugin returns this diagnostic and makes no further
//     checks
//
// # Usage
//
// Both registered names are equivalent:
//
//	plugin "beansprout.plugins.check_metadata" "<config>"
//
// or, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/checkmetadata" "<config>"
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.check_metadata" — upstream Python module path.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/checkmetadata" —
//     Go import path.
package checkmetadata
