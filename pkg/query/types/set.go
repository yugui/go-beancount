package types

import (
	"slices"
	"strings"
)

// Set is an immutable, ordered, deduplicated set of strings, modelling a
// transaction's tags or links. It is a non-null [Value] of kind [Set].
//
// Construction normalizes the elements (sorted, unique), so two Sets built
// from the same elements in any order are equal and compare 0. The zero
// Set is a valid empty set.
type Set struct {
	elems []string // invariant: sorted, unique; nil when empty
}

// NewSet returns a Set containing the distinct elements of elems. The input
// is copied, so later mutation of elems does not affect the Set.
func NewSet(elems ...string) Set {
	if len(elems) == 0 {
		return Set{}
	}
	sorted := slices.Clone(elems)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	return Set{elems: sorted}
}

// Contains reports whether s holds the element. It backs the IN operator
// over a set.
func (s Set) Contains(elem string) bool {
	_, found := slices.BinarySearch(s.elems, elem)
	return found
}

// Len returns the number of distinct elements.
func (s Set) Len() int { return len(s.elems) }

// Elements returns the set's elements in ascending order. The returned
// slice is a fresh copy the caller may retain or mutate.
func (s Set) Elements() []string { return slices.Clone(s.elems) }

func (Set) Type() Type   { return SetType }
func (Set) IsNull() bool { return false }
func (Set) sealedValue() {}

// Compare orders s against o per the total order documented on
// [Value.Compare]. Two Sets are ordered by their sorted elements
// lexicographically, a shorter set that is a prefix of a longer one
// sorting first.
func (s Set) Compare(o Value) int { return compare(s, o) }

// Format renders the set as "{a, b, c}" with elements in ascending order;
// the empty set renders as "{}".
func (s Set) Format() string {
	return "{" + strings.Join(s.elems, ", ") + "}"
}

// String matches Format.
func (s Set) String() string { return s.Format() }

func (s Set) compareTo(o Set) int {
	return slices.Compare(s.elems, o.elems)
}

func (s Set) marshalTree() any {
	out := make([]any, len(s.elems))
	for i, e := range s.elems {
		out[i] = e
	}
	return out
}
