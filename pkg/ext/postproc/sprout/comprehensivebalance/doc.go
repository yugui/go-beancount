// Package comprehensivebalance is the Go port of beansprout's
// comprehensive_balance plugin — it expands a Custom directive carrying
// an account and a multi-line list of balance assertions into a set of
// standard Balance directives covering every commodity the account
// could plausibly hold at the Custom's position. Evaluation of those
// Balance directives, including any pad bridging, is delegated to the
// downstream pad→balance pipeline in [pkg/loader] postBuiltins.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/comprehensive_balance.py
//
// # Behavior
//
// On startup the plugin walks every directive and intercepts each
// [ast.Custom] whose TypeName is "comprehensive_balance". A matching
// directive's Values must be exactly two: a [ast.MetaAccount] naming the
// account, followed by a [ast.MetaString] whose value is the multi-line
// body of assertions. Other directives pass through verbatim.
//
// For each intercepted Custom the body is split on '\n'. Lines that are
// empty or whose first non-whitespace character is ';' are skipped.
// Each remaining line is parsed via [ast.ParseBalanceAmount], which
// accepts the full beancount arithmetic-expression grammar plus an
// optional `~ <expr>` tolerance suffix (e.g. "1,000 + 500 USD",
// "319.020 ~ 0.002 RGAGX"). Diagnostics returned by ParseBalanceAmount
// carry byte offsets into the line; the plugin re-anchors them onto the
// enclosing Custom's Span before surfacing.
//
// The set of commodities the plugin asserts against — the "universe"
// for the (account, position) pair — is the union of:
//
//   - currencies that appeared on the account in any [ast.Transaction]
//     posting strictly before the Custom in source order,
//   - currencies that appeared on the account in any [ast.Balance]
//     directive strictly before the Custom in source order,
//   - currencies listed in the Custom's body.
//
// For each currency in the universe, exactly one [ast.Balance] is
// emitted, dated at the Custom's date. Listed currencies carry the
// user's amount and tolerance; currencies in the universe by virtue of
// prior activity but not listed in the body carry amount 0 and nil
// tolerance — the downstream balance plugin will fail those assertions
// if the actual residual is non-zero. The matching Custom is removed
// from the output. Emitted Balance directives are sorted by currency
// for deterministic downstream behavior.
//
// # Pad interaction
//
// Because this plugin emits [ast.Balance] directives, the downstream
// pad plugin treats the Custom's position as a valid pad target. When
// a pad directive on the account precedes a comprehensive_balance
// Custom with no intervening user-written balance, the pad fires
// against the Custom's emitted Balance — which is the intended
// semantic: comprehensive_balance is itself a balance assertion, and
// the user placing it after a pad is asserting "this account must
// equal X here." A later user-written balance on the same account
// evaluates without that pad's help.
//
// # Diagnostic codes
//
//   - "comprehensive-balance-invalid-config": the Custom's Values do
//     not match the (account, string) shape, or the account value is
//     empty.
//   - "comprehensive-balance-duplicate-currency": the body asserts the
//     same currency twice.
//   - "comprehensive-balance-parse": [ast.ParseBalanceAmount] returned
//     one or more diagnostics for a body line. The original code from
//     ast (e.g. "amount-expr-parse", "amount-trailing-input") is
//     preserved in the rebased diagnostic's Message; the surfaced Code
//     identifies the plugin that emitted it.
//
// All diagnostics are anchored at the Custom directive's Span, falling
// back to the triggering plugin directive's Span when the Custom has
// none.
//
// # Usage
//
// Either registered name activates the plugin:
//
//	plugin "beansprout.plugins.comprehensive_balance"
//
// or, equivalently:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/comprehensivebalance"
//
// Example body asserting two currencies; the plugin additionally
// emits a zero-balance assertion for any other commodity the account
// has previously touched:
//
//	2024-01-01 custom "comprehensive_balance" Assets:Checking "
//	  1,000.00 USD
//	  500.00 EUR  ; European holdings
//	  "
//
// # Registered names
//
//   - "beansprout.plugins.comprehensive_balance" — upstream Python
//     module path, so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/comprehensivebalance"
//     — Go import path, matching the project's convention for Go-native
//     plugins.
package comprehensivebalance
