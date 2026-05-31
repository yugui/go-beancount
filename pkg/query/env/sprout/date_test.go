package sprout_test

import (
	"context"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// TestSproutDateVariants covers the convenience date renderings across the
// year/quarter/month boundaries and their middles, over both a Monday and a
// Sunday (weekday_name is backed by a weekday table, so both ends of the week
// must be checked). Every case is driven through one ledger ordered by date.
// It depends only on the sprout variants, not on the std date() cast, which
// the sprout test package does not activate.
func TestSproutDateVariants(t *testing.T) {
	cases := []struct {
		y           int
		m           time.Month
		d           int
		weekdayName string // full English name
		quarterIdx  int64  // 1..4
		yearmonth   string // "YYYY-MM"
	}{
		{2021, 1, 1, "Friday", 1, "2021-01"},     // year start
		{2021, 3, 31, "Wednesday", 1, "2021-03"}, // Q1 end
		{2021, 4, 1, "Thursday", 2, "2021-04"},   // Q2 start
		{2021, 6, 30, "Wednesday", 2, "2021-06"}, // Q2 end
		{2021, 7, 1, "Thursday", 3, "2021-07"},   // Q3 start
		{2021, 10, 1, "Friday", 4, "2021-10"},    // Q4 start
		{2021, 12, 31, "Friday", 4, "2021-12"},   // year/Q4 end
		{2021, 2, 15, "Monday", 1, "2021-02"},    // mid-Q1, mid-month, a Monday
		{2021, 5, 12, "Wednesday", 2, "2021-05"}, // mid-Q2, mid-month
		{2021, 8, 16, "Monday", 3, "2021-08"},    // mid-Q3, a Monday
		{2021, 11, 14, "Sunday", 4, "2021-11"},   // mid-Q4, a Sunday
		{2021, 3, 15, "Monday", 1, "2021-03"},    // a Monday
		{2021, 3, 21, "Sunday", 1, "2021-03"},    // Sunday
	}
	l := &ast.Ledger{}
	dirs := make([]ast.Directive, len(cases))
	for i, c := range cases {
		dirs[i] = &ast.Transaction{
			Date: date(c.y, c.m, c.d),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
			},
		}
	}
	l.InsertAll(dirs)

	res, err := query.Query(context.Background(),
		`SELECT date AS dt, weekday_name(date) AS wn, quarter_index(date) AS qi,
		        yearmonth_str(date) AS ym FROM postings ORDER BY date`, l)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Columns are selected in order: dt, wn, qi, ym.
	byDate := map[string][]types.Value{}
	for _, row := range res.Rows {
		byDate[row[0].Format()] = row
	}

	for _, c := range cases {
		key := date(c.y, c.m, c.d).Format("2006-01-02")
		row, ok := byDate[key]
		if !ok {
			t.Fatalf("no result row for %s", key)
		}
		if s, _ := types.AsString(row[1]); s != c.weekdayName {
			t.Errorf("weekday_name(%s) = %q, want %q", key, s, c.weekdayName)
		}
		if n, _ := types.AsInt(row[2]); n != c.quarterIdx {
			t.Errorf("quarter_index(%s) = %d, want %d", key, n, c.quarterIdx)
		}
		if s, _ := types.AsString(row[3]); s != c.yearmonth {
			t.Errorf("yearmonth_str(%s) = %q, want %q", key, s, c.yearmonth)
		}
	}
}
