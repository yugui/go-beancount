// Package closetree is the Go port of upstream beancount's close_tree
// plugin — when an account is closed, the plugin synthesizes Close
// directives for every still-open descendant of that account so that a
// single user-authored Close shuts down the entire subtree.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/close_tree.py
//
// Upstream copyright: Copyright (C) 2023-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once, copying it through to the
// output. For every [ast.Close] directive C it computes the set of
// "subtree" accounts to also close: an account A qualifies when
//
//   - A is a strict descendant of C.Account
//     (per [ast.Account.IsAncestorOf]);
//   - A has been Open'ed earlier in source order than C, and that Open's
//     date is on or before C.Date (see "Date-aware Open visibility"
//     below);
//   - A has not been explicitly Closed earlier in source order, and has
//     not already been synthesized as a Close by an outer-tree Close in
//     this same pass.
//
// One [ast.Close] directive is synthesized per qualifying descendant.
// The synthesized Close inherits the parent Close's Date and Span — the
// Span anchors diagnostics back to the user-authored close line, which
// is the most actionable location for any downstream balance-mismatch
// or unused-account error.
//
// The plugin is synthesizing: it returns a Result with Directives set
// to a fresh slice containing all original directives plus the
// synthesized Closes. Input directives are never mutated. Empty input
// or a pass that synthesizes nothing returns Result{Directives: nil}
// (the no-change signal), matching the convention used by
// implicitprices.
//
// # Output ordering
//
// Synthesized Closes are appended to the output slice immediately after
// the parent Close that produced them, in alphabetical order of the
// descendant account name. This keeps the per-parent provenance
// readable in the un-canonicalized slice. The runner re-sorts the
// returned slice through [ast.Ledger.ReplaceAll] when it commits, which
// applies the canonical (date, kind, span, sequence) ordering — so the
// final, user-visible ordering matches whatever the canonical sort
// dictates regardless of this in-pass placement.
//
// # Usage
//
// The plugin takes no Config string; activation alone is enough. Either
// registered name works — the upstream Python module path for ledger
// portability, or the Go import path:
//
//	plugin "beancount.plugins.close_tree"
//
// or
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/closetree"
//
// A single user-authored Close on a parent account closes the entire
// subtree. Given the ledger fragment
//
//	plugin "beancount.plugins.close_tree"
//
//	2020-01-01 open Assets:Brokerage:Cash
//	2020-01-01 open Assets:Brokerage:Stocks:AAPL
//	2020-01-01 open Assets:Brokerage:Stocks:GOOG
//	2024-12-31 close Assets:Brokerage
//
// the plugin synthesizes Close directives for every descendant still
// open on the close date, so the effective directive stream becomes:
//
//	2020-01-01 open Assets:Brokerage:Cash
//	2020-01-01 open Assets:Brokerage:Stocks:AAPL
//	2020-01-01 open Assets:Brokerage:Stocks:GOOG
//	2024-12-31 close Assets:Brokerage
//	2024-12-31 close Assets:Brokerage:Cash
//	2024-12-31 close Assets:Brokerage:Stocks:AAPL
//	2024-12-31 close Assets:Brokerage:Stocks:GOOG
//
// # Deviation: date-aware Open visibility
//
// Upstream builds a single global "opens" set over the whole entry
// list, with no awareness of source order or directive dates. As a
// result a child account opened at date D2 > D1 is still synthesized
// a Close on date D1 when its parent is closed at D1. The synthesized
// Close then predates the child's Open, which is invariably a ledger
// error.
//
// This port skips Open directives that occur after the parent Close in
// source order, and additionally skips Opens whose date is strictly
// after the parent Close's date. Either guard alone would be enough in
// well-formed ledgers (where directives appear in chronological order),
// but the source-order check is cheaper and the date check catches
// out-of-order ledgers; we apply both for safety.
//
// # Deviation: original Close is preserved unconditionally
//
// Upstream drops the user-authored Close if its account was never
// Open'ed (`if entry.account in opens: new_entries.append(entry)`).
// That is surprising behavior — silently swallowing a directive the
// user wrote — and it makes the plugin a partial transformation rather
// than a pure addition. This port always preserves the original Close
// in the output. Validation of "close on never-opened account" is the
// job of the validator, not this plugin.
//
// # Deviation: source-order tracking of explicit Closes
//
// Upstream builds a global "closes" set over the whole entry list, so
// a Close that appears later in the file still suppresses synthesis at
// an earlier-seen parent Close. This port instead tracks explicit
// Closes seen so far at each step. In practice the difference is
// invisible for chronologically-ordered ledgers (the only kind users
// write); the change matters only to obey the principle of least
// surprise — a directive cannot retroactively cancel a synthesis that
// came before it.
//
// # Deviation: provenance metadata
//
// Upstream attaches a `<beancount.plugins.close_tree>` synthetic
// filename to each generated Close via `data.new_metadata(...)`. This
// port reuses the parent Close's Span instead, which is more actionable
// (the user can find the close they wrote). A TODO is open to add a
// dedicated provenance key once the project has a stable convention
// for synthesized metadata; today no such key exists.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.close_tree" — upstream Python module path
//     (with the underscore), so existing ledgers activate the port
//     without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/closetree" —
//     Go import path (no underscore, since Go package identifiers
//     cannot contain underscores), matching Phase 6a's convention for
//     Go-native plugins.
package closetree
