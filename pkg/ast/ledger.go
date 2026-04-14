package ast

import (
	"iter"
	"slices"
)

// ledgerEntry pairs a directive with its precomputed sortKey so comparisons
// during binary search and iteration do not reread directive methods.
type ledgerEntry struct {
	dir Directive
	key sortKey
}

// Len reports the number of directives in the ledger.
func (l *Ledger) Len() int {
	if l == nil {
		return 0
	}
	return len(l.entries)
}

// At returns the i-th directive in canonical chronological order.
// It panics if i is out of range, matching slice indexing semantics.
// Calling At on a nil *Ledger panics, consistently with slice indexing
// on a nil slice (any index is out of range).
func (l *Ledger) At(i int) Directive {
	if l == nil {
		panic("ast.Ledger.At: index out of range on nil *Ledger")
	}
	return l.entries[i].dir
}

// All returns an iterator over the ledger's directives in canonical
// chronological order. The iterator is non-destructive: it walks the
// underlying sorted slice, so it is safe to range over repeatedly and to
// exit early via break (honoring the iter.Seq2 contract).
//
// Iteration order matches the (date, kind, filename, offset, seq) total
// ordering described in sortKey.
//
// Do not call Insert or InsertAll while ranging over All(): those methods
// mutate the underlying slice in place (and may reslice its backing array),
// which would make the ongoing iteration yield shifted or duplicated
// entries. To stage new directives during iteration, collect them into a
// local slice and flush with InsertAll after the loop.
func (l *Ledger) All() iter.Seq2[int, Directive] {
	return func(yield func(int, Directive) bool) {
		if l == nil {
			return
		}
		for i, e := range l.entries {
			if !yield(i, e.dir) {
				return
			}
		}
	}
}

// Insert adds d to the ledger while preserving the canonical ordering
// invariant. It is intended for plugin-style additions where directives
// trickle in one at a time.
//
// Cost: one O(log n) binary search plus an O(n) slice shift. For typical
// beancount ledger sizes (thousands to tens of thousands of directives),
// this is measured in microseconds. If a plugin must add a large batch,
// prefer InsertAll to amortize the shift cost.
//
// The sort key's sequence component is assigned here, monotonically, so
// previously-inserted directives never change positions and directives
// inserted at the same (date, kind, filename, offset) are kept in FIFO
// order.
//
// The sort key is computed once, from the directive's DirSpan, DirKind,
// and DirDate at insertion time. Mutating any of those fields on d after
// Insert returns will silently desynchronize it from its cached key and
// break the ordering invariant — treat inserted directives as immutable
// with respect to their sort-key fields.
func (l *Ledger) Insert(d Directive) {
	key := makeSortKey(d, l.nextSeq)
	l.nextSeq++
	// BinarySearchFunc returns the first index at which an existing entry
	// is not less than key. Because seq is unique and monotonically
	// increasing, `found` is never true in practice, but the index is still
	// the correct insertion point.
	idx, _ := slices.BinarySearchFunc(l.entries, key, func(e ledgerEntry, target sortKey) int {
		return compareSortKey(e.key, target)
	})
	l.entries = slices.Insert(l.entries, idx, ledgerEntry{dir: d, key: key})
}

// InsertAll adds each directive in ds while preserving the canonical
// ordering invariant. It is faster than calling Insert in a loop when ds is
// large, because it performs a single sort over the merged slice
// (O((n+m) log(n+m))) instead of m individual O(n) shifts.
//
// Sequence numbers are assigned in the order ds appears, so elements of ds
// that compare equal on (date, kind, filename, offset) retain their input
// order and sort after any entries already in the ledger.
//
// The same mutation caveat as Insert applies: once a directive is handed
// off to InsertAll, its DirSpan/DirKind/DirDate must not change.
func (l *Ledger) InsertAll(ds []Directive) {
	if len(ds) == 0 {
		return
	}
	grown := make([]ledgerEntry, len(l.entries), len(l.entries)+len(ds))
	copy(grown, l.entries)
	for _, d := range ds {
		grown = append(grown, ledgerEntry{dir: d, key: makeSortKey(d, l.nextSeq)})
		l.nextSeq++
	}
	// Every entry has a unique seq, so no two keys compare equal and a
	// non-stable sort is sufficient.
	slices.SortFunc(grown, func(a, b ledgerEntry) int {
		return compareSortKey(a.key, b.key)
	})
	l.entries = grown
}
