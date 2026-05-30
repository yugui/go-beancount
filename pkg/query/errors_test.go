package query_test

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/query"
)

func TestCompileErrors(t *testing.T) {
	l := sampleLedger(t)
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"unknown column", "SELECT nope FROM postings", "unknown column"},
		{"unknown table or column in FROM", "SELECT account FROM nosuch", "unknown table or column"},
		{"type mismatch comparison", "SELECT account WHERE account > 1", "cannot compare"},
		{"type mismatch arithmetic", "SELECT account + 1 FROM postings", "numeric"},
		{"no overload", "SELECT nosuchfn(account) FROM postings", "no matching overload"},
		{"aggregate in where", "SELECT account WHERE count(account) > 0", "aggregate"},
		{"aggregate mixing", "SELECT payee, count(account) GROUP BY account", "must appear in GROUP BY"},
		{"nested aggregate", "SELECT sum(count(number)) GROUP BY account", "nested aggregate"},
		{"non-boolean predicate", "SELECT account WHERE number", "boolean"},
		{"non-boolean from", "SELECT account FROM number", "boolean"},
		{"aggregate only in order by", "SELECT account FROM postings ORDER BY sum(number)", "must appear in GROUP BY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := query.Compile(tc.query, l)
			if err == nil {
				t.Fatalf("Compile(%q) succeeded, want error containing %q", tc.query, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestColumnsAvailableBeforeRun(t *testing.T) {
	c, err := query.Compile("SELECT account, sum(number) AS total GROUP BY account", sampleLedger(t))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	cols := c.Columns()
	if len(cols) != 2 || cols[0].Name != "account" || cols[1].Name != "total" {
		t.Fatalf("columns = %+v, want [account total]", cols)
	}
}
