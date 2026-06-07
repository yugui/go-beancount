package csvbase

import "github.com/yugui/go-beancount/pkg/ast"

// DiagMissingColumn is the diagnostic code emitted when a column required by
// the RowMapper is absent from the file's header. Severity: Error.
const DiagMissingColumn = "csvbase-missing-column"

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
