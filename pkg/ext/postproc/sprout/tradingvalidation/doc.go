// Package tradingvalidation is the Go port of beansprout's
// trading_validation plugin. It validates that every transaction touching
// a trading account satisfies three balance rules.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/trading_validation.py
//
// # Behavior
//
// A "trading account" is any account whose name starts with a configurable
// prefix (Config string, default "Equity:Trading"). The prefix match is
// account-boundary aware: a prefix `P` matches the account `P` itself or
// any sub-account `P:<...>`, but does NOT match a sibling whose name
// merely shares the same leading characters (e.g. `Equity:TradingDesk`
// is not a trading account when the prefix is `Equity:Trading`). This is
// stricter than the upstream Python plugin's plain `str.startswith`
// check and prevents false positives on unrelated accounts.
//
// For every [ast.Transaction] that contains at least one posting to a
// trading account the plugin checks three rules:
//
//  1. Trading-account postings alone must balance to zero (within
//     tolerance) across all currencies they mention.
//  2. Non-trading-account postings alone must balance to zero (within
//     tolerance) across all currencies they mention.
//  3. For each effective commodity, all postings that belong to that
//     commodity must balance to zero. The effective commodity of a posting
//     is normally its units currency. For commodities whose [ast.Commodity]
//     directive carries the metadata key "trading-account" with the value
//     "disabled", the posting is grouped instead by the price currency of
//     the price annotation. If such a posting has no price annotation it is
//     excluded from rule 3.
//
// Tolerance for numeric comparisons uses the same inference logic as the
// std/sellgains port: per-currency minimum exponent from posting unit
// amounts multiplied by the ledger's tolerance_multiplier option
// (default 0.5).
//
// # Diagnostic codes
//
//   - "trading-not-balanced" — rule 1 or rule 2 failed; one diagnostic per
//     failing currency within the offending sub-group.
//   - "trading-commodity-not-balanced" — rule 3 failed; one diagnostic per
//     failing commodity.
//
// All diagnostics carry [ast.Error] severity and are anchored at the
// offending transaction's Span, falling back to the triggering plugin
// directive's Span.
//
// # Usage
//
// Either registered name works:
//
//	plugin "beansprout.plugins.trading_validation"
//
// or, equivalently, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/tradingvalidation"
//
// An optional Config string overrides the default account prefix:
//
//	plugin "beansprout.plugins.trading_validation" "Assets:Trading"
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.trading_validation" — upstream Python module path,
//     so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/tradingvalidation"
//     — Go import path, matching the convention for Go-native plugins.
package tradingvalidation
