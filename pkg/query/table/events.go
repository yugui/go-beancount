package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// EventsOver returns a virtual table with the given name: one row per Event
// directive yielded by all, in the sequence order that all produces; other
// directives are skipped. all is called once per [Table.Rows] invocation,
// producing a fresh iterator each time. The returned table is immutable and
// safe for concurrent read (see the package doc).
//
// The `type` and `description` columns are the upstream beanquery names for an
// event's name/value pair.
func EventsOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: eventColumns,
		Rows:    directiveRows[*ast.Event](all),
	}
}

var eventColumns = []Column{
	dirCol("date", types.Date, func(e *ast.Event) types.Value {
		return nullableDate(e.Date)
	}),
	dirCol("type", types.String, func(e *ast.Event) types.Value {
		return types.NewString(e.Name)
	}),
	dirCol("description", types.String, func(e *ast.Event) types.Value {
		return types.NewString(e.Value)
	}),
	dirCol("meta", types.DictType, func(e *ast.Event) types.Value {
		return metaval.Dict(e.Meta)
	}),
}
