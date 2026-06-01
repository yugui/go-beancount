package types

import (
	"slices"
	"strings"
)

// Dict is an immutable, string-keyed map of [Value], modelling directive
// metadata. It is a non-null [Value] of kind [DictType]. The zero Dict is
// a valid empty dict.
//
// A Dict never stores a nil entry value: [NewDict] drops any nil-valued
// entry. A missing key and a key mapped to a typed NULL are distinguished
// by [Dict.Get]'s second result.
type Dict struct {
	keys []string         // invariant: sorted, unique
	m    map[string]Value // nil when empty
}

// NewDict returns a Dict holding a shallow copy of m. The map is copied, so
// later mutation of m does not affect the Dict; the Value entries are
// already immutable and are shared. A nil or empty map yields an empty
// Dict. Entries with a nil Value are dropped.
func NewDict(m map[string]Value) Dict {
	if len(m) == 0 {
		return Dict{}
	}
	cp := make(map[string]Value, len(m))
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		cp[k] = v
		keys = append(keys, k)
	}
	if len(cp) == 0 {
		return Dict{}
	}
	slices.Sort(keys)
	return Dict{keys: keys, m: cp}
}

// Get returns the Value stored under key and true, or (nil, false) when the
// key is absent. It backs getitem/meta lookups. A present key bound to a
// NULL Value returns that NULL with ok=true.
func (d Dict) Get(key string) (Value, bool) {
	v, ok := d.m[key]
	return v, ok
}

// Len returns the number of entries.
func (d Dict) Len() int { return len(d.keys) }

// Keys returns the dict's keys in ascending order. The returned slice is a
// fresh copy.
func (d Dict) Keys() []string { return slices.Clone(d.keys) }

func (Dict) Type() Type   { return DictType }
func (Dict) IsNull() bool { return false }
func (Dict) sealedValue() {}

// Compare orders d against o per the total order documented on
// [Value.Compare]. Dicts compare by their (key, value) pairs in ascending
// key order: keys lexicographically, ties broken by the value order (NULL
// last); a dict that is a key/value prefix of another sorts first. The
// order is deterministic and total but not a meaningful business order.
func (d Dict) Compare(o Value) int { return compare(d, o) }

// Format renders the dict as "{k1: v1, k2: v2}" in ascending key order,
// each value via its Format; the empty dict renders as "{}".
func (d Dict) Format() string {
	if len(d.keys) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range d.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(d.m[k].Format())
	}
	b.WriteByte('}')
	return b.String()
}

// String matches Format.
func (d Dict) String() string { return d.Format() }

func (d Dict) compareTo(o Dict) int {
	n := min(len(d.keys), len(o.keys))
	for i := range n {
		if c := strings.Compare(d.keys[i], o.keys[i]); c != 0 {
			return c
		}
		if c := d.m[d.keys[i]].Compare(o.m[o.keys[i]]); c != 0 {
			return c
		}
	}
	return cmpInt(len(d.keys), len(o.keys))
}

func (d Dict) marshalTree() any {
	out := map[string]any{}
	for _, k := range d.keys {
		out[k] = d.m[k].marshalTree()
	}
	return out
}
