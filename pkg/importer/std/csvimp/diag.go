package csvimp

import "github.com/yugui/go-beancount/pkg/ast"

// Diagnostic codes emitted by [*Importer.Extract]. All carry
// [ast.Error] severity. csvimp emits one diagnostic per failing row
// and skips that row; it never aborts the whole Extract on a per-row
// problem.
const (
	// DiagBadDate signals that the date column failed to parse under
	// the shape's date_format.
	DiagBadDate = "csvimp-bad-date"

	// DiagBadAmount signals that one of the amount columns held a
	// non-blank value that failed decimal parsing.
	DiagBadAmount = "csvimp-bad-amount"

	// DiagAllBlankAmount signals that every amount column for the row
	// held only whitespace.
	DiagAllBlankAmount = "csvimp-all-blank-amount"

	// DiagMissingCurrency signals that the row's currency could be
	// resolved from neither currency_col nor default_currency.
	DiagMissingCurrency = "csvimp-missing-currency"

	// DiagMissingAccount signals that the row's account could be
	// resolved from neither Hints["account"] nor the shape's account.
	DiagMissingAccount = "csvimp-missing-account"

	// DiagMissingColumn signals that a required column declared in the
	// shape was absent from the file's header at Extract time.
	DiagMissingColumn = "csvimp-missing-column"
)

func rowDiag(code, path string, line int, msg string) ast.Diagnostic {
	return ast.Diagnostic{
		Code:     code,
		Span:     ast.Span{Start: ast.Position{Filename: path, Line: line}},
		Message:  msg,
		Severity: ast.Error,
	}
}
