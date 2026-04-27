// Package coherentcost is the Go port of upstream beancount's
// coherent_cost plugin — it diagnoses any (account, commodity) pair
// that is held both with and without a cost annotation in the same
// ledger. Mixing the two for the same account+commodity confuses
// booking and is almost always an importer or data-entry bug.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/coherent_cost.py
//
// Upstream copyright: Copyright (C) 2016-2017, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once, considering only
// [ast.Transaction] directives. For every [ast.Posting] whose Amount is
// non-nil, it keys observations by (Account, Amount.Currency) and
// records whether the posting carried a Cost annotation (Cost != nil)
// or not (Cost == nil). After the walk, for each (Account, Commodity)
// key seen with BOTH with-cost and without-cost forms, the plugin
// emits one diagnostic.
//
// Diagnostics carry the code "incoherent-cost". The plugin is
// diagnostic-only: it returns nil Result.Directives so the runner
// makes no change to the ledger, mirroring upstream's behavior of
// returning the entries unchanged.
//
// Postings whose Amount is nil (auto-balanced postings) carry no
// currency and are skipped without contributing to either set, so the
// plugin does not crash on the canonical Beancount idiom of omitting
// the amount on the residual posting.
//
// # Diagnostic anchor
//
// Each diagnostic's Span is anchored at the offending account's
// [ast.Open] directive when one exists in the ledger; otherwise it
// falls back to the triggering [ast.Plugin] directive's Span. This
// matches the actionable-fix location for users — the Open is where
// the user typically fixes the booking method or splits the account
// into separate at-cost and not-at-cost children — and mirrors the
// anchor convention adopted by sibling onecommodity.
//
// # Output ordering
//
// Diagnostics are emitted in lexicographic order, first by Account and
// then by Commodity, so the output is stable across runs regardless of
// Go's map iteration randomness.
//
// # Usage
//
// The plugin takes no Config string; activation alone is enough. Either
// registered name works:
//
//	plugin "beancount.plugins.coherent_cost"
//
// or, equivalently, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/coherentcost"
//
// A ledger that books AAPL into the same account both at-cost and
// without-cost trips the diagnostic:
//
//	plugin "beancount.plugins.coherent_cost"
//
//	2024-01-01 open Assets:Inv
//	2024-01-02 * "Buy at cost"
//	  Assets:Inv          10 AAPL {150.00 USD}
//	  Assets:Cash     -1500.00 USD
//	2024-01-03 * "Receive without cost"
//	  Assets:Inv           5 AAPL
//	  Income:Gift        -5 AAPL
//
// emits a diagnostic of the form:
//
//	[incoherent-cost] Account 'Assets:Inv' holds 'AAPL' both with and
//	without a cost
//
// anchored at the `2024-01-01 open Assets:Inv` directive — the typical
// fix is on that Open, either via a tighter booking method or by
// splitting the account into separate at-cost and not-at-cost children.
//
// # Deviations from upstream
//
//   - Upstream keys observations by Currency alone (a single global
//     map per cost-flag), so it would flag any commodity that ever
//     appears with-cost in one account and without-cost in any other
//     account in the ledger. This port keys by (Account, Commodity)
//     instead, treating each account independently. The intent of
//     upstream's check is to catch a per-account booking mismatch —
//     the failure mode it cites is "selling a lot without specifying
//     it via its cost basis," which is a per-account concern — so the
//     stricter per-account key is a deliberate refinement: it avoids
//     false positives across accounts that legitimately hold the same
//     commodity in different forms (e.g. an investment account holds
//     USD cash without cost, while a forex trading account holds USD
//     positions at cost). Tests in this package are pinned to the
//     per-account rule.
//   - Upstream's diagnostic anchor is the source metadata of the
//     without-cost entry that first introduced the conflicting
//     currency, with the with-cost entry attached as the namedtuple's
//     `entry` field. This port anchors at the offending account's
//     [ast.Open] directive (or the triggering [ast.Plugin] directive
//     when no Open exists) for actionability, matching sibling
//     onecommodity. The source-of-conflict transactions are not
//     identified individually in the diagnostic; the user finds them
//     by grepping the account for the named commodity.
//   - Upstream's diagnostic message is "Currency '{}' is used both
//     with and without cost". This port's message names the account
//     as well: "Account '{}' holds '{}' both with and without a cost".
//     The richer message reflects the per-account key.
//   - The diagnostic code "incoherent-cost" is added by this port;
//     upstream's CoherentCostError namedtuple carries no
//     machine-readable category. The kebab-case spelling matches the
//     convention used by sibling ports.
//   - Diagnostic ordering is alphabetical by (Account, Commodity);
//     upstream relies on Python's set iteration order, which is
//     insertion-order in CPython 3.7+. Sorting is the closest portable
//     equivalent and gives stable output across runs.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.coherent_cost" — upstream Python module path,
//     so existing ledgers activate the port without edits. The
//     underscore in the module path is preserved verbatim from
//     upstream; only the Go package name uses the underscore-free
//     spelling because Go disallows underscores in package names.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/coherentcost" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package coherentcost
