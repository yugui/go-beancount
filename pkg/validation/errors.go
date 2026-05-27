// Package validation declares the diagnostic codes emitted by the
// validation plugins. Each code is a stable, machine-readable
// classifier that callers (CLI, IDE plugins, test fixtures) can grep on
// to find the kind of failure that produced a given [ast.Diagnostic].
//
// The codes live on [ast.Diagnostic.Code] as plain strings; the [Code]
// named type is purely a documentation handle on the validation
// namespace's vocabulary.
package validation

// Code identifies a kind of validation diagnostic. Values are surfaced
// on [ast.Diagnostic.Code] as their underlying string; the named type
// exists only to group the constants.
type Code string

const (
	// CodeAccountNotOpen indicates a directive references an account that has never been opened.
	CodeAccountNotOpen Code = "account-not-open"
	// CodeAccountNotYetOpen indicates a directive references an account on a date before its open directive.
	CodeAccountNotYetOpen Code = "account-not-yet-open"
	// CodeAccountClosed indicates a posting references an account after it has been closed.
	CodeAccountClosed Code = "account-closed"
	// CodeDuplicateOpen indicates an account was opened more than once.
	CodeDuplicateOpen Code = "duplicate-open"
	// CodeUnbalancedTransaction indicates a transaction's postings do not sum to zero.
	CodeUnbalancedTransaction Code = "unbalanced-transaction"
	// CodeMultipleAutoPostings indicates a transaction contains more than one posting
	// whose amount must be inferred.
	CodeMultipleAutoPostings Code = "multiple-auto-postings"
	// CodeAutoPostingUnresolved indicates a posting reached validation with a
	// nil Amount. Validation runs on booked AST, so this signals either a
	// skipped booking pass or a regression in booking itself.
	CodeAutoPostingUnresolved Code = "auto-posting-unresolved"
	// CodeBalanceMismatch indicates a balance assertion did not match the computed balance.
	CodeBalanceMismatch Code = "balance-mismatch"
	// CodeDuplicateBalance indicates two balance directives share the same
	// (account, date, currency) but assert different expected amounts. The
	// first-seen amount is retained as the reference; subsequent assertions
	// with conflicting numbers each emit one diagnostic. Same-amount
	// repetitions are silently ignored.
	CodeDuplicateBalance Code = "duplicate-balance"
	// CodePadUnresolved indicates a pad directive could not be resolved to a balance assertion.
	CodePadUnresolved Code = "pad-unresolved"
	// CodePadTargetHasCost indicates a Pad directive targets an account
	// whose inventory holds (or will hold, during the pad → balance window)
	// any cost-bearing position. Pad cannot invent a (Cost, Date, Label)
	// lot identity for the synthetic balancing entry, so the ledger must
	// be rewritten to use explicit transactions for the cost-held lots.
	// See pkg/inventory's "# Lot identity" package doc for the full rationale.
	CodePadTargetHasCost Code = "pad-target-has-cost"
	// CodeCurrencyNotAllowed indicates a posting uses a currency not permitted by the account's open directive.
	CodeCurrencyNotAllowed Code = "currency-not-allowed"
	// CodeInternalError indicates an internal validation failure such as an
	// arithmetic error from the underlying decimal context. These are not
	// user-facing ledger problems but signal a bug or pathological input.
	CodeInternalError Code = "internal-error"
	// CodeInvalidOption indicates a malformed value for a known option key.
	CodeInvalidOption Code = "invalid-option"
)
