package scope

import (
	"iter"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Spec describes the entry-stream scoping applied by [View]. The zero value
// means no scoping: View returns the ledger's full directive sequence
// unchanged.
//
// Open and Close are UTC-midnight dates (as produced by the BQL parser).
// A zero Open or Close means that clause is absent. Clear requests
// income/expense balance transfers at the boundary date. The boundary defaults
// to (Close − 1 day) if Close is set, else the last entry's date, else today
// (see Step 6).
type Spec struct {
	Open  time.Time
	Close time.Time
	Clear bool
}

// View returns an iterator over the directives in l after applying the
// scoping in s. Each call returns a fresh iterator that allocates only its
// own iteration state; the underlying ledger is never mutated.
//
// View accepts a nil ledger and yields nothing in that case.
//
// Indices in the returned sequence are dense 0-based (re-indexed); they do
// not correspond to the original ledger positions.
//
// When s is the zero Spec, View returns l.All() directly with no
// additional allocation or wrapping.
//
// CLOSE ON D (s.Close non-zero): directives with DirDate() >= s.Close are
// dropped. The predicate is strict less-than, matching beanquery's
// summarize.truncate semantics.
//
// OPEN ON and CLEAR are not yet implemented (Steps 5 and 6); they must be
// rejected at compile time before reaching View.
func View(l *ast.Ledger, s Spec) iter.Seq2[int, ast.Directive] {
	if s == (Spec{}) {
		return l.All()
	}
	return func(yield func(int, ast.Directive) bool) {
		idx := 0
		for _, d := range l.All() {
			if !s.Close.IsZero() && !d.DirDate().Before(s.Close) {
				continue
			}
			if !yield(idx, d) {
				return
			}
			idx++
		}
	}
}
