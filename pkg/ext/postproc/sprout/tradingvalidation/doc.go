// Package tradingvalidation is the Go port of beansprout's
// trading_validation plugin. It validates that every transaction
// touching a trading account satisfies three balance rules.
//
// # Behavior
//
// A "trading account" is any account whose name equals or starts with
// a configurable prefix (Config string, default "Equity:Trading"),
// matched at account-name boundaries: a prefix P matches the account
// P itself or any sub-account P:<...>, but not a sibling whose name
// merely shares the same leading characters (e.g. Equity:TradingDesk
// is not a trading account when the prefix is Equity:Trading).
//
// For every [ast.Transaction] that contains at least one posting to a
// trading account the plugin checks three rules:
//
//  1. Trading-account postings alone must balance to zero (within
//     tolerance) across all currencies they mention.
//  2. Non-trading-account postings alone must balance to zero (within
//     tolerance) across all currencies they mention.
//  3. For each effective commodity, all postings that belong to that
//     commodity must balance to zero. The effective commodity of a
//     posting is normally its units currency. For commodities whose
//     [ast.Commodity] directive carries the metadata key
//     "trading-account" with the value "disabled", the posting is
//     grouped instead by the price currency of the price annotation.
//     If such a posting has no price annotation it is excluded from
//     rule 3.
//
// Each rule infers its tolerance from only the postings inside that
// rule's subset, not from the whole transaction. The same currency may
// therefore carry different tolerances across rules within one
// transaction, and a coarse-precision posting in one subset does not
// loosen the tolerance applied to another subset.
//
// # Diagnostic codes
//
//   - "trading-not-balanced" — rule 1 or rule 2 failed; one diagnostic
//     per failing currency within the offending sub-group.
//   - "trading-commodity-not-balanced" — rule 3 failed; one diagnostic
//     per failing commodity.
//
// All diagnostics carry [ast.Error] severity and are anchored at the
// offending transaction's Span, falling back to the triggering plugin
// directive's Span.
//
// # Usage
//
// Either registered name activates the plugin:
//
//	plugin "beansprout.plugins.trading_validation"
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/tradingvalidation"
//
// An optional Config string overrides the default account prefix:
//
//	plugin "beansprout.plugins.trading_validation" "Assets:Trading"
package tradingvalidation
