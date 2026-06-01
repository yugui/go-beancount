package table

import (
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/metaval"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// EntryAttribute returns the static type and accessor of the directive
// attribute named name (matched case-insensitively), and whether such an
// attribute exists. The accessor is pure over the directive and returns a
// typed NULL for directive kinds that do not carry the attribute, mirroring
// the entries table's NULL convention. It is the purpose-built namespace for
// `<entry>.<attr>` field access in BQL (see pkg/query/exec).
func EntryAttribute(name string) (types.Type, func(ast.Directive) types.Value, bool) {
	a, ok := entryAttributes[strings.ToLower(name)]
	if !ok {
		return types.Invalid, nil, false
	}
	return a.typ, a.get, true
}

type entryAttr struct {
	typ types.Type
	get func(ast.Directive) types.Value
}

var entryAttributes = map[string]entryAttr{
	"type": {types.String, func(d ast.Directive) types.Value {
		return types.NewString(types.DirectiveTypeName(d))
	}},
	"id": {types.String, func(d ast.Directive) types.Value {
		return types.NewString(types.EntryID(d))
	}},
	"date": {types.Date, func(d ast.Directive) types.Value {
		return nullableDate(d.DirDate())
	}},
	"filename": {types.String, func(d ast.Directive) types.Value {
		return spanFilename(d.DirSpan())
	}},
	"lineno": {types.Int, func(d ast.Directive) types.Value {
		return spanLineno(d.DirSpan())
	}},
	"meta": {types.DictType, func(d ast.Directive) types.Value {
		return metaval.Dict(d.DirMeta())
	}},
	"account":  {types.String, attrAccount},
	"accounts": {types.SetType, attrAccounts},
	"currencies": {types.SetType, func(d ast.Directive) types.Value {
		if o, ok := d.(*ast.Open); ok {
			return types.NewSet(o.Currencies...)
		}
		return types.Null(types.SetType)
	}},
	"amount": {types.Amount, attrAmount},
	"narration": {types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return types.NewString(txn.Narration)
		}
		return types.Null(types.String)
	}},
	"payee": {types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return nullableString(txn.Payee)
		}
		return types.Null(types.String)
	}},
	"flag": {types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return flagString(txn.Flag)
		}
		return types.Null(types.String)
	}},
	"tags": {types.SetType, func(d ast.Directive) types.Value {
		if tags, ok := directiveTags(d); ok {
			return types.NewSet(tags...)
		}
		return types.Null(types.SetType)
	}},
	"links": {types.SetType, func(d ast.Directive) types.Value {
		if links, ok := directiveLinks(d); ok {
			return types.NewSet(links...)
		}
		return types.Null(types.SetType)
	}},
	"description": {types.String, func(d ast.Directive) types.Value {
		if txn, ok := d.(*ast.Transaction); ok {
			return description(txn.Payee, txn.Narration)
		}
		return types.Null(types.String)
	}},
}

// attrAccount returns the single account of the directive kinds that carry one
// (Open, Close, Balance, Note, Document, Pad target), or a typed NULL String.
// A Transaction has no single account; use the accounts attribute.
func attrAccount(d ast.Directive) types.Value {
	switch v := d.(type) {
	case *ast.Open:
		return types.NewString(string(v.Account))
	case *ast.Close:
		return types.NewString(string(v.Account))
	case *ast.Balance:
		return types.NewString(string(v.Account))
	case *ast.Note:
		return types.NewString(string(v.Account))
	case *ast.Document:
		return types.NewString(string(v.Account))
	case *ast.Pad:
		return types.NewString(string(v.Account))
	default:
		return types.Null(types.String)
	}
}

func attrAccounts(d ast.Directive) types.Value {
	if accts, ok := directiveAccounts(d); ok {
		return types.NewSet(accts...)
	}
	return types.Null(types.SetType)
}

func attrAmount(d ast.Directive) types.Value {
	switch v := d.(type) {
	case *ast.Balance:
		return types.NewAmount(v.Amount)
	case *ast.Price:
		return types.NewAmount(v.Amount)
	default:
		return types.Null(types.Amount)
	}
}
