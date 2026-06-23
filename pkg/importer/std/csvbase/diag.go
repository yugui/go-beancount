package csvbase

import "github.com/yugui/go-beancount/pkg/ast"

// DiagMissingColumn is the diagnostic code emitted when a column required by
// the RowMapper is absent from the file's header. Severity: Error.
const DiagMissingColumn = "csvbase-missing-column"

// DiagBadDate signals that a date column failed to parse under the configured
// layout. Severity: Error.
const DiagBadDate = "csvbase-bad-date"

// DiagBadAmount signals that an amount column held a non-blank value that
// failed decimal parsing. Severity: Error.
const DiagBadAmount = "csvbase-bad-amount"

// DiagAllBlankAmount signals that every amount column was blank or a
// placeholder. Severity: Error.
const DiagAllBlankAmount = "csvbase-all-blank-amount"

// DiagMissingCurrency signals that no currency could be resolved from any
// configured source. Severity: Error.
const DiagMissingCurrency = "csvbase-missing-currency"

// DiagMissingAccount signals that the primary account could not be resolved
// from any configured source. Severity: Error.
const DiagMissingAccount = "csvbase-missing-account"

// DiagUnmappedAccount signals that the account key was absent from a strict
// account map; the row is dropped. Severity: Error.
const DiagUnmappedAccount = "csvbase-unmapped-account"

// DiagUnmappedCounterAccount signals that the counter-account key was absent
// from a strict counter map; the row is kept with a single posting. Severity: Warning.
const DiagUnmappedCounterAccount = "csvbase-unmapped-counter-account"

// DiagBadTemplate signals that a Template failed to render for the row.
// Severity: Error.
const DiagBadTemplate = "csvbase-bad-template"

// DiagBadCost signals that the cost spec could not be built: unparseable
// number, missing currency, or unparseable date. Severity: Error.
const DiagBadCost = "csvbase-bad-cost"

// DiagNoPostings signals that an assembled transaction had no postings.
// Severity: Error.
const DiagNoPostings = "csvbase-no-postings"

// ErrorDiag builds an Error-severity ast.Diagnostic located at path:line.
// The span covers only the start position; End is zero.
func ErrorDiag(code, path string, line int, msg string) ast.Diagnostic {
	return ast.Diagnostic{
		Code:     code,
		Span:     ast.Span{Start: ast.Position{Filename: path, Line: line}},
		Message:  msg,
		Severity: ast.Error,
	}
}

// WarnDiag builds a Warning-severity ast.Diagnostic located at path:line.
// The span covers only the start position; End is zero.
func WarnDiag(code, path string, line int, msg string) ast.Diagnostic {
	return ast.Diagnostic{
		Code:     code,
		Span:     ast.Span{Start: ast.Position{Filename: path, Line: line}},
		Message:  msg,
		Severity: ast.Warning,
	}
}
