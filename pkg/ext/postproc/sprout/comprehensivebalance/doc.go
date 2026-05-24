// Package comprehensivebalance is the Go port of beansprout's
// comprehensive_balance plugin — it expands a Custom directive carrying
// an account and a multi-line list of balance assertions into a set of
// standard Balance directives, and synthesizes zero-balance assertions
// for any commodity the account holds that the user did not list.
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
// For every parsed assertion the plugin emits a synthetic [ast.Balance]
// directive at the Custom's date and account, copying the parsed amount
// and tolerance. In addition, for every commodity the account currently
// holds (computed by summing every preceding [ast.Transaction] posting
// on the account in directive-sequence order, up to but not past the
// Custom directive itself) that the body did NOT list, the plugin
// emits a zero-balance assertion of that commodity. Commodities whose
// current balance is zero are not listed and require no assertion. The
// matching Custom is removed from the output.
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
// # Deviations
//
// "What commodities does the account currently hold?" is computed by
// summing each preceding [ast.Transaction] posting's raw
// `Amount.Number` per (account, currency), with no booking-aware cost
// or price reduction. This agrees with Python beancount's
// `realization.realize` output for cost-free postings, which is the
// common case for comprehensive_balance's intended targets (cash,
// checking, simple commodity inventories). Ledgers that carry cost
// annotations or per-leg `@ price` reductions on the asserted account
// may see a different set of "missing" commodities than upstream
// beansprout, which delegates to realization to consume those cost
// legs. Porting full booking-aware realization is out of scope for
// this plugin.
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
// Example body asserting two currencies and (implicitly) zero of any
// other commodity the account holds:
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
