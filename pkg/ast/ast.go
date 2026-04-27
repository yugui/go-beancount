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

// DirectiveKind assigns a canonical same-day processing priority to each
// directive type. Lower values sort earlier.
//
// Beancount processes same-day directives in a specific order so that, for
// example, a balance assertion is evaluated against the opening balance of
// the day (before any transactions posted that day). The order used here
// matches Beancount's canonical order:
//
//  1. open
//  2. balance
//  3. pad
//  4. transaction
//  5. note / document / event / commodity / query / custom
//  6. close
//  7. price
//
// Directives without a date (option, plugin, include) use KindFileHeader and
// sort before dated directives via their zero DirDate.
type DirectiveKind int

const (
	// KindFileHeader covers directives without an intrinsic date
	// (option, plugin, include).
	KindFileHeader DirectiveKind = iota
	// KindOpen is the canonical kind for account opening directives.
	KindOpen
	// KindBalance is the canonical kind for balance assertions.
	KindBalance
	// KindPad is the canonical kind for pad directives.
	KindPad
	// KindTransaction is the canonical kind for transactions.
	KindTransaction
	// KindOther covers directives (note, document, event, commodity,
	// query, custom) that share an ordering slot between transactions
	// and close directives.
	KindOther
	// KindClose is the canonical kind for account close directives.
	KindClose
	// KindPrice is the canonical kind for price directives.
	KindPrice
)

// Directive is the interface implemented by all AST directive types.
//
// Every Directive carries the metadata needed to place it in canonical
// order: a source span, a directive kind, and an effective date (zero for
// header directives). Because directive() is unexported, the interface can
// only be satisfied by types defined in this package, which keeps the kind
// and date contracts closed to external extension.
type Directive interface {
	directive()             // marker method
	DirSpan() Span          // DirSpan returns the source span of the directive.
	DirKind() DirectiveKind // DirKind returns the canonical kind for ordering.
	DirDate() time.Time     // DirDate returns the effective date, or zero for header directives.
}

// File is the result of lowering a single CST file into an AST.
// File.Directives are in source order; the per-file AST is the unit that
// back-end tools (printer, daemon writeback) rewrite.
type File struct {
	Filename    string
	Directives  []Directive
	Diagnostics []Diagnostic
}

// Ledger is the result of loading a beancount ledger, including all
// transitively included files. Unlike File.Directives, the Ledger exposes
// directives only through chronological iteration via All, Len, and At, and
// maintains them in canonical order as an invariant. Use Insert or InsertAll
// to add plugin-generated directives without breaking that invariant.
type Ledger struct {
	Files       []*File      // all files in load order (root first)
	Diagnostics []Diagnostic // merged diagnostics from all files

	// entries holds directives in canonical (sortKey) order. The slice is
	// kept sorted as an invariant; Insert and InsertAll preserve it.
	entries []ledgerEntry
	// nextSeq is the next monotonic sequence number to assign. It serves
	// as the final tiebreaker in sortKey so plugin-generated directives
	// without source spans still land at deterministic positions and
	// preserve FIFO insertion order among themselves.
	nextSeq uint64
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
