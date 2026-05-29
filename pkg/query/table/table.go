package table

import (
	"iter"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// spanFilename returns the source filename from a span as a String value, or
// a typed NULL when the span carries no filename.
func spanFilename(s ast.Span) types.Value {
	return nullableString(s.Start.Filename)
}

// spanLineno returns the 1-based source line from a span as an Int value, or
// a typed NULL when the span carries no position (Line == 0).
func spanLineno(s ast.Span) types.Value {
	if s.Start.Line == 0 {
		return types.Null(types.Int)
	}
	return types.NewInt(int64(s.Start.Line))
}

// flagString renders a single flag byte as a one-character String value, or a
// typed NULL when the byte is 0 (no flag).
func flagString(b byte) types.Value {
	if b == 0 {
		return types.Null(types.String)
	}
	return types.NewString(string(b))
}

// nullableString returns a String value, or a typed NULL when s is empty.
func nullableString(s string) types.Value {
	if s == "" {
		return types.Null(types.String)
	}
	return types.NewString(s)
}

// nullableDate returns a Date value, or a typed NULL when t is the zero time
// (header directives carry no date).
func nullableDate(t time.Time) types.Value {
	if t.IsZero() {
		return types.Null(types.Date)
	}
	return types.NewDate(t)
}

// datePart returns the int component selected by extract as an Int value, or a
// typed NULL when t is the zero time.
func datePart(t time.Time, extract func(time.Time) int) types.Value {
	if t.IsZero() {
		return types.Null(types.Int)
	}
	return types.NewInt(int64(extract(t)))
}

// Row is an opaque per-table row handle. Each table's accessors understand
// only the concrete handle type that its own [Table.Rows] sequence yields;
// they must not be applied to handles from a different table.
type Row = any

// Column is a named, statically-typed projection of a [Row]. Type is the
// column's static [types.Type]; every value Accessor returns has this Type,
// including NULLs (a typed [types.Null]). Accessor is a pure function of the
// row handle: it depends only on the handle and never mutates shared state,
// so it is safe to call from many goroutines concurrently.
type Column struct {
	Name     string
	Type     types.Type
	Accessor func(Row) types.Value
}

// Table is a virtual table: a name, an ordered set of typed [Column]s, and a
// factory for a lazy row sequence.
//
// A Table is immutable after construction. Rows returns a NEW iterator each
// call and shares no mutable state with prior iterators; accessors are pure
// over the row handle. Consequently many goroutines may iterate one Table
// built over one shared immutable [github.com/yugui/go-beancount/pkg/ast.Ledger]
// with no locking, and an iterator may be abandoned early (break) without
// materializing the remaining rows.
type Table struct {
	Name    string
	Columns []Column
	Rows    func() iter.Seq[Row]
}

// Column returns the column named name and true, matched case-insensitively
// ("ACCOUNT" finds "account"). It returns the zero Column and false when no
// column matches.
func (t *Table) Column(name string) (Column, bool) {
	for _, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Column{}, false
}
