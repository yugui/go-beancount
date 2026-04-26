// Package autoaccounts is the Go port of upstream beancount's
// auto_accounts plugin — it synthesizes Open directives for any account
// referenced in the ledger but never explicitly opened.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/auto_accounts.py
//
// Upstream copyright: Copyright (C) 2014-2017, 2022, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once and gathers, for each referenced
// account, the earliest date on which it is referenced. For every such
// account that does not already have an explicit Open directive, the
// plugin synthesizes one Open directive at that earliest date with no
// currency constraint and the default booking method (matching upstream's
// `data.Open(meta, date_first_used, account, None, None)`).
//
// The plugin is synthesizing: it returns a Result with Directives set to
// a fresh slice containing all original directives plus the synthesized
// Opens. Input directives are never mutated.
//
// # Sources of account references
//
// To stay byte-compatible with upstream, the set of directives that
// contribute an account reference is limited to the ones upstream's
// `getters.get_accounts_use_map` walks:
//
//   - Open, Close, Balance, Note, Document — single Account field.
//   - Pad — Account and PadAccount (upstream's source_account).
//   - Transaction — every posting's Account.
//
// Other directive types (Commodity, Event, Query, Price, Custom) do not
// contribute account references, even when a Custom directive happens to
// embed an account-typed MetaValue. Upstream classifies Custom under the
// "no accounts" handler; this port preserves that decision so existing
// ledgers see identical Open synthesis.
//
// # Output ordering
//
// Upstream sorts the combined (original + synthesized) entry list by
// `data.entry_sortkey` after appending. This port returns a single slice
// in input order with synthesized Opens prepended; the runner re-sorts
// the result through [ast.Ledger.ReplaceAll] when it commits the
// returned slice, which uses the same canonical (date, kind, span,
// sequence) ordering. The user-visible result is therefore equivalent
// to upstream's: synthesized Opens land immediately before the
// directives that first reference each account on each shared day, and
// in canonical chronological order across days.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.auto_accounts" — upstream Python module path,
//     so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/autoaccounts" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package autoaccounts
