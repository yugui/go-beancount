// Package noduplicates is the Go port of upstream beancount's
// noduplicates plugin — it diagnoses [ast.Transaction] directives that
// appear to be duplicates of an earlier transaction in the same ledger.
// The canonical bug class is importing the same bank export twice: two
// transactions on the same date posting the same amounts to the same
// accounts.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/noduplicates.py
//
// Upstream copyright: Copyright (C) 2014, 2016-2017, 2020, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once, considering only
// [ast.Transaction] directives. For each transaction it computes a
// similarity key — see "Similarity rule" below — and groups
// transactions by that key. For each group with more than one
// transaction, every transaction after the first (in source-encounter
// order) yields one diagnostic.
//
// Diagnostics carry the code "duplicate-transaction". The plugin is
// diagnostic-only: it returns nil Result.Directives so the runner makes
// no change to the ledger, mirroring upstream's behavior of returning
// the entries unchanged.
//
// # Similarity rule (simplification of upstream)
//
// Upstream's noduplicates.py delegates to
// [beancount.core.compare.hash_entries] with `exclude_meta=True`, which
// hashes the entire directive — for a Transaction that means Date,
// Flag, Payee, Narration, Tags, Links, AND every Posting (account,
// units, cost, price, but excluding the posting's own meta). Two
// transactions are duplicates only when ALL of those fields agree.
//
// This port adopts a deliberately simpler, more permissive rule:
//
//	Two [ast.Transaction] directives are duplicates iff
//	  (a) they fall on the same Date (calendar day, UTC), AND
//	  (b) they have the same multiset of
//	      (Account, Amount.Number, Amount.Currency)
//	      tuples across their Postings.
//
// Concretely:
//
//   - Posting order does not matter: the multiset comparison is
//     order-insensitive, so a transaction whose postings are reversed
//     relative to an earlier one is still flagged. Upstream's hash is
//     also order-insensitive (it sorts subhashes), so this is
//     consistent with upstream on the posting-order question.
//   - Narration, Payee, Flag, Tags, and Links are NOT part of the key.
//     This is the central deviation from upstream: an importer that
//     differs only in narration text (e.g. "ATM withdrawal" vs "ATM
//     WITHDRAWAL") will be flagged as a duplicate by this port but not
//     by upstream. The simplification is intentional — narration drift
//     across imports is exactly the case this diagnostic is meant to
//     catch — but it does mean two genuinely distinct same-day
//     transactions to the same accounts in the same amounts will be
//     flagged. Document the false-positive and add a metadata-based
//     opt-out in a follow-up if it becomes a real-world annoyance.
//   - [ast.Posting] entries with a nil Amount (the auto-balanced
//     residual posting) contribute to the multiset under a sentinel
//     key, so two transactions that both have an auto-balanced
//     posting on the same account match each other on that posting.
//     Cost specs and price annotations on postings are NOT consulted;
//     two transactions agreeing on (account, number, currency) are
//     duplicates even if their costs or prices differ. This is a
//     further simplification from upstream and is documented to keep
//     the rule simple.
//   - The "same Date" comparison is calendar-day equality on the
//     directive's Date field. This port does NOT implement upstream's
//     close-in-date window; the `find_similar_entries` helper that
//     supports a date window is not invoked by upstream's
//     noduplicates plugin (which calls `hash_entries`, not
//     `find_similar_entries`), so requiring exact-date equality here
//     is consistent with the noduplicates plugin specifically — even
//     though a hypothetical "near-duplicate" port using
//     `find_similar_entries` would need a window.
//
// The port does NOT claim full upstream parity; it claims to catch the
// same bug class (a duplicated import) under a simpler rule that is
// strictly more aggressive on the "narration differs" axis and
// strictly less strict on the "narration must match" axis. Tests in
// this package are pinned against the rule above, not against the
// upstream hash.
//
// # Diagnostic anchor
//
// Each diagnostic's Span is anchored at the duplicate
// [ast.Transaction]'s own Span when non-zero. The actionable fix is at
// the duplicate (the user removes it or fixes the importer that
// produced it), so anchoring there points the user at the directive
// to delete. When the duplicate Transaction has a zero Span the
// diagnostic falls back to the triggering [ast.Plugin] directive's
// Span, matching the convention used by sibling ports.
//
// # Output ordering
//
// Diagnostics are emitted in source-encounter order: the order in
// which the duplicate transactions appear in the input directive
// stream. Given a deterministic input order this is deterministic.
//
// # Deviations from upstream
//
//   - Similarity rule: this port keys on (Date, multiset of (Account,
//     Number, Currency) posting tuples) only. Upstream keys on every
//     transaction field except meta — Date, Flag, Payee, Narration,
//     Tags, Links, and every posting field except posting meta. The
//     simpler rule is documented above; tests are pinned against it.
//   - Diagnostic shape: upstream emits one CompareError per duplicate
//     entry whose human-readable message is "Duplicate entry: {entry}
//     == {other}". This port emits one [api.Error] per duplicate with
//     code "duplicate-transaction" and a message identifying the
//     duplicated date — the message is shorter but the structured
//     [api.Error.Code] preserves the upstream-equivalent category for
//     downstream tooling.
//   - The diagnostic code "duplicate-transaction" is added by this
//     port; upstream's CompareError namedtuple carries no
//     machine-readable category. The kebab-case spelling matches the
//     convention used by sibling ports.
//   - Non-Transaction directives are ignored. Upstream's
//     `hash_entries` operates on every directive type and explicitly
//     allows duplicate Price entries while flagging duplicates of
//     other types. Restricting this port to Transactions keeps the
//     rule focused on the importer-double-run bug class; duplicate
//     Price entries are out of scope (and, per upstream, are legal
//     anyway), and duplicate Open/Close/Balance/etc. directives are
//     better diagnosed by their respective dedicated validators.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.noduplicates" — upstream Python module path,
//     so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/noduplicates" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package noduplicates
