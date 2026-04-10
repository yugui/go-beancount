// Package ast defines the abstract syntax tree types for beancount files.
package ast

import (
	"time"

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

// Severity indicates the severity of a diagnostic.
type Severity int

const (
	// Error indicates a fatal problem.
	Error Severity = iota
	// Warning indicates a non-fatal problem.
	Warning
)

// Diagnostic represents a problem found during CST→AST lowering.
type Diagnostic struct {
	Span     Span
	Message  string
	Severity Severity
}

// Directive is the interface implemented by all AST directive types.
type Directive interface {
	directive()    // marker method
	DirSpan() Span // DirSpan returns the source span of the directive.
}

// File is the result of lowering a single CST file into an AST.
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

// MetaValueKind tags which field of MetaValue is populated.
type MetaValueKind int

const (
	MetaString   MetaValueKind = iota // MetaString indicates a string value.
	MetaAccount                       // MetaAccount indicates an account name.
	MetaCurrency                      // MetaCurrency indicates a currency code.
	MetaDate                          // MetaDate indicates a date value.
	MetaTag                           // MetaTag indicates a tag value.
	MetaLink                          // MetaLink indicates a link value.
	MetaNumber                        // MetaNumber indicates a numeric value.
	MetaAmount                        // MetaAmount indicates an amount (number + currency).
	MetaBool                          // MetaBool indicates a boolean value.
)

// MetaValue is a tagged union for metadata values.
type MetaValue struct {
	Kind   MetaValueKind
	String string      // MetaString, MetaAccount, MetaCurrency, MetaTag, MetaLink
	Date   time.Time   // MetaDate
	Number apd.Decimal // MetaNumber
	Amount Amount      // MetaAmount
	Bool   bool        // MetaBool
}

// Metadata is a collection of key-value pairs attached to directives or postings.
type Metadata struct {
	// Props holds key-value pairs. Insertion order is not guaranteed.
	Props map[string]MetaValue
}
