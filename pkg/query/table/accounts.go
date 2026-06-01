package table

import (
	"iter"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// accountRow is the handle the accounts table yields: an account name with its
// first Open and first Close directive (either pointer may be nil).
type accountRow struct {
	account  string
	openDir  *ast.Open
	closeDir *ast.Close
}

// AccountsOver returns the accounts virtual table with the given name: one row
// per account that has an Open or Close directive in all, in ascending account
// order. Each account maps to its first Open and first Close (later duplicates
// are ignored), mirroring upstream beanquery's get_account_open_close. all is
// called once per [Table.Rows] invocation, producing a fresh iterator each
// time. The returned table is immutable and safe for concurrent read (see the
// package doc).
func AccountsOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table {
	return &Table{
		Name:    name,
		Columns: accountColumns,
		Rows: func() iter.Seq[Row] {
			return func(yield func(Row) bool) {
				for _, r := range collectAccounts(all) {
					if !yield(r) {
						return
					}
				}
			}
		},
	}
}

// Accounts returns the accounts virtual table over the full directive stream of
// l. The returned table is immutable and safe for concurrent read (see the
// package doc); it holds l by reference and never mutates it.
func Accounts(l *ast.Ledger) *Table {
	return AccountsOver("accounts", l.All)
}

func collectAccounts(all func() iter.Seq2[int, ast.Directive]) []accountRow {
	rows := map[ast.Account]*accountRow{}
	at := func(a ast.Account) *accountRow {
		r, ok := rows[a]
		if !ok {
			r = &accountRow{account: string(a)}
			rows[a] = r
		}
		return r
	}
	for _, d := range all() {
		switch v := d.(type) {
		case *ast.Open:
			if r := at(v.Account); r.openDir == nil {
				r.openDir = v
			}
		case *ast.Close:
			if r := at(v.Account); r.closeDir == nil {
				r.closeDir = v
			}
		}
	}
	out := make([]accountRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].account < out[j].account })
	return out
}

func accountCol(name string, t types.Type, fn func(accountRow) types.Value) Column {
	return Column{
		Name: name,
		Type: t,
		Accessor: func(r Row) types.Value {
			return fn(r.(accountRow))
		},
	}
}

var accountColumns = []Column{
	accountCol("account", types.String, func(r accountRow) types.Value {
		return types.NewString(r.account)
	}),
	accountCol("open", types.Entry, func(r accountRow) types.Value {
		if r.openDir == nil {
			return types.Null(types.Entry)
		}
		return types.NewEntry(r.openDir)
	}),
	accountCol("close", types.Entry, func(r accountRow) types.Value {
		if r.closeDir == nil {
			return types.Null(types.Entry)
		}
		return types.NewEntry(r.closeDir)
	}),
}
