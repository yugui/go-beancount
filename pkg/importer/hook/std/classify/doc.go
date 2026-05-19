// Package classify is the reference classifier hook. It registers a
// [hook.Factory] under the kind "classify"; each factory call produces one
// fully-configured [*Hook] for a declared instance.
//
// # Configuration
//
// Each instance is configured at construction time via the decode callback
// supplied to the factory. The configuration schema is a top-level array of
// rule tables:
//
//	[[rule]]
//	payee_regex = "(?i)acme"
//	account     = "Expenses:Office"
//
//	[[rule]]
//	narration_regex = "(?i)salary"
//	currency        = "USD"
//	account         = "Income:Salary"
//
// Rules are applied in declaration order; the first matching rule wins.
// At least one of payee_regex or narration_regex is required per rule.
// account is required. currency is optional; when omitted the source
// posting's currency is used.
//
// # Apply semantics
//
// For each directive in the input:
//   - Non-Transaction directives pass through unchanged.
//   - Transactions with zero or two-or-more postings pass through unchanged
//     (already-balanced or zero-posting transactions are ignored).
//   - Transactions with exactly one posting are matched against the rule list.
//     The first matching rule's account and currency are forwarded to
//     [github.com/yugui/go-beancount/pkg/importer/importerutil.BalanceWith],
//     which appends a counterpart posting. If no rule matches, a
//     [DiagNoRule] Warning diagnostic is emitted and the transaction passes
//     through unchanged.
//
// # Concurrency
//
// A Hook's internal state is frozen at construction. Apply is safe for
// concurrent invocation on the same value with no external synchronisation.
package classify

import "github.com/yugui/go-beancount/pkg/importer/hook"

func init() {
	hook.RegisterFactory("classify", hook.FactoryFunc(newHook))
}
