// Package ast defines the abstract syntax tree types for beancount files.
package ast

import (
	"fmt"

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
//
// Diagnostic is a pure data type: it deliberately does NOT implement
// the [error] interface. The two channels are kept structurally
// distinct throughout the pipeline:
//
//   - Input-data findings — every problem attributable to the ledger
//     under analysis — flow as [Diagnostic] values (typically through
//     a [`[]Diagnostic`] slot or a `*Diagnostic` "optional finding"
//     return).
//   - System failures unrelated to ledger contents (I/O, context
//     cancellation, implementation bugs) flow through a separate
//     `error` return.
//
// Functions that may produce either return both in separate slots
// (e.g. `(result, *Diagnostic, error)`) so callers cannot confuse a
// finding for a bug and vice versa. CLI rendering goes through the
// [fmt.Stringer] implementation below; the canonical greppable shape
// is the contract.
type Diagnostic struct {
	Code     string
	Span     Span
	Message  string
	Severity Severity
}

// String renders the diagnostic in the canonical greppable form
//
//	<path>:<line>:<col>: <severity>: <message> [<code>]
//
// omitting the location prefix when [Span.Start.Filename] is empty and
// the trailing `[<code>]` when [Code] is empty. The format is part of
// the diagnostic contract: tools that grep CLI output rely on it.
func (d Diagnostic) String() string {
	sev := "error"
	if d.Severity == Warning {
		sev = "warning"
	}
	msg := d.Message
	if d.Code != "" {
		msg = fmt.Sprintf("%s [%s]", msg, d.Code)
	}
	pos := d.Span.Start
	if pos.Filename == "" {
		return fmt.Sprintf("%s: %s", sev, msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s", pos.Filename, pos.Line, pos.Column, sev, msg)
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
