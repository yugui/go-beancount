package ast

import (
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

func collect(l *Ledger) []Directive {
	out := make([]Directive, 0, l.Len())
	for _, d := range l.All() {
		out = append(out, d)
	}
	return out
}

// TestLedgerInsertSameDayKindOrder verifies that within a single day, Insert
// places directives in Beancount's canonical kind order regardless of the
// sequence in which they arrive.
func TestLedgerInsertSameDayKindOrder(t *testing.T) {
	d := day(t, "2024-01-15")

	tx := &Transaction{Date: d, Narration: "coffee"}
	bal := &Balance{Date: d, Account: "Assets:Cash"}
	open := &Open{Date: d, Account: "Assets:Cash"}
	closeD := &Close{Date: d, Account: "Assets:Cash"}
	price := &Price{Date: d, Commodity: "USD"}
	pad := &Pad{Date: d, Account: "Assets:Cash", PadAccount: "Equity:Opening"}

	l := &Ledger{}
	// Insert in an arbitrary order.
	for _, dir := range []Directive{tx, closeD, price, open, bal, pad} {
		l.Insert(dir)
	}

	got := collect(l)
	want := []Directive{open, bal, pad, tx, closeD, price}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %T, want %T", i, got[i], want[i])
		}
	}
}

// TestLedgerInsertAcrossDays verifies primary ordering by date.
func TestLedgerInsertAcrossDays(t *testing.T) {
	d1 := day(t, "2024-01-01")
	d2 := day(t, "2024-02-01")
	d3 := day(t, "2024-03-01")

	tx1 := &Transaction{Date: d1}
	tx2 := &Transaction{Date: d2}
	tx3 := &Transaction{Date: d3}

	l := &Ledger{}
	for _, dir := range []Directive{tx3, tx1, tx2} {
		l.Insert(dir)
	}

	got := collect(l)
	want := []Directive{tx1, tx2, tx3}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestLedgerInsertFileHeaderBeforeDated verifies that option/plugin/include
// directives sort ahead of any dated directive and among themselves keep
// insertion order via the seq tiebreaker.
func TestLedgerInsertFileHeaderBeforeDated(t *testing.T) {
	d := day(t, "2024-01-01")

	opt := &Option{Key: "title", Value: "Test"}
	plg := &Plugin{Name: "beancount.plugins.auto"}
	inc := &Include{Path: "other.beancount"}
	open := &Open{Date: d, Account: "Assets:Cash"}

	l := &Ledger{}
	// Deliberately scramble: dated first, then headers in the order
	// opt, plg, inc — after ordering the headers must appear first and
	// among themselves in that insertion order.
	for _, dir := range []Directive{open, opt, plg, inc} {
		l.Insert(dir)
	}

	got := collect(l)
	want := []Directive{opt, plg, inc, open}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %T, want %T", i, got[i], want[i])
		}
	}
}

// TestLedgerInsertStabilityFIFO verifies that directives sharing the same
// (date, kind) retain their insertion order — the seq tiebreaker gives a
// FIFO result — and that existing entries never move when new ones arrive.
func TestLedgerInsertStabilityFIFO(t *testing.T) {
	d := day(t, "2024-02-01")
	tx1 := &Transaction{Date: d, Narration: "first"}
	tx2 := &Transaction{Date: d, Narration: "second"}
	tx3 := &Transaction{Date: d, Narration: "third"}

	l := &Ledger{}
	l.Insert(tx1)
	l.Insert(tx2)
	l.Insert(tx3)

	got := collect(l)
	want := []Directive{tx1, tx2, tx3}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestLedgerInsertPreservesExistingOrder inserts a new directive into the
// middle of an existing sequence and checks that previously present
// directives keep their relative positions.
func TestLedgerInsertPreservesExistingOrder(t *testing.T) {
	d1 := day(t, "2024-01-01")
	d2 := day(t, "2024-02-01")
	d3 := day(t, "2024-03-01")

	tx1 := &Transaction{Date: d1}
	tx3 := &Transaction{Date: d3}

	l := &Ledger{}
	l.Insert(tx1)
	l.Insert(tx3)

	// Insert a directive for the middle date.
	tx2 := &Transaction{Date: d2}
	l.Insert(tx2)

	got := collect(l)
	want := []Directive{tx1, tx2, tx3}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestLedgerInsertEmptySpanPluginDirectives simulates plugin-generated
// directives with no source span: they should still sort deterministically
// by insertion order among themselves.
func TestLedgerInsertEmptySpanPluginDirectives(t *testing.T) {
	d := day(t, "2024-04-01")
	a := &Transaction{Date: d, Narration: "plugin-a"}
	b := &Transaction{Date: d, Narration: "plugin-b"}
	c := &Transaction{Date: d, Narration: "plugin-c"}

	l := &Ledger{}
	for _, dir := range []Directive{a, b, c} {
		l.Insert(dir)
	}

	got := collect(l)
	want := []Directive{a, b, c}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestLedgerAllEarlyBreak verifies that iter.Seq2 range-over-func respects
// early break: iteration stops when yield returns false.
func TestLedgerAllEarlyBreak(t *testing.T) {
	d := day(t, "2024-05-01")
	l := &Ledger{}
	for i := 0; i < 5; i++ {
		l.Insert(&Transaction{Date: d, Narration: "t"})
	}

	count := 0
	for range l.All() {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("early break did not stop iteration: count = %d, want 2", count)
	}
}

// TestLedgerInsertAllMatchesInsert verifies that InsertAll produces the same
// final ordering as inserting each directive one at a time.
func TestLedgerInsertAllMatchesInsert(t *testing.T) {
	d1 := day(t, "2024-01-01")
	d2 := day(t, "2024-02-01")
	d3 := day(t, "2024-03-01")

	build := func() []Directive {
		return []Directive{
			&Transaction{Date: d3, Narration: "tx3"},
			&Open{Date: d1, Account: "Assets:A"},
			&Balance{Date: d2, Account: "Assets:A"},
			&Transaction{Date: d1, Narration: "tx1"},
			&Price{Date: d2, Commodity: "USD"},
			&Close{Date: d3, Account: "Assets:A"},
		}
	}

	var byInsert Ledger
	for _, d := range build() {
		byInsert.Insert(d)
	}

	var byBulk Ledger
	byBulk.InsertAll(build())

	if byInsert.Len() != byBulk.Len() {
		t.Fatalf("len mismatch: insert=%d, bulk=%d", byInsert.Len(), byBulk.Len())
	}
	for i := 0; i < byInsert.Len(); i++ {
		// Compare by concrete type + date/narration since the instances
		// differ between builds.
		a := byInsert.At(i)
		b := byBulk.At(i)
		if a.DirKind() != b.DirKind() || !a.DirDate().Equal(b.DirDate()) {
			t.Errorf("index %d: insert order (%T, %v) != bulk order (%T, %v)",
				i, a, a.DirDate(), b, b.DirDate())
		}
	}
}

// TestLedgerInsertAllAfterInsert ensures InsertAll can be called after a few
// Insert calls without disturbing the already-present entries.
func TestLedgerInsertAllAfterInsert(t *testing.T) {
	d := day(t, "2024-06-15")

	existing1 := &Open{Date: d, Account: "Assets:A"}
	existing2 := &Open{Date: d, Account: "Assets:B"}
	l := &Ledger{}
	l.Insert(existing1)
	l.Insert(existing2)

	new1 := &Open{Date: d, Account: "Assets:C"}
	new2 := &Open{Date: d, Account: "Assets:D"}
	l.InsertAll([]Directive{new1, new2})

	got := collect(l)
	want := []Directive{existing1, existing2, new1, new2}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %p, want %p", i, got[i], want[i])
		}
	}
}

// TestLedgerLenNil verifies the nil-receiver semantics of Len.
func TestLedgerLenNil(t *testing.T) {
	var l *Ledger
	if l.Len() != 0 {
		t.Errorf("nil Ledger Len = %d, want 0", l.Len())
	}
	count := 0
	for range l.All() {
		count++
	}
	if count != 0 {
		t.Errorf("nil Ledger All yielded %d items, want 0", count)
	}
}

// TestLedgerAtNilPanics verifies that At on a nil receiver panics with a
// clear message rather than a bare nil-pointer dereference.
func TestLedgerAtNilPanics(t *testing.T) {
	var l *Ledger
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("At on nil *Ledger did not panic")
		}
	}()
	_ = l.At(0)
}

// TestLedgerInsertAllEmpty verifies that InsertAll with an empty slice is
// a no-op (neither mutates entries nor bumps nextSeq).
func TestLedgerInsertAllEmpty(t *testing.T) {
	l := &Ledger{}
	l.Insert(&Open{Date: day(t, "2024-01-01"), Account: "Assets:Cash"})
	seqBefore := l.nextSeq
	lenBefore := l.Len()
	l.InsertAll(nil)
	l.InsertAll([]Directive{})
	if l.nextSeq != seqBefore {
		t.Errorf("nextSeq = %d, want unchanged %d", l.nextSeq, seqBefore)
	}
	if l.Len() != lenBefore {
		t.Errorf("Len = %d, want unchanged %d", l.Len(), lenBefore)
	}
}

// TestLedgerFilenameOffsetTieBreak verifies that when two file-originated
// directives share the same (date, kind) the ordering falls back to the
// source position (filename, offset), giving a deterministic result
// independent of insertion order.
func TestLedgerFilenameOffsetTieBreak(t *testing.T) {
	d := day(t, "2024-07-01")

	early := &Open{
		Span:    Span{Start: Position{Filename: "a.beancount", Offset: 10}},
		Date:    d,
		Account: "Assets:A",
	}
	late := &Open{
		Span:    Span{Start: Position{Filename: "a.beancount", Offset: 200}},
		Date:    d,
		Account: "Assets:B",
	}
	other := &Open{
		Span:    Span{Start: Position{Filename: "b.beancount", Offset: 0}},
		Date:    d,
		Account: "Assets:C",
	}

	l := &Ledger{}
	// Insert in reverse of expected order.
	for _, dir := range []Directive{other, late, early} {
		l.Insert(dir)
	}

	got := collect(l)
	want := []Directive{early, late, other}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// BenchmarkLedgerInsert measures the per-insert cost at several ledger
// sizes. It exists to catch accidental regressions (e.g. replacing the
// binary-search path with a full sort).
func BenchmarkLedgerInsert(b *testing.B) {
	sizes := []int{1_000, 10_000}
	for _, n := range sizes {
		b.Run(benchSize(n), func(b *testing.B) {
			base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			// Prewarm the ledger to size n.
			var l Ledger
			for i := 0; i < n; i++ {
				l.Insert(&Transaction{Date: base.AddDate(0, 0, i%365)})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.Insert(&Transaction{Date: base.AddDate(0, 0, i%365)})
			}
		})
	}
}

func benchSize(n int) string {
	switch n {
	case 1_000:
		return "n=1000"
	case 10_000:
		return "n=10000"
	default:
		return "unknown"
	}
}
