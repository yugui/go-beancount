package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// PricesOver returns a virtual table with the given name: one row per Price
// directive yielded by all, in the sequence order that all produces; other
// directives are skipped. all is called once per [Table.Rows] invocation,
// producing a fresh iterator each time. The returned table is immutable and
// safe for concurrent read (see the package doc).
func PricesOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: priceColumns,
		Rows:    directiveRows[*ast.Price](all),
	}
}

var priceColumns = []Column{
	dirCol("date", types.Date, func(p *ast.Price) types.Value {
		return nullableDate(p.Date)
	}),
	dirCol("currency", types.String, func(p *ast.Price) types.Value {
		return types.NewString(p.Commodity)
	}),
	dirCol("amount", types.Amount, func(p *ast.Price) types.Value {
		return types.NewAmount(p.Amount)
	}),
	dirCol("meta", types.DictType, func(p *ast.Price) types.Value {
		return metaval.Dict(p.Meta)
	}),
}
