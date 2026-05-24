// Package leafonly is the Go port of beansprout's leafonly plugin —
// it diagnoses any non-leaf account that is used in a transaction
// posting or a pad directive.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/leafonly.py
//
// # Behavior
//
// An account is "non-leaf" when some other account referenced anywhere
// in the ledger has it as a strict ancestor: e.g. if the ledger
// references both "Assets:Cash" and "Assets:Cash:USD", then
// "Assets:Cash" is non-leaf. The plugin walks every directive once to
// build the set of referenced accounts and the set of non-leaf
// accounts derived from it; it then walks the ledger a second time to
// emit one diagnostic per offending [ast.Transaction] posting or
// [ast.Pad] directive whose account is in the non-leaf set.
//
// Diagnostics carry the code "non-leaf-account". The accompanying
// human-readable message names the offending account, in the form
// "non-leaf account '{}' has transactions or pad directives on it".
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
//	plugin "beansprout.plugins.leafonly"
//
// or, equivalently, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/leafonly"
//
// Given a ledger that opens both a parent and a child account and then
// posts to the parent
//
//	plugin "beansprout.plugins.leafonly"
//
//	2024-01-01 open Assets:Cash
//	2024-01-01 open Assets:Cash:USD
//	2024-01-15 * "Coffee"
//	  Assets:Cash       -5.00 USD
//	  Expenses:Coffee    5.00 USD
//
// the plugin emits a diagnostic of the form:
//
//	[non-leaf-account] non-leaf account 'Assets:Cash' has transactions or pad directives on it
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
// # Deviations
//
// This plugin deviates from its two nearest relatives in distinct
// directions, by design.
//
// Deviation 1 — trigger filter is NARROWER than std/leafonly:
//
// The std/leafonly port (registering under "beancount.plugins.leafonly")
// accepts all transaction postings as potential triggers. This port
// restricts triggers to [ast.Transaction] postings and [ast.Pad]
// directives only, matching beansprout-Python's explicit filter
// (`isinstance(item, (data.TxnPosting, data.Pad))`). Open, Close,
// Note, Document, Balance, and Custom references on a non-leaf account
// do NOT trigger here.
//
// Deviation 2 — diagnostic anchor DIFFERS from beansprout-Python, aligns with std/leafonly:
//
// Beansprout-Python anchors each error at the account's Open directive
// (or a synthetic "<leafonly>" sentinel when no Open exists). This port
// anchors at the offending posting / pad span instead, falling back to
// the enclosing transaction's span and finally to the triggering plugin
// directive's span — matching std/leafonly's LSP-friendly convention
// where the diagnostic points to the actionable site, not the account
// declaration.
//
// # Coexistence with std/leafonly
//
// Both plugins can be active simultaneously in the same beancount file.
// They register under different names and coexist without conflict:
//
//   - "beancount.plugins.leafonly" — the std port (broader trigger).
//   - "beansprout.plugins.leafonly" — this port (Transaction + Pad only).
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.leafonly" — upstream Python module path, so
//     existing beansprout ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/leafonly" —
//     Go import path, matching the Go-native plugin convention.
package leafonly
