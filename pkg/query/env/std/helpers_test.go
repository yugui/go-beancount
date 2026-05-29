package std_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"

	// activate the built-in function library under test
	_ "github.com/yugui/go-beancount/pkg/query/env/std"
)

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func mustQuery(t *testing.T, l *ast.Ledger, q string) query.Result {
	t.Helper()
	res, err := query.Query(context.Background(), q, l)
	if err != nil {
		t.Fatalf("Query(%q): %v", q, err)
	}
	return res
}

func column(t *testing.T, res query.Result, name string) int {
	t.Helper()
	for i, c := range res.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("no output column %q", name)
	return -1
}

// scalarLedger is a single transaction whose first posting carries a booked
// cost lot (2 AAPL @ 10 USD), a multibyte payee, and posting metadata. It
// drives the scalar-function tests through the postings table.
func scalarLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Transaction{
			Date:      date(2021, 3, 15), // Monday, Q1
			Flag:      '*',
			Payee:     "Café",
			Narration: "naïve résumé",
			Tags:      []string{"food", "weekly"},
			Postings: []ast.Posting{
				{
					Account: "Assets:Brokerage:AAPL",
					Amount:  &ast.Amount{Number: dec(t, "2"), Currency: "AAPL"},
					Cost:    &ast.Cost{Number: dec(t, "10"), Currency: "USD", Date: date(2021, 1, 1)},
					Meta: ast.Metadata{Props: map[string]ast.MetaValue{
						"category": {Kind: ast.MetaString, String: "tech"},
						"qty":      {Kind: ast.MetaNumber, Number: dec(t, "42")},
					}},
				},
				{
					Account: "Assets:Cash",
					Amount:  &ast.Amount{Number: dec(t, "-20"), Currency: "USD"},
				},
			},
		},
	})
	return l
}
