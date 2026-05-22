package csvimp

import "github.com/yugui/go-beancount/pkg/ast"

// Diagnostic codes emitted by [*Importer.Extract]. Most carry
// [ast.Error] severity and cause csvimp to skip the offending row;
// DiagUnmappedCounterAccount is the sole exception and surfaces as a
// warning while the row is still emitted with a single posting.
// csvimp never aborts the whole Extract on a per-row problem.
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

	// DiagUnmappedCounterAccount signals that the joined
	// [counter_account].col cells produced a non-empty key that was
	// absent from [counter_account.map] in strict mode. The row is
	// kept as a single (unbalanced) posting; the diagnostic carries
	// [ast.Warning] severity, surfacing the configuration gap without
	// dropping the transaction. A blank counter key (every cell
	// empty) with no default silently falls back to a single posting
	// and does NOT emit this code.
	DiagUnmappedCounterAccount = "csvimp-unmapped-counter-account"

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

func rowWarn(code, path string, line int, msg string) ast.Diagnostic {
	return ast.Diagnostic{
		Code:     code,
		Span:     ast.Span{Start: ast.Position{Filename: path, Line: line}},
		Message:  msg,
		Severity: ast.Warning,
	}
}
