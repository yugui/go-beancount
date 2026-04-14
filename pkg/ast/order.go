package ast

import (
	"cmp"
	"time"
)

// sortKey is the ordering key used by Ledger to maintain its canonical
// chronological invariant.
//
// The lexicographic order is (date, kind, filename, offset, seq):
//
//   - date orders directives by their effective day; directives with a zero
//     DirDate (option, plugin, include) sort before all dated directives.
//   - kind breaks ties within a day using Beancount's canonical same-day
//     processing order (see DirectiveKind).
//   - filename+offset anchors file-originated directives to their physical
//     source location so ordering is stable across loads, independent of
//     traversal order between files.
//   - seq is the final tiebreaker: a monotonic counter assigned when a
//     directive enters the ledger. It makes the ordering a total order even
//     for plugin-generated directives with an empty Span, and it guarantees
//     that insertions never reorder directives that are already present.
type sortKey struct {
	date     time.Time
	kind     DirectiveKind
	filename string
	offset   int
	seq      uint64
}

// makeSortKey derives the sort key for d using the given sequence number.
func makeSortKey(d Directive, seq uint64) sortKey {
	start := d.DirSpan().Start
	return sortKey{
		date:     d.DirDate(),
		kind:     d.DirKind(),
		filename: start.Filename,
		offset:   start.Offset,
		seq:      seq,
	}
}

// compareSortKey returns -1, 0, or +1 in the usual sense. Two keys compare
// equal only when every component matches, which is impossible in practice
// because seq is unique per insertion — but the total-order property is
// what lets slices.BinarySearchFunc pick a deterministic insertion point.
func compareSortKey(a, b sortKey) int {
	if c := a.date.Compare(b.date); c != 0 {
		return c
	}
	if c := cmp.Compare(a.kind, b.kind); c != 0 {
		return c
	}
	if c := cmp.Compare(a.filename, b.filename); c != 0 {
		return c
	}
	if c := cmp.Compare(a.offset, b.offset); c != 0 {
		return c
	}
	return cmp.Compare(a.seq, b.seq)
}
