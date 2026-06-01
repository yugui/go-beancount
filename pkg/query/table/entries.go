package table

import (
	"iter"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// EntriesOver returns a virtual table with the given name: one row per
// directive yielded by all, in the sequence order that all produces. Each row
// handle is the [ast.Directive] itself; a column a directive type does not
// carry yields a typed NULL. all is called once per [Table.Rows] invocation,
// producing a fresh iterator each time. The returned table is immutable and
// safe for concurrent read (see the package doc).
//
// Use this constructor when the directive source is a scoped view (e.g. a
// [pkg/query/scope.View] result) rather than the full ledger. Callers that
// hold an [*ast.Ledger] should use [Entries].
func EntriesOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: entryColumns,
		Rows: func() iter.Seq[Row] {
			return func(yield func(Row) bool) {
				for _, d := range all() {
					if !yield(d) {
						return
					}
				}
			}
		},
	}
}

// Entries returns the virtual table over the full directive stream: one row
// per directive in l, in the ledger's canonical order. Each row handle is
// the [ast.Directive] itself; a column a directive type does not carry
// yields a typed NULL. The returned table is immutable and safe for
// concurrent read (see the package doc); it holds l by reference and never
// mutates it.
func Entries(l *ast.Ledger) *Table {
	return EntriesOver("entries", l.All)
}

// entryCol builds a [Column] whose accessor receives the row handle already
// asserted to [ast.Directive].
func entryCol(name string, t types.Type, fn func(ast.Directive) types.Value) Column {
	return Column{
		Name: name,
		Type: t,
		Accessor: func(r Row) types.Value {
			return fn(r.(ast.Directive))
		},
	}
}

var entryColumns = []Column{
	entryCol("type", types.String, func(d ast.Directive) types.Value {
		return types.NewString(directiveTypeName(d))
	}),
	entryCol("date", types.Date, func(d ast.Directive) types.Value {
		return nullableDate(d.DirDate())
	}),
	entryCol("year", types.Int, func(d ast.Directive) types.Value {
		return datePart(d.DirDate(), func(t time.Time) int { return t.Year() })
	}),
	entryCol("month", types.Int, func(d ast.Directive) types.Value {
		return datePart(d.DirDate(), func(t time.Time) int { return int(t.Month()) })
	}),
	entryCol("day", types.Int, func(d ast.Directive) types.Value {
		return datePart(d.DirDate(), func(t time.Time) int { return t.Day() })
	}),
	entryCol("filename", types.String, func(d ast.Directive) types.Value {
		return spanFilename(d.DirSpan())
	}),
	entryCol("lineno", types.Int, func(d ast.Directive) types.Value {
		return spanLineno(d.DirSpan())
	}),
	entryCol("flag", types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok && txn.Flag != 0 {
			return flagString(txn.Flag)
		}
		return types.Null(types.String)
	}),
	entryCol("payee", types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return nullableString(txn.Payee)
		}
		return types.Null(types.String)
	}),
	entryCol("narration", types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return types.NewString(txn.Narration)
		}
		return types.Null(types.String)
	}),
	entryCol("tags", types.SetType, func(d ast.Directive) types.Value {
		if tags, ok := directiveTags(d); ok {
			return types.NewSet(tags...)
		}
		return types.Null(types.SetType)
	}),
	entryCol("links", types.SetType, func(d ast.Directive) types.Value {
		if links, ok := directiveLinks(d); ok {
			return types.NewSet(links...)
		}
		return types.Null(types.SetType)
	}),
	entryCol("meta", types.DictType, func(d ast.Directive) types.Value {
		return metaval.Dict(d.DirMeta())
	}),
	// both equal directive meta — no posting concept here
	entryCol("entry_meta", types.DictType, func(d ast.Directive) types.Value {
		return metaval.Dict(d.DirMeta())
	}),
	entryCol("any_meta", types.DictType, func(d ast.Directive) types.Value {
		return metaval.Dict(d.DirMeta())
	}),
	entryCol("id", types.String, func(d ast.Directive) types.Value {
		return types.NewString(entryID(d))
	}),
	entryCol("description", types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return description(txn.Payee, txn.Narration)
		}
		return types.Null(types.String)
	}),
	entryCol("accounts", types.SetType, func(d ast.Directive) types.Value {
		if accts, ok := directiveAccounts(d); ok {
			return types.NewSet(accts...)
		}
		return types.Null(types.SetType)
	}),
}

// directiveTypeName returns the lowercase BQL type name for a directive.
func directiveTypeName(d ast.Directive) string {
	switch d.(type) {
	case *ast.Transaction:
		return "transaction"
	case *ast.Open:
		return "open"
	case *ast.Close:
		return "close"
	case *ast.Balance:
		return "balance"
	case *ast.Pad:
		return "pad"
	case *ast.Price:
		return "price"
	case *ast.Note:
		return "note"
	case *ast.Document:
		return "document"
	case *ast.Commodity:
		return "commodity"
	case *ast.Event:
		return "event"
	case *ast.Query:
		return "query"
	case *ast.Custom:
		return "custom"
	case *ast.Option:
		return "option"
	case *ast.Plugin:
		return "plugin"
	case *ast.Include:
		return "include"
	default:
		return "directive"
	}
}

// directiveTags returns the directive's tags and ok=true for the types that
// carry tags (Transaction, Note, Document); ok=false for types with no tags
// concept.
func directiveTags(d ast.Directive) ([]string, bool) {
	switch v := d.(type) {
	case *ast.Transaction:
		return v.Tags, true
	case *ast.Note:
		return v.Tags, true
	case *ast.Document:
		return v.Tags, true
	default:
		return nil, false
	}
}

func directiveLinks(d ast.Directive) ([]string, bool) {
	switch v := d.(type) {
	case *ast.Transaction:
		return v.Links, true
	case *ast.Note:
		return v.Links, true
	case *ast.Document:
		return v.Links, true
	default:
		return nil, false
	}
}

// directiveAccounts returns the accounts a directive references and ok=true
// for the types that carry an account concept; ok=false for types with none.
// A Transaction yields every posting account; a Pad yields both its target and
// source account.
func directiveAccounts(d ast.Directive) ([]string, bool) {
	switch v := d.(type) {
	case *ast.Transaction:
		accts := make([]string, len(v.Postings))
		for i := range v.Postings {
			accts[i] = string(v.Postings[i].Account)
		}
		return accts, true
	case *ast.Open:
		return []string{string(v.Account)}, true
	case *ast.Close:
		return []string{string(v.Account)}, true
	case *ast.Balance:
		return []string{string(v.Account)}, true
	case *ast.Note:
		return []string{string(v.Account)}, true
	case *ast.Document:
		return []string{string(v.Account)}, true
	case *ast.Pad:
		return []string{string(v.Account), string(v.PadAccount)}, true
	default:
		return nil, false
	}
}

// description joins a transaction's payee and narration with " | ", dropping
// empty parts; it returns a typed-NULL String when both are empty. This is the
// shared form of the upstream `description` column on the postings, entries,
// and transactions tables.
func description(payee, narration string) types.Value {
	switch {
	case payee != "" && narration != "":
		return types.NewString(payee + " | " + narration)
	case payee != "":
		return types.NewString(payee)
	default:
		return nullableString(narration)
	}
}
