package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// CommoditiesOver returns a virtual table with the given name: one row per
// Commodity directive yielded by all, in the sequence order that all produces;
// other directives are skipped. all is called once per [Table.Rows]
// invocation, producing a fresh iterator each time. The returned table is
// immutable and safe for concurrent read (see the package doc).
func CommoditiesOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: commodityColumns,
		Rows:    directiveRows[*ast.Commodity](all),
	}
}

var commodityColumns = []Column{
	dirCol("currency", types.String, func(c *ast.Commodity) types.Value {
		return types.NewString(c.Currency)
	}),
	dirCol("date", types.Date, func(c *ast.Commodity) types.Value {
		return nullableDate(c.Date)
	}),
	dirCol("meta", types.DictType, func(c *ast.Commodity) types.Value {
		return metaval.Dict(c.Meta)
	}),
}
