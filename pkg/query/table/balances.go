package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// BalancesOver returns a virtual table with the given name: one row per Balance
// directive yielded by all, in the sequence order that all produces; other
// directives are skipped. all is called once per [Table.Rows] invocation,
// producing a fresh iterator each time. The returned table is immutable and
// safe for concurrent read (see the package doc).
func BalancesOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: balanceColumns,
		Rows:    directiveRows[*ast.Balance](all),
	}
}

var balanceColumns = []Column{
	dirCol("date", types.Date, func(b *ast.Balance) types.Value {
		return nullableDate(b.Date)
	}),
	dirCol("account", types.String, func(b *ast.Balance) types.Value {
		return types.NewString(string(b.Account))
	}),
	dirCol("amount", types.Amount, func(b *ast.Balance) types.Value {
		return types.NewAmount(b.Amount)
	}),
	dirCol("tolerance", types.Decimal, func(b *ast.Balance) types.Value {
		if b.Tolerance == nil {
			return types.Null(types.Decimal)
		}
		return types.NewDecimal(*b.Tolerance)
	}),
	dirCol("discrepancy", types.Amount, func(b *ast.Balance) types.Value {
		if b.DiffAmount == nil {
			return types.Null(types.Amount)
		}
		return types.NewAmount(*b.DiffAmount)
	}),
	dirCol("meta", types.DictType, func(b *ast.Balance) types.Value {
		return metaval.Dict(b.Meta)
	}),
}
