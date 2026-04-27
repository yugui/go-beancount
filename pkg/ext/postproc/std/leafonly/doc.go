// Package leafonly is the Go port of upstream beancount's leafonly
// plugin — it diagnoses any non-leaf account that is used in a
// transaction posting.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/leafonly.py
//
// Upstream copyright: Copyright (C) 2014-2017, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// An account is "non-leaf" when some other account referenced anywhere
// in the ledger has it as a strict ancestor: e.g. if the ledger
// references both "Assets:Cash" and "Assets:Cash:USD", then
// "Assets:Cash" is non-leaf. The plugin walks every directive once to
// build the set of referenced accounts and the set of non-leaf
// accounts derived from it; it then walks the ledger a second time to
// emit one diagnostic per [ast.Transaction] posting whose account is
// in the non-leaf set.
//
// Diagnostics carry the code "non-leaf-account". The accompanying
// human-readable message names the offending account, matching
// upstream's "Non-leaf account '{}' has postings on it".
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
//	plugin "beancount.plugins.leafonly"
//
// or, equivalently, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/leafonly"
//
// Given a ledger that opens both a parent and a child account and then
// posts to the parent
//
//	plugin "beancount.plugins.leafonly"
//
//	2024-01-01 open Assets:Cash
//	2024-01-01 open Assets:Cash:USD
//	2024-01-15 * "Coffee"
//	  Assets:Cash       -5.00 USD
//	  Expenses:Coffee    5.00 USD
//
// the plugin emits a diagnostic of the form:
//
//	[non-leaf-account] Non-leaf account 'Assets:Cash' has postings on it
//
// anchored at the offending posting's Span. The fix is to post to the
// leaf (`Assets:Cash:USD`) instead of the parent.
//
// # Sources of account references
//
// To match upstream's `realization.realize` (which is what populates
// the tree of accounts whose children determine non-leafness), the set
// of directives that contribute an account reference is:
//
//   - Open, Close, Balance, Note, Document — single Account field.
//   - Pad — Account and PadAccount (upstream's source_account).
//   - Transaction — every posting's Account.
//   - Custom — any value whose kind is [ast.MetaAccount].
//
// An account does not need to be opened to count: a transaction
// posting alone is enough to make a parent non-leaf. This matches
// upstream, where `realization.realize` builds the tree from
// `postings_by_account(entries)` (every directive that mentions an
// account contributes to the tree).
//
// # Deviations from upstream
//
//   - Upstream emits one diagnostic per offending non-leaf account and
//     anchors it at the account's Open directive (or a synthetic
//     "<leafonly>" sentinel when no Open exists). This port emits one
//     diagnostic per offending posting and anchors it at the posting's
//     own Span, falling back to the enclosing transaction's Span and
//     finally to the triggering plugin directive's Span. The shift is
//     intentional: in beancount-lsp and other tooling the actionable
//     fix is on the posting line, not the open line. The total error
//     count therefore differs from upstream when a single non-leaf
//     account has multiple bad postings.
//   - Upstream's filter is "any txn_posting that is not an Open or
//     Balance"; that includes Close, Note, Document, Pad, and Custom
//     references on a non-leaf account. This port restricts the
//     trigger to Transaction postings, since posting-to-parent is the
//     bug class the plugin exists to catch and the other directives
//     are typically harmless metadata anchors.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.leafonly" — upstream Python module path, so
//     existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/leafonly" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package leafonly
