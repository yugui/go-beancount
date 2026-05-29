package table

import (
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// metaDict coerces directive or posting metadata into a [types.Dict],
// mapping each [ast.MetaValue] to the [types.Value] of the corresponding
// kind. An empty or zero [ast.Metadata] yields an empty Dict (never NULL),
// so the dict-valued meta column is always present.
func metaDict(m ast.Metadata) types.Dict {
	if len(m.Props) == 0 {
		return types.NewDict(nil)
	}
	out := make(map[string]types.Value, len(m.Props))
	for k, mv := range m.Props {
		out[k] = metaValue(mv)
	}
	return types.NewDict(out)
}

func metaValue(mv ast.MetaValue) types.Value {
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
