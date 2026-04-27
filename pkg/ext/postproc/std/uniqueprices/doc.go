// Package uniqueprices is the Go port of upstream beancount's
// unique_prices plugin — it diagnoses [ast.Price] directives whose
// (date, base, quote) triple has another Price directive declared on
// the same date but with a different price value. Multiple Price
// directives that agree on the value are not flagged.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/unique_prices.py
//
// Upstream copyright: Copyright (C) 2014, 2016-2017, 2020, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once and groups [ast.Price]
// directives by the triple (Date, Commodity, Amount.Currency). Within
// each group:
//
//   - If only one Price directive exists, no diagnostic.
//   - If multiple Price directives exist and they all agree on
//     [ast.Amount.Number] (compared via [apd.Decimal.Cmp] == 0), no
//     diagnostic — duplicates with matching values are explicitly
//     allowed by upstream.
//   - If multiple Price directives exist and at least two disagree on
//     the value, the plugin emits one diagnostic per Price directive
//     after the first whose value differs from the first directive's
//     value (in source-encounter order). For a three-way conflict the
//     plugin emits two diagnostics; for an N-way conflict it emits up
//     to N-1 diagnostics, one per offending directive. Any later Price
//     whose value matches the first is silently accepted (it is a
//     duplicate, not a conflict).
//
// Diagnostics carry the code "duplicate-price". Upstream's
// UniquePricesError namedtuple has no machine-readable category; the
// kebab-case spelling is added by this port for downstream tooling
// (lsp, log filters) to match on without parsing the message string.
//
// The plugin is diagnostic-only. It returns nil Result.Directives so
// the runner makes no change to the ledger, mirroring upstream's
// behavior of returning the entries unchanged.
//
// # Diagnostic anchor
//
// Each diagnostic's Span is anchored at the offending Price
// directive's own [ast.Price.Span] when non-zero. The actionable fix
// is at the second-encountered Price — that is the one introducing the
// disagreement — so anchoring there points the user at the directive
// to remove or correct. When the offending Price has a zero Span the
// diagnostic falls back to the triggering [ast.Plugin] directive's
// Span.
//
// # Output ordering
//
// Diagnostics are emitted in source-encounter order: the order in
// which the offending Price directives appear in the input directive
// stream. This is deterministic given a deterministic input order and
// matches the actionable-fix-first reading order. Upstream emits one
// error per conflict group rather than per directive, citing only the
// first directive of the group; this port emits per-directive
// diagnostics so each conflicting Price points at its own location.
//
// # Deviations from upstream
//
//   - Package name is "uniqueprices", not "unique_prices": Go package
//     identifiers may not contain underscores. The upstream module
//     path "beancount.plugins.unique_prices" is preserved as one of
//     the two registration names so existing ledger files continue to
//     work unchanged.
//   - Upstream emits one UniquePricesError per conflict group, citing
//     the entire group's directive list as the error's `entry`. This
//     port emits one [ast.Diagnostic] per offending Price directive after
//     the first, so each conflicting Price has its own anchored
//     diagnostic. The information conveyed is the same — the user
//     learns about every conflict — but the structured-diagnostic
//     contract used across go-beancount is one error per source
//     location, so the per-directive shape is the natural fit.
//   - The diagnostic code "duplicate-price" is added by this port for
//     downstream tooling to match on; upstream's UniquePricesError
//     namedtuple carries no machine-readable category.
//   - Upstream's error message is "Disagreeing price entries". This
//     port produces a more specific message naming the offending
//     commodity pair, date, and the two disagreeing values, e.g.
//     "Disagreeing price for HOOL/USD on 2024-01-01: 99 vs 100", so
//     the diagnostic is actionable on its own. The structured
//     [ast.Diagnostic.Code] preserves the upstream-equivalent category.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.unique_prices" — upstream Python module path
//     (with the underscore), so existing ledgers activate the port
//     without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/uniqueprices" —
//     Go import path (no underscore, since Go package identifiers
//     cannot contain underscores), matching Phase 6a's convention for
//     Go-native plugins.
package uniqueprices
