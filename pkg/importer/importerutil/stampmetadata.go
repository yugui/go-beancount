package importerutil

import (
	"github.com/yugui/go-beancount/pkg/ast"
)

// StampMetadata sets key to a MetaString value on d's Meta field. When the
// key already holds the same string value, d is returned aliased without
// allocation (idempotent). When key is absent or holds a different value, the
// directive is deep-cloned and the entry written. key is not validated; a
// malformed key will fail downstream, not here.
//
// No-op cases — the input directive is returned aliased, without allocation:
//   - d is *ast.Option, *ast.Plugin, or *ast.Include (no Meta field).
//   - d is nil.
func StampMetadata(d ast.Directive, key string, value string) ast.Directive {
	if d == nil {
		return nil
	}
	desired := ast.MetaValue{Kind: ast.MetaString, String: value}
	switch v := d.(type) {
	case *ast.Transaction:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone() // Transaction.Clone already deep-copies Meta
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Open:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Close:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Balance:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Pad:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Note:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Document:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Price:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Commodity:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Event:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Query:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	case *ast.Custom:
		if isAlready(v.Meta, key, desired) {
			return d
		}
		c := v.Clone()
		c.Meta = forkMeta(c.Meta)
		setMetaProp(&c.Meta, key, desired)
		return c
	default:
		return d
	}
}

func isAlready(m ast.Metadata, key string, desired ast.MetaValue) bool {
	existing, ok := m.Props[key]
	// MetaValue is struct-comparable
	return ok && existing == desired
}

// forkMeta returns a Metadata with a freshly allocated Props map containing
// the same entries as m.Props. The returned map shares no storage with m, so
// callers may add, overwrite, or delete keys without affecting the input.
// This compensates for ast.*.Clone() on non-Transaction directives, which
// share the Meta.Props map with the receiver by convention.
func forkMeta(m ast.Metadata) ast.Metadata {
	if m.Props == nil {
		return ast.Metadata{}
	}
	out := make(map[string]ast.MetaValue, len(m.Props)+1)
	for k, v := range m.Props {
		out[k] = v
	}
	return ast.Metadata{Props: out}
}

// setMetaProp writes v at key in m.Props. When m.Props is nil it is lazily
// allocated, so callers may stamp onto a directive whose Metadata has not yet
// been populated.
func setMetaProp(m *ast.Metadata, key string, v ast.MetaValue) {
	if m.Props == nil {
		m.Props = make(map[string]ast.MetaValue, 1)
	}
	m.Props[key] = v
}
