package csvimp

import "github.com/yugui/go-beancount/pkg/ast"

// Diagnostic codes emitted by [*Importer.Extract]. All carry
// [ast.Error] severity. csvimp emits one diagnostic per failing row
// and skips that row; it never aborts the whole Extract on a per-row
// problem.
const (
	// DiagBadDate signals that the date column failed to parse under
	// the shape's [date].format.
	DiagBadDate = "csvimp-bad-date"

	// DiagBadAmount signals that one of the amount columns held a
	// non-blank value that failed decimal parsing.
	DiagBadAmount = "csvimp-bad-amount"

	// DiagAllBlankAmount signals that every amount column for the row
	// held only whitespace.
	DiagAllBlankAmount = "csvimp-all-blank-amount"

	// DiagMissingCurrency signals that the row's currency could be
	// resolved from neither [currency].col nor [currency].default.
	DiagMissingCurrency = "csvimp-missing-currency"

	// DiagMissingAccount signals that the row's account could not be
	// resolved from any of Hints["account"], the [account].col cell, or
	// [account].default.
	DiagMissingAccount = "csvimp-missing-account"

	// DiagUnmappedAccount signals that the [account].col cell held a
	// non-blank value that was absent from [account.map]. The row is
	// skipped. This code is only emitted when [account.map] is set
	// (strict mode); when no map is configured the cell value is used
	// verbatim instead.
	DiagUnmappedAccount = "csvimp-unmapped-account"

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
