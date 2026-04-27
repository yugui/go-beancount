// Package excludetag is the Go port of upstream beancount's exclude_tag
// plugin — it removes [ast.Transaction] directives that carry a
// designated tag from the ledger, so a single tag can be used to mark
// transactions that should be hidden from the booked stream
// (canonical use case: virtual transactions used to demonstrate or
// rehearse postings without affecting balances).
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/exclude_tag.py
//
// Upstream copyright: Copyright (C) 2014, 2016-2017 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// For every directive in the input the plugin either copies it through
// to the output or drops it. A directive is dropped when it is an
// [ast.Transaction] whose Tags slice contains the configured tag as a
// whole-word member. Every other directive kind passes through
// unchanged, and a transaction without the tag is preserved.
//
// The configured tag is taken from the plugin directive's Config string
// — the second argument of `plugin "..." "..."`. When the Config is
// empty the plugin falls back to the upstream default, the literal
// string "virtual" (without the leading `#`, since [ast.Transaction.Tags]
// stores the bare name). Tag matching is case-sensitive — beancount
// tags are case-sensitive in upstream and across this codebase, so
// `Foo` and `foo` are distinct tags. Membership is by full-string
// equality, not substring: `"car"` does not match `"carpool"`.
//
// The plugin is filtering: when at least one transaction is dropped,
// it returns a Result with Directives set to a freshly-built slice
// containing exactly the surviving directives (preserving their
// original order). When no directive is dropped — including the empty
// or nil-iterator case — it returns Result{Directives: nil}, the
// no-change signal honoured by the runner. Input directives are never
// mutated.
//
// # Usage
//
// Activate the plugin with no Config to use the upstream-default tag
// "virtual":
//
//	plugin "beancount.plugins.exclude_tag"
//
// Activate with a Config string to filter on a different tag — pass
// the bare name without the leading `#`:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/excludetag" "rehearsal"
//
// Either registered name accepts a Config; pick whichever matches your
// ledger's convention. Given the input
//
//	plugin "beancount.plugins.exclude_tag"
//
//	2024-01-15 * "Coffee" #virtual
//	  Assets:Cash       -5.00 USD
//	  Expenses:Coffee    5.00 USD
//
//	2024-01-16 * "Lunch"
//	  Assets:Cash       -12.00 USD
//	  Expenses:Food     12.00 USD
//
// the plugin drops the first transaction (it carries `#virtual`) and
// passes the second through, so the effective directive stream is:
//
//	2024-01-16 * "Lunch"
//	  Assets:Cash       -12.00 USD
//	  Expenses:Food     12.00 USD
//
// # Deviation: configurable tag
//
// Upstream's plugin hard-codes the tag at module scope
// (`EXCLUDED_TAG = "virtual"`) and exposes no way to override it
// without forking the plugin; the docstring acknowledges this is an
// example more than a finished feature ("if we integrated this we
// could provide a way to choose which tags to exclude"). This port
// realises that note: the configured tag is taken from the plugin
// directive's Config string and falls back to "virtual" only when the
// config is empty. Existing ledgers that activate the plugin without
// a Config keep upstream-compatible behaviour; ledgers that pass a
// Config get to pick their own tag without forking.
//
// # Deviation: leading `#` in the configured tag
//
// Beancount tags appear in source with a `#` prefix
// (`#virtual`), but the parser strips that prefix before populating
// [ast.Transaction.Tags]. This port treats the Config string the same
// way the AST treats the field — the bare name. A Config of `#virtual`
// would be compared against the bare-name field and not match
// anything, which is consistent with how a user would write the tag
// name in any other Go-side context (regex filters, metadata keys, …).
// The package godoc spells this out so users do not paste the `#` by
// reflex.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.exclude_tag" — upstream Python module path
//     (with the underscore), so existing ledgers activate the port
//     without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/excludetag" —
//     Go import path (no underscore, since Go package identifiers
//     cannot contain underscores), matching Phase 6a's convention for
//     Go-native plugins.
package excludetag
