package table

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// TransactionsOver returns a virtual table with the given name: one row per
// Transaction directive yielded by all, in the sequence order that all
// produces; other directives are skipped. all is called once per [Table.Rows]
// invocation, producing a fresh iterator each time. The returned table is
// immutable and safe for concurrent read (see the package doc).
//
// Unlike [Postings], a row is the whole transaction, not one of its postings;
// like upstream beanquery there is no `postings` column. The `accounts` column
// is the set of all posting accounts.
func TransactionsOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: transactionColumns,
		Rows:    directiveRows[*ast.Transaction](all),
	}
}

var transactionColumns = []Column{
	dirCol("date", types.Date, func(t *ast.Transaction) types.Value {
		return nullableDate(t.Date)
	}),
	dirCol("flag", types.String, func(t *ast.Transaction) types.Value {
		return flagString(t.Flag)
	}),
	dirCol("payee", types.String, func(t *ast.Transaction) types.Value {
		return nullableString(t.Payee)
	}),
	dirCol("narration", types.String, func(t *ast.Transaction) types.Value {
		return types.NewString(t.Narration)
	}),
	dirCol("description", types.String, func(t *ast.Transaction) types.Value {
		return description(t.Payee, t.Narration)
	}),
	dirCol("tags", types.SetType, func(t *ast.Transaction) types.Value {
		return types.NewSet(t.Tags...)
	}),
	dirCol("links", types.SetType, func(t *ast.Transaction) types.Value {
		return types.NewSet(t.Links...)
	}),
	dirCol("accounts", types.SetType, func(t *ast.Transaction) types.Value {
		return types.NewSet(txnAccounts(t, -1)...)
	}),
	dirCol("meta", types.DictType, func(t *ast.Transaction) types.Value {
		return metaval.Dict(t.Meta)
	}),
}
