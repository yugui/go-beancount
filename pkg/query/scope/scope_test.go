package scope_test

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/scope"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// threeYearLedger builds a ledger with one directive per year (2020, 2021,
// 2022) plus a header Option that has a zero DirDate, to exercise that header
// directives are also subject to the CLOSE filter (zero date is before any
// real date, so they are always kept).
func threeYearLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Option{Key: "title", Value: "test"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date:      date(2021, 3, 1),
			Flag:      '*',
			Narration: "txn 2021",
			Postings: []ast.Posting{
				{Account: "Assets:Cash"},
			},
		},
		&ast.Balance{
			Date:    date(2022, 6, 15),
			Account: "Assets:Cash",
		},
	})
	return l
}

func collectView(l *ast.Ledger, s scope.Spec) []ast.Directive {
	var out []ast.Directive
	for _, d := range scope.View(l, s) {
		out = append(out, d)
	}
	return out
}

func TestViewZeroSpecIsIdentity(t *testing.T) {
	l := threeYearLedger(t)

	want := collectView(l, scope.Spec{})

	var wantAll []ast.Directive
	for _, d := range l.All() {
		wantAll = append(wantAll, d)
	}

	if len(want) != len(wantAll) {
		t.Fatalf("len = %d, want %d", len(want), len(wantAll))
	}
	for i := range want {
		if want[i] != wantAll[i] {
			t.Errorf("index %d: got %T, want %T", i, want[i], wantAll[i])
		}
	}
}

func TestViewCloseDropsOnAndAfter(t *testing.T) {
	l := threeYearLedger(t)
	s := scope.Spec{Close: date(2021, 6, 1)}

	got := collectView(l, s)

	// header Option (zero date) and Open(2020-01-01) survive; Transaction(2021-03-01) survives;
	// Balance(2022-06-15) is dropped.
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3; directives: %v", len(got), got)
	}
	if _, ok := got[0].(*ast.Option); !ok {
		t.Errorf("got[0] = %T, want *ast.Option", got[0])
	}
	if _, ok := got[1].(*ast.Open); !ok {
		t.Errorf("got[1] = %T, want *ast.Open", got[1])
	}
	if _, ok := got[2].(*ast.Transaction); !ok {
		t.Errorf("got[2] = %T, want *ast.Transaction", got[2])
	}
}

func TestViewCloseExactBoundaryExcluded(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2021, 6, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date:      date(2021, 5, 31),
			Flag:      '*',
			Narration: "before",
		},
	})

	s := scope.Spec{Close: date(2021, 6, 1)}
	got := collectView(l, s)

	// Open on exactly Close date must be dropped (strict <).
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; directives: %v", len(got), got)
	}
	if txn, ok := got[0].(*ast.Transaction); !ok || txn.Narration != "before" {
		t.Errorf("got[0] = %T %v, want Transaction 'before'", got[0], got[0])
	}
}

func TestViewReindexesDense(t *testing.T) {
	l := threeYearLedger(t)
	s := scope.Spec{Close: date(2022, 1, 1)}

	var indices []int
	for i := range scope.View(l, s) {
		indices = append(indices, i)
	}

	want := make([]int, len(indices))
	for i := range want {
		want[i] = i
	}
	if !slices.Equal(indices, want) {
		t.Errorf("indices = %v, want dense 0-based %v", indices, want)
	}
}

func TestViewReplayable(t *testing.T) {
	l := threeYearLedger(t)
	s := scope.Spec{Close: date(2022, 1, 1)}

	first := collectView(l, s)
	second := collectView(l, s)

	if len(first) != len(second) {
		t.Fatalf("first len %d, second len %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("index %d differs: %T vs %T", i, first[i], second[i])
		}
	}
}

func TestViewCloseKeepsZeroDateDirectives(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Option{Key: "title", Value: "test"},
		&ast.Balance{
			Date:    date(2022, 1, 1),
			Account: "Assets:Cash",
		},
	})

	s := scope.Spec{Close: date(2022, 1, 1)}
	got := collectView(l, s)

	// Zero-date Option survives (time.Time{}.Before(Close) is true);
	// Balance on exactly Close is dropped (strict <).
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; directives: %v", len(got), got)
	}
	if _, ok := got[0].(*ast.Option); !ok {
		t.Errorf("got[0] = %T, want *ast.Option", got[0])
	}
}

func TestViewEarlyBreak(t *testing.T) {
	l := threeYearLedger(t)
	s := scope.Spec{Close: date(2022, 1, 1)}

	var first ast.Directive
	for _, d := range scope.View(l, s) {
		first = d
		break
	}
	if first == nil {
		t.Fatal("expected at least one directive")
	}

	// Re-iterating must replay from index 0, not from where the previous break stopped.
	second := collectView(l, s)
	if len(second) == 0 {
		t.Fatal("expected non-empty replay")
	}
	if second[0] != first {
		t.Errorf("replay[0] = %T, want same as first = %T", second[0], first)
	}
}

// TestViewConcurrentRaceFree models the table.TestConcurrentReadIsRaceFree
// pattern: one ledger, one Spec, many goroutines each creating and consuming
// their own iterator. Run with -race.
func TestViewConcurrentRaceFree(t *testing.T) {
	l := threeYearLedger(t)
	s := scope.Spec{Close: date(2022, 1, 1)}

	baseline := collectView(l, s)

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := collectView(l, s)
			if len(got) != len(baseline) {
				errs <- "length mismatch"
				return
			}
			for i := range got {
				if got[i] != baseline[i] {
					errs <- "directive mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatalf("concurrent View: %s", msg)
	}
}
