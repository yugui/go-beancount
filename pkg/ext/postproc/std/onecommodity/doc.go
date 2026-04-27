// Package onecommodity is the Go port of upstream beancount's
// onecommodity plugin — it diagnoses any account that holds postings
// (or balance assertions) in more than one commodity.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/onecommodity.py
//
// Upstream copyright: Copyright (C) 2014-2020, 2022, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once and accumulates, per account,
// the set of unit currencies (from [ast.Transaction] postings and
// [ast.Balance] assertions) and the set of cost currencies (from the
// cost spec on [ast.Transaction] postings). After the walk it emits one
// diagnostic per account whose unit-currency set has more than one
// element and one diagnostic per account whose cost-currency set has
// more than one element. The two checks are independent: an account
// can earn one diagnostic for mixed units, another for mixed costs,
// neither, or both.
//
// Diagnostics carry the code "multi-commodity-account". The
// human-readable message matches upstream verbatim:
//
//   - "More than one currency in account '{}': {comma-joined currencies}"
//   - "More than one cost currency in account '{}': {comma-joined currencies}"
//
// The currency list is sorted alphabetically so the message is stable
// across runs (upstream relies on Python's set iteration order, which
// is insertion-order in CPython 3.7+; sorting is the closest portable
// equivalent).
//
// The plugin is diagnostic-only. It returns nil Result.Directives so
// the runner makes no change to the ledger, mirroring upstream's
// behavior of returning the entries unchanged.
//
// # Usage
//
// Activate the plugin with no Config to check every account:
//
//	plugin "beancount.plugins.onecommodity"
//
// Pass a regular expression as the Config to scope the check to a
// subset of accounts — only accounts whose name matches the regex (at
// the start of the name, since upstream's `re.match` is anchored) are
// considered. To check only Asset accounts, for example:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/onecommodity" "Assets:.*"
//
// Either registered name accepts a Config; pick whichever matches your
// ledger's convention. Per-account opt-out is available via metadata
// on the Open directive — the metadata key is `onecommodity` and the
// value is `FALSE`:
//
//	2024-01-01 open Assets:MultiCurrency
//	  onecommodity: FALSE
//
// A typical diagnostic looks like:
//
//	[multi-commodity-account] More than one currency in account
//	'Assets:Cash': EUR, USD
//
// anchored at the offending account's Open directive.
//
// # Opt-out
//
// An account is excluded from the check when any of the following
// holds, matching upstream:
//
//   - Its [ast.Open] directive has metadata "onecommodity: FALSE"
//     (case-sensitive key; the value is recognised as either
//     [ast.MetaBool]{Bool:false} or [ast.MetaString] whose value is a
//     case-insensitive "false").
//   - Its [ast.Open] directive declares more than one currency in the
//     Currencies slot — the user has explicitly asked for a
//     multi-currency account, so the plugin defers to that declaration.
//   - The plugin's optional Config string is a regular expression and
//     the account's name does not match it. Upstream's regex is
//     anchored with `re.match`; this port mirrors that by anchoring at
//     the start of the input. Invalid regular expressions emit an
//     "invalid-regexp" diagnostic and disable the regex filter for the
//     remainder of the run (treated as no filter).
//
// # Diagnostic anchor
//
// Each diagnostic's Span is anchored at the offending account's
// [ast.Open] directive when one exists in the ledger; otherwise it
// falls back to the triggering [ast.Plugin] directive's Span. This
// matches the actionable-fix location for users: the Open is where the
// "multi-commodity: FALSE" opt-out belongs.
//
// # Postings without an Amount
//
// A posting with a nil Amount (the auto-balanced posting in a
// transaction) contributes no currency observation; it is skipped so
// the plugin does not crash on the canonical Beancount idiom of
// omitting the amount on the residual posting.
//
// # Deviations from upstream
//
//   - Upstream's diagnostic anchor is the Transaction or Balance entry
//     in which the multi-currency state was first observed (the entry
//     that pushed the count past one). This port anchors at the
//     account's [ast.Open] directive instead, which is where the
//     opt-out metadata or currency-set declaration would be added —
//     the actionable fix location. The fallback when no Open exists
//     is the triggering [ast.Plugin] directive's Span.
//   - Upstream emits errors with Python-set iteration order for the
//     currency list. This port sorts the list alphabetically for
//     stable output across runs.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.onecommodity" — upstream Python module path,
//     so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/onecommodity" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package onecommodity
