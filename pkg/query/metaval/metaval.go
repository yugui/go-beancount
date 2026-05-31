// Package metaval converts beancount directive and posting metadata
// ([ast.Metadata]) into the query value vocabulary ([types.Dict] /
// [types.Value]). It is a leaf shared by the query table columns and the
// directive-context functions so the meta-to-Dict coercion lives in exactly
// one place without crossing the table/types layering boundary.
package metaval

import (
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// Dict coerces directive or posting metadata into a [types.Dict], mapping each
// [ast.MetaValue] to the [types.Value] of the corresponding kind. An empty or
// zero [ast.Metadata] yields an empty Dict (never NULL), so a dict-valued meta
// column or lookup is always present.
func Dict(m ast.Metadata) types.Dict {
	if len(m.Props) == 0 {
		return types.NewDict(nil)
	}
	out := make(map[string]types.Value, len(m.Props))
	for k, mv := range m.Props {
		out[k] = Value(mv)
	}
	return types.NewDict(out)
}

// Value maps a single [ast.MetaValue] to the [types.Value] of its kind.
// Unrecognized kinds yield a NULL String.
func Value(mv ast.MetaValue) types.Value {
	switch mv.Kind {
	case ast.MetaString, ast.MetaAccount, ast.MetaCurrency, ast.MetaTag, ast.MetaLink:
		return types.NewString(mv.String)
	case ast.MetaDate:
		return types.NewDate(mv.Date)
	case ast.MetaNumber:
		return types.NewDecimal(mv.Number)
	case ast.MetaAmount:
		return types.NewAmount(mv.Amount)
	case ast.MetaBool:
		return types.NewBool(mv.Bool)
	default:
		return types.Null(types.String)
	}
}
