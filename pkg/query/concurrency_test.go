package query_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yugui/go-beancount/pkg/query"
)

// TestConcurrentRun is the binding proof of Decision 6: one *Compiled over
// one shared immutable ledger, run from many goroutines, yields identical
// results with no locking. Run under `go test -race`.
func TestConcurrentRun(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{
			"no scoping",
			"SELECT account, sum(number) AS total GROUP BY account ORDER BY total DESC",
		},
		{
			"CLOSE ON scoping",
			"SELECT account, sum(number) AS total FROM postings CLOSE ON 2022-01-01 GROUP BY account ORDER BY total DESC",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := sampleLedger(t)
			c, err := query.Compile(tc.query, l)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			want, err := c.Run(context.Background())
			if err != nil {
				t.Fatalf("baseline Run: %v", err)
			}

			const goroutines = 32
			var wg sync.WaitGroup
			errs := make(chan error, goroutines)
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := c.Run(context.Background())
					if err != nil {
						errs <- err
						return
					}
					if !sameResult(want, got) {
						errs <- errMismatch
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent Run diverged: %v", err)
			}
		})
	}
}

var errMismatch = errConst("concurrent result differs from baseline")

type errConst string

func (e errConst) Error() string { return string(e) }

func sameResult(a, b query.Result) bool {
	if len(a.Columns) != len(b.Columns) || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	for i := range a.Rows {
		if len(a.Rows[i]) != len(b.Rows[i]) {
			return false
		}
		for j := range a.Rows[i] {
			if a.Rows[i][j].Compare(b.Rows[i][j]) != 0 {
				return false
			}
		}
	}
	return true
}
