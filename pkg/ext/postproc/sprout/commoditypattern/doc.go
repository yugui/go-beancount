// Package commoditypattern is the Go port of beansprout's commodity_pattern
// plugin — it validates that each transaction posting's currency matches a
// regex pattern declared on the account's Open directive.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/commodity_pattern.py
//
// # Behavior
//
// On startup the plugin scans all [ast.Open] directives for a
// "commodity-pattern" metadata key. Its value is compiled as a Go regular
// expression (stdlib "regexp"). If the pattern is syntactically invalid the
// plugin immediately emits a diagnostic with code "commodity-pattern-invalid-regexp"
// and returns without validating any transactions.
//
// For each [ast.Transaction] posting whose account has a registered pattern,
// the posting's currency is tested with a full match (regexp.Regexp.MatchString
// anchored via \A…\z, equivalent to Python's re.fullmatch). A mismatch
// produces a diagnostic with code "commodity-pattern-mismatch". Postings
// without a units amount (auto-balanced legs) and postings against accounts
// that carry no "commodity-pattern" metadata are silently skipped.
//
// The plugin is diagnostic-only: it returns nil Result.Directives, leaving the
// ledger unchanged.
//
// # Deviations
//
// Patterns are compiled with Go's stdlib regexp engine (RE2), not
// Python's `re` module. RE2 deliberately omits backreferences, lookaround
// assertions, and a handful of other PCRE-only constructs; any pattern
// using those features will fail to compile and surface as a
// "commodity-pattern-invalid-regexp" diagnostic.
//
// # Diagnostic span selection
//
// For mismatch diagnostics the preference order is:
//  1. the posting's own Span;
//  2. the enclosing transaction's Span;
//  3. the triggering plugin directive's Span.
//
// Invalid-regexp diagnostics are anchored at the Open directive's Span,
// falling back to the plugin directive's Span.
//
// # Usage
//
// The plugin takes no Config string; only the per-account metadata drives it.
// Either registered name works:
//
//	plugin "beansprout.plugins.commodity_pattern"
//
// or, equivalently:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/commoditypattern"
//
// Example ledger:
//
//	plugin "beansprout.plugins.commodity_pattern"
//
//	2020-01-01 open Assets:Stocks:US
//	  commodity-pattern: "STOCK-[A-Z]+"
//
//	2020-01-02 * "Buy stock"
//	  Assets:Stocks:US  10 STOCK-AAPL
//	  Assets:Cash      -1000 USD
//
// The second transaction is valid because STOCK-AAPL fully matches
// "STOCK-[A-Z]+". A posting of 10 AAPL would yield:
//
//	[commodity-pattern-mismatch] Commodity 'AAPL' in account 'Assets:Stocks:US' does not match pattern 'STOCK-[A-Z]+'
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.commodity_pattern" — upstream Python module path, so
//     existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/commoditypattern" —
//     Go import path, matching the project's convention for Go-native plugins.
package commoditypattern
