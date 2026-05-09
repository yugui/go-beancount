package ast

// Without returns a copy of m that omits any key listed in keys. If none of
// the listed keys are present in m.Props, the receiver is returned unchanged
// (same map pointer) so callers that pass an empty StripMetaKeys list pay no
// allocation cost. The original Metadata is never mutated.
func (m Metadata) Without(keys ...string) Metadata {
	if len(keys) == 0 || len(m.Props) == 0 {
		return m
	}
	// Check whether any listed key is actually present before allocating.
	found := false
	for _, k := range keys {
		if _, ok := m.Props[k]; ok {
			found = true
			break
		}
	}
	if !found {
		return m
	}
	// Build a skip set so the inner loop is O(|Props|) not O(|Props|*|keys|).
	skip := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		skip[k] = struct{}{}
	}
	out := Metadata{Props: make(map[string]MetaValue, len(m.Props)-len(skip))}
	for k, v := range m.Props {
		if _, excluded := skip[k]; !excluded {
			out.Props[k] = v
		}
	}
	return out
}

// StripMetaKeys returns d with all keys in keys removed from its metadata
// (and, for *Transaction, from every posting's metadata). When keys is empty
// or none of the listed keys are present, d is returned unchanged — no
// allocation, no copy. When stripping is needed the directive is
// deep-cloned (metadata only) so the original AST is never mutated.
//
// This function is the canonical emit-time helper: call it just before
// printing a directive whose routing metadata must not appear in the output.
func StripMetaKeys(d Directive, keys []string) Directive {
	if len(keys) == 0 {
		return d
	}
	switch v := d.(type) {
	case *Transaction:
		stripped := v.Clone()
		stripped.Meta = stripped.Meta.Without(keys...)
		for i := range stripped.Postings {
			stripped.Postings[i].Meta = stripped.Postings[i].Meta.Without(keys...)
		}
		return stripped
	case *Open:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Close:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Pad:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Note:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Document:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Price:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Event:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Query:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Custom:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Commodity:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	case *Balance:
		c := v.Clone()
		c.Meta = c.Meta.Without(keys...)
		return c
	default:
		// TODO: if future metadata-bearing directive types are added, they
		// must be handled here; falling through silently would suppress stripping.
		// Non-metadata-bearing directive types (Option, Plugin, Include, etc.)
		// pass through unchanged.
		return d
	}
}
