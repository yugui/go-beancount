package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// DocumentsOver returns a virtual table with the given name: one row per
// Document directive yielded by all, in the sequence order that all produces;
// other directives are skipped. all is called once per [Table.Rows]
// invocation, producing a fresh iterator each time. The returned table is
// immutable and safe for concurrent read (see the package doc).
//
// The `filename` column is the upstream beanquery name for a document's path.
func DocumentsOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: documentColumns,
		Rows:    directiveRows[*ast.Document](all),
	}
}

var documentColumns = []Column{
	dirCol("date", types.Date, func(d *ast.Document) types.Value {
		return nullableDate(d.Date)
	}),
	dirCol("account", types.String, func(d *ast.Document) types.Value {
		return types.NewString(string(d.Account))
	}),
	dirCol("filename", types.String, func(d *ast.Document) types.Value {
		return types.NewString(d.Path)
	}),
	dirCol("tags", types.SetType, func(d *ast.Document) types.Value {
		return types.NewSet(d.Tags...)
	}),
	dirCol("links", types.SetType, func(d *ast.Document) types.Value {
		return types.NewSet(d.Links...)
	}),
	dirCol("meta", types.DictType, func(d *ast.Document) types.Value {
		return metaval.Dict(d.Meta)
	}),
}
