package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// NotesOver returns a virtual table with the given name: one row per Note
// directive yielded by all, in the sequence order that all produces; other
// directives are skipped. all is called once per [Table.Rows] invocation,
// producing a fresh iterator each time. The returned table is immutable and
// safe for concurrent read (see the package doc).
func NotesOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: noteColumns,
		Rows:    directiveRows[*ast.Note](all),
	}
}

var noteColumns = []Column{
	dirCol("date", types.Date, func(n *ast.Note) types.Value {
		return nullableDate(n.Date)
	}),
	dirCol("account", types.String, func(n *ast.Note) types.Value {
		return types.NewString(string(n.Account))
	}),
	dirCol("comment", types.String, func(n *ast.Note) types.Value {
		return types.NewString(n.Comment)
	}),
	dirCol("tags", types.SetType, func(n *ast.Note) types.Value {
		return types.NewSet(n.Tags...)
	}),
	dirCol("links", types.SetType, func(n *ast.Note) types.Value {
		return types.NewSet(n.Links...)
	}),
	dirCol("meta", types.DictType, func(n *ast.Note) types.Value {
		return metaval.Dict(n.Meta)
	}),
}
