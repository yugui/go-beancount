// Package classify is the reference classifier hook. It registers a
// process-global [*Hook] under the canonical short name "classify" and
// under its Go fully-qualified package path; either lookup returns the
// same instance.
//
// # Configuration
//
// The hook is encoding-agnostic: its [*Hook.Configure] method takes a
// decoder closure supplied by the caller (typically the CLI's TOML loader).
// The configuration schema is a top-level array of rule tables:
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
// Configure and Apply on the same instance must be serialised by the caller;
// the hook.Chain runner provides that serialisation by construction.
// Configure may be called multiple times in sequence; a failed Configure
// leaves any previously-installed rule list intact.
package classify

import "github.com/yugui/go-beancount/pkg/importer/hook"

func init() {
	h := &Hook{}
	hook.Register("classify", h)
	hook.Register("github.com/yugui/go-beancount/pkg/importer/hook/std/classify", h)
}
