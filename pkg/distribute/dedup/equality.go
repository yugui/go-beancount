package dedup

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/yugui/go-beancount/pkg/ast"
)

// decimalCmp equates apd.Decimal values numerically. Its underlying
// big.Int has unexported fields cmp cannot reflect into, so any cmp
// option set that walks decimal-bearing AST nodes must include this.
var decimalCmp = cmp.Comparer(func(a, b apd.Decimal) bool { return a.Cmp(&b) == 0 })

// equalityOpts returns the cmp options that strip cross-cutting fields
// from AST equality: every Span (so spans from different source files
// compare equal), the override metadata key (so a directive that
// gained a route-account hint compares equal to one that did not), and
// numeric apd.Decimal comparison.
//
// Metadata is compared via a dedicated Comparer rather than the
// cmpopts.IgnoreMapEntries filter so that {overrideMetaKey: X} and the
// nil map compare equal: filtering a single-entry map down to zero
// entries yields an empty (non-nil) map, which cmp does not treat as
// equal to a nil map even with cmpopts.EquateEmpty.
//
// overrideMetaKey is parameterized so that 7.5g can flow it from
// Config.TransactionSection.OverrideMetaKey without a signature change.
func equalityOpts(overrideMetaKey string) cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreTypes(ast.Span{}),
		cmp.Comparer(func(a, b ast.Metadata) bool {
			return metadataEqual(a, b, overrideMetaKey)
		}),
		decimalCmp,
	}
}

// metadataEqual reports whether two Metadata values are equal after
// stripping the override key. nil and empty maps compare equal.
func metadataEqual(a, b ast.Metadata, overrideMetaKey string) bool {
	stripped := func(props map[string]ast.MetaValue) map[string]ast.MetaValue {
		if overrideMetaKey == "" {
			return props
		}
		if _, ok := props[overrideMetaKey]; !ok {
			return props
		}
		out := make(map[string]ast.MetaValue, len(props))
		for k, v := range props {
			if k == overrideMetaKey {
				continue
			}
			out[k] = v
		}
		return out
	}
	pa, pb := stripped(a.Props), stripped(b.Props)
	if len(pa) != len(pb) {
		return false
	}
	for k, va := range pa {
		vb, ok := pb[k]
		if !ok {
			return false
		}
		if !cmp.Equal(va, vb, decimalCmp) {
			return false
		}
	}
	return true
}

// equivalent reports whether a and b are equivalent under the design's
// OR-combined rule. AST equality (with Span and the override key
// stripped) wins first; otherwise a metadata-key match against any of
// eqKeys produces MatchMeta. MatchNone otherwise.
func equivalent(a, b ast.Directive, overrideMetaKey string, eqKeys []string) MatchKind {
	if cmp.Equal(a, b, equalityOpts(overrideMetaKey)...) {
		return MatchAST
	}
	if metaMatch(a, b, eqKeys) {
		return MatchMeta
	}
	return MatchNone
}

// metaMatch reports whether a and b carry the same value under any key
// listed in eqKeys. The first match wins.
func metaMatch(a, b ast.Directive, eqKeys []string) bool {
	if len(eqKeys) == 0 {
		return false
	}
	ma, mb := metadataOf(a), metadataOf(b)
	if ma == nil || mb == nil {
		return false
	}
	for _, k := range eqKeys {
		va, oka := ma.Props[k]
		vb, okb := mb.Props[k]
		if !oka || !okb {
			continue
		}
		if cmp.Equal(va, vb, decimalCmp) {
			return true
		}
	}
	return false
}

// metadataOf returns the metadata pointer of a directive that carries
// metadata, or nil for directive types that do not.
func metadataOf(d ast.Directive) *ast.Metadata {
	switch v := d.(type) {
	case *ast.Open:
		return &v.Meta
	case *ast.Close:
		return &v.Meta
	case *ast.Commodity:
		return &v.Meta
	case *ast.Balance:
		return &v.Meta
	case *ast.Pad:
		return &v.Meta
	case *ast.Note:
		return &v.Meta
	case *ast.Document:
		return &v.Meta
	case *ast.Price:
		return &v.Meta
	case *ast.Event:
		return &v.Meta
	case *ast.Query:
		return &v.Meta
	case *ast.Custom:
		return &v.Meta
	case *ast.Transaction:
		return &v.Meta
	}
	return nil
}
