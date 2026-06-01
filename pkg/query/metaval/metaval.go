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
func Dict(m ast.Metadata) types.Dict { return types.MetaDict(m) }

// Value maps a single [ast.MetaValue] to the [types.Value] of its kind.
// Unrecognized kinds yield a NULL String.
func Value(mv ast.MetaValue) types.Value { return types.MetaValue(mv) }
