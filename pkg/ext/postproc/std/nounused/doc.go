// Package nounused is the Go port of upstream beancount's nounused
// plugin — it diagnoses any account that is opened but never
// subsequently referenced by another directive.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/nounused.py
//
// Upstream copyright: Copyright (C) 2014, 2016-2017, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once and partitions them into two
// streams: [ast.Open] directives populate a map keyed by account, and
// every other directive contributes its referenced accounts to a "used"
// set. After the walk, any account in the open map whose name is not in
// the used set yields one diagnostic.
//
// Diagnostics carry the code "unused-account". The accompanying
// human-readable message names the offending account, matching
// upstream's "Unused account '{}'" template verbatim.
//
// The plugin is diagnostic-only. It returns nil Result.Directives so
// the runner makes no change to the ledger, mirroring upstream's
// behavior of returning the entries unchanged.
//
// # Usage
//
// The plugin takes no Config string; activation alone is enough. Either
// registered name works:
//
//	plugin "beancount.plugins.nounused"
//
// or, equivalently, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/nounused"
//
// Given a ledger with an Open that no later directive references:
//
//	plugin "beancount.plugins.nounused"
//
//	2024-01-01 open Assets:Cash
//	2024-01-01 open Assets:Stale
//
//	2024-01-15 * "Coffee"
//	  Assets:Cash       -5.00 USD
//	  Expenses:Coffee    5.00 USD
//
// the plugin emits a diagnostic of the form:
//
//	[unused-account] Unused account 'Assets:Stale'
//
// anchored at the offending Open directive. The fix is to delete the
// stale Open or to use the account.
//
// # What counts as a use
//
// To match upstream's `getters.get_entry_accounts`, the directive types
// that contribute account references are:
//
//   - [ast.Close], [ast.Balance], [ast.Note], [ast.Document] — single
//     Account field.
//   - [ast.Pad] — both Account and PadAccount (upstream's
//     source_account).
//   - [ast.Transaction] — every posting's Account.
//
// Notably absent: [ast.Custom]. Upstream classifies Custom under its
// "no accounts" handler (`_zero`), so even when a Custom directive's
// values include an [ast.MetaAccount] entry, those values do not count
// as a use. This port preserves that behavior so a Custom-only
// reference still triggers the diagnostic — matching the upstream
// catch-typos-and-stale-opens intent.
//
// An [ast.Open] directive's own Account does not count as a use of
// itself, as in upstream — Opens are filtered out before account
// references are gathered. An account that is opened and immediately
// closed (with no other reference) is therefore considered USED, since
// the [ast.Close] directive contributes the account name to the used
// set. Upstream documents this explicitly as "an account that is open
// and then closed is considered used"; this port preserves that
// behavior.
//
// # Diagnostic anchor
//
// Each diagnostic's Span is anchored at the offending [ast.Open]
// directive's own Span when non-zero, falling back to the triggering
// [ast.Plugin] directive's Span otherwise. The Open directive is the
// actionable fix location: the user either deletes it or replaces the
// stale account name with the intended one.
//
// # Output ordering
//
// Errors are sorted alphabetically by account name so the diagnostic
// stream is deterministic across runs. Upstream relies on Python's
// dict iteration order (insertion order in CPython 3.7+); sorting is
// the closest portable equivalent and matches the convention adopted
// by sibling ports (onecommodity, leafonly).
//
// # Deviations from upstream
//
//   - Upstream's diagnostic carries the Open directive's `meta` mapping
//     as its source. This port substitutes [ast.Span] from the Open
//     directive, with a fallback to the triggering plugin directive's
//     Span when the Open lacks a span. The information conveyed is the
//     same — point the user at the Open — through the structured
//     [ast.Diagnostic] contract used across go-beancount diagnostics.
//   - The diagnostic code "unused-account" is added by this port for
//     downstream tooling to match on; upstream's `UnusedAccountError`
//     namedtuple carries no machine-readable category. The kebab-case
//     spelling matches the convention used by sibling ports.
//   - Errors are emitted in alphabetical account order; upstream
//     emits them in the iteration order of the open map (CPython's
//     insertion order). The change is purely for deterministic output.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.nounused" — upstream Python module path, so
//     existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/nounused" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package nounused
