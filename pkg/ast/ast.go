// Package ast defines the abstract syntax tree types for beancount files.
package ast

import (
	"github.com/cockroachdb/apd/v3"
)

// Position represents a source location.
type Position struct {
	Filename string
	Offset   int // byte offset
	Line     int
	Column   int
}

// Span represents a range in source code.
type Span struct {
	Start Position
	End   Position
}

// Severity indicates the severity of a diagnostic. The zero value is
// [Error], so [Diagnostic] literals that omit Severity default to error
// severity.
type Severity int

// Error must keep iota value 0 so [Diagnostic]'s zero value defaults to
// error severity. Reordering or inserting a constant before Error would
// silently flip the meaning of every Diagnostic literal in the codebase.
const (
	// Error indicates a fatal problem.
	Error Severity = iota
	// Warning indicates a non-fatal problem.
	Warning
)

// Diagnostic is the unified ledger-level problem report. It carries
// every issue attributable to ledger contents — parse errors, lowering
// failures, include resolution problems, validation failures, and
// plugin-emitted findings — so callers see one channel of diagnostics
// rather than several.
//
// Code is an optional machine-readable classifier (e.g. "balance-mismatch",
// "plugin-not-registered"). The empty string is permitted for
// diagnostics that have no useful classification. Severity indicates
// whether the diagnostic is fatal (Error) or advisory (Warning); the
// zero value is Error so freshly constructed diagnostics default to
// error severity.
type Diagnostic struct {
	Code     string
	Span     Span
	Message  string
	Severity Severity
}

// File is the result of lowering a single CST file into an AST.
// File.Directives are in source order; the per-file AST is the unit that
// back-end tools (printer, daemon writeback) rewrite.
type File struct {
	Filename    string
	Directives  []Directive
	Diagnostics []Diagnostic
}

// Amount represents a numeric value with a currency.
type Amount struct {
	Number   apd.Decimal
	Currency string
}
