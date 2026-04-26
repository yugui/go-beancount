// Package sellgains is the Go port of upstream beancount's sellgains
// plugin — a diagnostic-only check that cross-validates declared gains
// against the price-times-units side of capital-gain transactions.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/sellgains.py
//
// Upstream copyright: Copyright (C) 2015-2021, 2024-2026 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see
// COPYING).
//
// # What it checks
//
// When you sell stock, the gains can be implied by the corresponding
// cash, fee, and gain postings. For a transaction shaped like
//
//	1999-07-31 * "Sell"
//	  Assets:US:BRS:Company:ESPP   -81 ADSK {26.3125 USD} @ 26.4375 USD
//	  Assets:US:BRS:Company:Cash    2141.36 USD
//	  Expenses:Financial:Fees       0.08 USD
//	  Income:US:Company:ESPP:PnL
//
// upstream computes
//
//	-81 × 26.4375 = -2141.4375
//	+ 2141.36 (Assets) + 0.08 (Expenses) = 2141.44
//
// and verifies that |total_price + total_proceeds| sits below a small
// tolerance, currency-by-currency. The Income leg is intentionally
// excluded from the proceeds sum: the price annotation is the
// independent check, so an elided or auto-computed Income amount can
// still be verified against the user-entered sale price.
//
// The check applies only to transactions where every posting that has
// a Cost annotation also has a Price annotation; transactions without
// any priced cost-basis postings — or where some cost-basis postings
// lack a price — are skipped.
//
// Diagnostics carry the kebab-case code "invalid-sell-gains". The
// plugin is diagnostic-only: it returns nil Result.Directives, so the
// runner makes no change to the ledger, mirroring upstream's behavior
// of returning entries unchanged.
//
// # Accounts considered "proceeds"
//
// "Proceeds" — the non-Income side of the equation — covers postings
// whose account roots are Assets, Liabilities, Equity, or Expenses.
// Income postings are excluded because their amount is what the check
// implicitly derives. This matches upstream's `proceed_types` set,
// which is built from the option-driven account-type names; this port
// hardcodes the four English-language defaults pending a
// go-beancount options-registry extension for `name_assets`,
// `name_liabilities`, `name_equity`, `name_expenses`, and
// `name_income`.
//
// # Posting weight
//
// Each non-Income posting contributes its weight to the proceeds sum,
// matching upstream's `convert.get_weight`:
//
//   - A posting with a Cost annotation contributes
//     units × cost-per-unit in the cost currency. (Total-only cost
//     forms are accepted: the contribution is the total in the cost
//     currency, with sign matching units.)
//   - A posting with a Price annotation but no Cost contributes
//     units × price-per-unit in the price currency, or the total price
//     directly when the annotation is the @@-form.
//   - A posting with neither contributes its plain Amount.
//
// Postings with a nil Amount (auto-balanced residuals) are skipped;
// their weight is unknown until interpolation has run, which is a
// validator's job, not this plugin's.
//
// # Tolerance
//
// Each currency's tolerance is inferred from the precision of the
// posting amounts in that currency: the smallest exponent observed
// (the most precise number) sets the base, and the tolerance is
// `multiplier × 10^minExponent × 2`, where the leading multiplier is
// the Beancount-default 0.5 and the trailing factor of 2 matches
// upstream's `EXTRA_TOLERANCE_MULTIPLIER`. With default precision this
// is 0.005 × 2 = 0.01 for two-decimal-place currencies. The looser
// bound exists because rounding occurs both on the price-side and the
// proceeds-side, and a user-entered ledger usually only satisfies one
// of those constraints byte-for-byte.
//
// # Diagnostic anchor
//
// Diagnostics anchor at the offending Transaction directive's Span
// when it is non-zero, and otherwise at the triggering Plugin
// directive's Span — matching the convention used by sibling diagnostic
// ports such as noduplicates and uniqueprices. The Transaction is
// where the user fixes the imbalance; the plugin-directive fallback
// keeps the diagnostic associated with the activation point in
// fixture-built tests that omit per-directive spans.
//
// # Deviations from upstream
//
//   - The kebab-case diagnostic code "invalid-sell-gains" is added by
//     this port; upstream's SellGainsError namedtuple has no
//     machine-readable category. Downstream tooling (lsp, log filters)
//     can match on it without parsing the human-readable message.
//   - Tolerance inference is a self-contained, simplified version of
//     upstream's `interpolate.infer_tolerances`: this port honors the
//     precision-of-the-most-precise-posting rule for unit currencies
//     but does not consume the `inferred_tolerance_multiplier` or
//     `infer_tolerance_from_cost` options, because the plugin runs
//     above an Input that does not yet thread option directives
//     through to plugins (PLAN.md Phase 4 owns option plumbing). The
//     0.5 base multiplier is hardcoded, matching the Beancount default,
//     and `EXTRA_TOLERANCE_MULTIPLIER` is preserved at 2. Ledgers that
//     override either option will see slightly different acceptance
//     thresholds than upstream until option threading lands; the
//     default-options case is byte-identical.
//   - The "proceeds" account roots are hardcoded to the English
//     defaults Assets, Liabilities, Equity, Expenses (and the excluded
//     Income root) for the same reason. PLAN.md Phase 6d documents
//     this as the standing tradeoff for plugins that need account-type
//     names. Ledgers using non-English account roots configured via
//     `name_*` options will not be matched correctly until the option
//     threading lands.
//   - Upstream's diagnostic message embeds the upstream Inventory
//     repr, which includes Python-side formatting peculiarities. This
//     port emits a stable, human-readable message of the form
//     "Invalid price vs. proceeds for <date>: <details>", with the
//     details listing each disagreeing currency in alphabetical order.
//   - The diagnostic is emitted only once per offending transaction,
//     even if multiple currencies disagree. Upstream behaves the same
//     way (one SellGainsError per transaction); this port preserves
//     that contract.
//   - Diagnostics are emitted in the source order of the offending
//     transactions, matching the iterator's traversal order. This
//     happens to coincide with upstream's order, where the Python list
//     comprehension also walks entries in source order.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.sellgains" — upstream Python module path, so
//     existing ledgers activate the port without edits. (Upstream's
//     module path has no underscore, so this name happens to match the
//     Go package name byte-for-byte.)
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/sellgains" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package sellgains
