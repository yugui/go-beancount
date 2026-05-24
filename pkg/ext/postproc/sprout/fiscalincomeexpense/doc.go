// Package fiscalincomeexpense is the Go port of beansprout's
// fiscal_income_expense plugin — it validates that the net change of an
// income or expense account (including sub-accounts) over a fiscal
// period matches a user-declared expected amount, within a tolerance.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/fiscal_income_expense.py
//
// # Behavior
//
// On startup the plugin walks every directive and intercepts each
// [ast.Custom] whose TypeName is "fiscal_income_expense". A matching
// directive's Values must follow one of two shapes:
//
//   - (account, expected) — the begin date defaults to January 1st of
//     the directive's year.
//   - (account, begin_date, expected) — the begin date is explicit.
//
// The directive's own date is the end of the fiscal period; both
// boundary dates are inclusive.
//
// "expected" is either a pre-parsed [ast.MetaAmount] value (e.g.
// `50000 JPY`) or a [ast.MetaString] holding the
// `<amount> [~ <tolerance>] <currency>` form (e.g.
// `"50000 ~ 1 JPY"`). The string form is parsed via
// [ast.ParseBalanceAmount]; diagnostics returned by it are re-anchored
// onto the Custom's Span.
//
// Tolerance handling follows beancount convention. When the user
// supplies `~ <tol>` in the string form, that value is used verbatim.
// Otherwise tolerance is inferred from the expected amount's
// decimal-precision exponent: tolerance = 10^exponent * 0.5. For
// example, `50000 JPY` (exponent 0) yields tolerance 0.5, while
// `50000.00 JPY` (exponent -2) yields 0.005. Whole-number amounts
// (exponent >= 0) use 0.5 directly.
//
// For each intercepted Custom the plugin sums every
// [ast.Transaction] posting against the account or any of its
// sub-accounts whose date falls within `[begin, end]`, in the expected
// currency. If `|actual - expected| > tolerance` it emits a
// "fiscal-income-expense-mismatch" diagnostic. The Custom is removed
// from the output regardless.
//
// # Diagnostic codes
//
//   - "fiscal-income-expense-invalid-config": parameter count, types,
//     or date range are malformed.
//   - "fiscal-income-expense-parse": [ast.ParseBalanceAmount] returned
//     a diagnostic for the string-amount form. The original ast code
//     ("amount-expr-parse", "amount-trailing-input", …) is preserved
//     in the rebased diagnostic's Message; the surfaced Code identifies
//     this plugin.
//   - "fiscal-income-expense-mismatch": actual net change differs from
//     the expected amount by more than the (explicit or inferred)
//     tolerance.
//   - "fiscal-income-expense-cost-on-flow-account" (Warning): a summed
//     posting carries a cost annotation. Lots/costs are uncommon on
//     income/expense (flow) accounts; the cost is ignored when computing
//     the actual, which can diverge from booking-aware realization.
//     Anchored at the offending posting's Span.
//
// Configuration, parse, and mismatch diagnostics are anchored at the
// Custom directive's Span, falling back to the triggering plugin
// directive's Span when the Custom has none.
//
// # Deviations
//
// The "actual" net change is computed by summing each in-range
// [ast.Transaction] posting's raw `Amount.Number` per (account,
// currency), with no booking-aware cost or price reduction. This
// agrees with Python beancount's `realization.realize` output for
// cost-free postings. Income and Expense accounts almost never carry
// cost annotations in practice; ledgers that nevertheless attach costs
// to those accounts may see actuals that differ from upstream
// beansprout's realization-driven sum. To surface that divergence the
// plugin emits a "fiscal-income-expense-cost-on-flow-account" Warning
// for each summed posting that carries a cost annotation. Porting
// full booking-aware realization is out of scope for this plugin.
//
// # Usage
//
// Either registered name activates the plugin:
//
//	plugin "beansprout.plugins.fiscal_income_expense"
//
// or, equivalently:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/fiscalincomeexpense"
//
// Example directives:
//
//	; Explicit begin date.
//	2024-03-31 custom "fiscal_income_expense" Expenses:Food 2023-04-01 50000 JPY
//
//	; Implicit begin date — defaults to 2024-01-01.
//	2024-12-31 custom "fiscal_income_expense" Expenses:Food 50000 JPY
//
//	; Explicit tolerance (string form).
//	2024-12-31 custom "fiscal_income_expense" Expenses:Food "50000 ~ 1 JPY"
//
// # Registered names
//
//   - "beansprout.plugins.fiscal_income_expense" — upstream Python
//     module path, so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/fiscalincomeexpense"
//     — Go import path, matching the project's convention for Go-native
//     plugins.
package fiscalincomeexpense
