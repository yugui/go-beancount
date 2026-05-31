package std_test

import (
	"context"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/price"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestDateAdd(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"date_add(date(2021,3,15), 0)", "2021-03-15"},  // identity
		{"date_add(date(2021,1,31), 1)", "2021-02-01"},  // month boundary
		{"date_add(date(2021,12,31), 1)", "2022-01-01"}, // year boundary
		{"date_add(date(2021,1,1), -1)", "2020-12-31"},  // negative across year
		{"date_add(date(2020,2,28), 1)", "2020-02-29"},  // into a leap day
		{"date_add(date(2021,2,28), 1)", "2021-03-01"},  // non-leap skips Feb 29
		{"date_add(date(2021,1,1), 365)", "2022-01-01"}, // full non-leap year
		{"date_add(date(2020,1,1), 365)", "2020-12-31"}, // leap year is 366 days
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.Date || v.Format() != c.want {
			t.Errorf("%s = %v, want %s", c.expr, v, c.want)
		}
	}

	// NULL propagation: a NULL date argument yields NULL.
	res := mustQuery(t, l, "SELECT date_add(cost_date, 1) AS v FROM postings WHERE account = 'Assets:Cash'")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("date_add(NULL, 1) = %v, want NULL", res.Rows[0][0])
	}
}

func TestDateDiff(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct {
		expr string
		want int64
	}{
		{"date_diff(date(2021,1,1), date(2020,12,31))", 1},  // across year
		{"date_diff(date(2020,12,31), date(2021,1,1))", -1}, // negative direction
		{"date_diff(date(2021,3,15), date(2021,3,15))", 0},  // same day
		{"date_diff(date(2020,3,1), date(2020,2,28))", 2},   // spans a leap day
		{"date_diff(date(2021,3,1), date(2021,2,28))", 1},   // non-leap Feb
		{"date_diff(date(2022,1,1), date(2021,1,1))", 365},  // whole year
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkInt(t, v, c.want)
	}

	// NULL propagation.
	res := mustQuery(t, l, "SELECT date_diff(date, cost_date) AS v FROM postings WHERE account = 'Assets:Cash'")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("date_diff(_, NULL) = %v, want NULL", res.Rows[0][0])
	}
}

func TestInterval(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"interval('5 day')", "5 days"},
		{"interval('1 day')", "1 day"},
		{"interval('-1 day')", "-1 day"},
		{"interval('3 month')", "3 months"},
		{"interval('3 months')", "3 months"}, // plural tolerated on input
		{"interval('1 year')", "1 year"},
		{"interval('+2 years')", "2 years"},
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.Interval || v.Format() != c.want {
			t.Errorf("%s = %v, want %s interval", c.expr, v, c.want)
		}
	}

	// Non-matching input yields NULL.
	for _, expr := range []string{
		"interval('1 week')",   // unsupported unit
		"interval('1 decade')", // unsupported unit
		"interval('abc')",
		"interval('')",
		"interval('1month')",   // no whitespace
		"interval('1.5 days')", // non-integer
	} {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}

	// NULL propagation: a NULL string argument short-circuits to a typed NULL
	// (registerStrict). Driven through the resolved overload since the postings
	// table exposes no convenient NULL String column.
	fn, err := env.Resolve("interval", []types.Type{types.String})
	if err != nil {
		t.Fatalf("Resolve(interval): %v", err)
	}
	v, err := fn.Scalar(price.NewQueryContext(nil), []types.Value{types.Null(types.String)})
	if err != nil {
		t.Fatalf("interval(NULL): %v", err)
	}
	if !v.IsNull() {
		t.Errorf("interval(NULL) = %v, want NULL", v)
	}
}

// TestDateBin covers both stride kinds and, critically, the dateutil
// end-of-month clamp accumulating across iteration steps.
func TestDateBin(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"date_bin(interval('1 month'), date(2021,3,15), date(2021,1,1))", "2021-03-01"},
		// Clamp accumulation: origin Jan31 iterates Jan31->Feb28->Mar28->Apr28,
		// so Mar31 falls in the Mar28 bin (dateutil end-of-month clamp).
		{"date_bin(interval('1 month'), date(2021,3,31), date(2021,1,31))", "2021-03-28"},
		{"date_bin(interval('1 year'), date(2023,6,1), date(2020,1,1))", "2023-01-01"},
		{"date_bin(interval('1 month'), date(2020,12,15), date(2021,1,1))", "2020-12-01"}, // backward
		{"date_bin(interval('2 day'), date(2021,1,6), date(2021,1,1))", "2021-01-05"},
		// floor toward -inf: diff -1 day, floor(-1/2)*2 = -2.
		{"date_bin(interval('2 day'), date(2020,12,31), date(2021,1,1))", "2020-12-30"},
		// String overload parity.
		{"date_bin('1 month', date(2021,3,15), date(2021,1,1))", "2021-03-01"},
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.Date || v.Format() != c.want {
			t.Errorf("%s = %v, want %s date", c.expr, v, c.want)
		}
	}

	// Non-positive strides and a bad stride string yield NULL.
	for _, expr := range []string{
		"date_bin(interval('0 day'), date(2021,1,1), date(2021,1,1))",
		"date_bin(interval('-1 month'), date(2021,5,1), date(2021,1,1))",
		"date_bin('xyz', date(2021,3,15), date(2021,1,1))",
	} {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}

	// NULL propagation: a NULL date argument yields NULL.
	res := mustQuery(t, l,
		"SELECT date_bin(interval('1 month'), cost_date, date(2021,1,1)) AS v FROM postings WHERE account = 'Assets:Cash'")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("date_bin(_, NULL, _) = %v, want NULL", res.Rows[0][0])
	}
}

func TestParseDate(t *testing.T) {
	l := scalarLedger(t)

	// Single-argument form accepts each ISO layout; any time-of-day is dropped.
	okCases := []struct{ expr, want string }{
		{"parse_date('2021-03-15')", "2021-03-15"},
		{"parse_date('2021/03/15')", "2021-03-15"},
		{"parse_date('2021-03-15T10:30:00Z')", "2021-03-15"},
		// Two-argument strptime form, exercising several directives.
		{"parse_date('15/03/2021', '%d/%m/%Y')", "2021-03-15"},
		{"parse_date('March 15, 2021', '%B %d, %Y')", "2021-03-15"},
		{"parse_date('15-Mar-21', '%d-%b-%y')", "2021-03-15"},
	}
	for _, c := range okCases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.Date || v.Format() != c.want {
			t.Errorf("%s = %v, want %s date", c.expr, v, c.want)
		}
	}

	// Unparseable input is a returned error in both forms.
	errCases := []string{
		"SELECT parse_date('not-a-date') FROM postings LIMIT 1",
		"SELECT parse_date('2021-03-15', '%d/%m/%Y') FROM postings LIMIT 1",
	}
	for _, q := range errCases {
		if _, err := query.Query(context.Background(), q, l); err == nil {
			t.Errorf("%s: want error, got nil", q)
		}
	}
}

// TestDateTrunc covers every truncation field and the boundaries that matter:
// an ISO-week truncation crossing into the previous year (including pre-epoch),
// each quarter start, and the decade/century/millennium rules at round years.
func TestDateTrunc(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"date_trunc('week', date(2021,1,1))", "2020-12-28"},       // week crosses year
		{"date_trunc('week', date(2021,3,15))", "2021-03-15"},      // already a Monday
		{"date_trunc('week', date(2021,3,21))", "2021-03-15"},      // Sunday -> prior Monday
		{"date_trunc('week', date(1970,1,1))", "1969-12-29"},       // pre-epoch week
		{"date_trunc('month', date(2021,3,15))", "2021-03-01"},     // mid-month
		{"date_trunc('month', date(2021,3,1))", "2021-03-01"},      // already the 1st
		{"date_trunc('month', date(2021,1,31))", "2021-01-01"},     // end of a 31-day month
		{"date_trunc('month', date(2021,2,28))", "2021-02-01"},     // end of non-leap Feb
		{"date_trunc('month', date(2020,2,29))", "2020-02-01"},     // leap day
		{"date_trunc('quarter', date(2021,2,5))", "2021-01-01"},    // Q1 mid
		{"date_trunc('quarter', date(2021,1,1))", "2021-01-01"},    // already a quarter start
		{"date_trunc('quarter', date(2021,5,20))", "2021-04-01"},   // Q2
		{"date_trunc('quarter', date(2021,8,5))", "2021-07-01"},    // Q3
		{"date_trunc('quarter', date(2021,12,31))", "2021-10-01"},  // Q4 end
		{"date_trunc('year', date(2021,7,4))", "2021-01-01"},       // mid-year
		{"date_trunc('year', date(2021,1,1))", "2021-01-01"},       // Jan 1st
		{"date_trunc('year', date(2021,12,31))", "2021-01-01"},     // Dec 31st
		{"date_trunc('decade', date(2021,6,1))", "2020-01-01"},     //
		{"date_trunc('decade', date(2020,1,1))", "2020-01-01"},     // boundary year itself
		{"date_trunc('century', date(2021,6,1))", "2001-01-01"},    // 21st century from 2001
		{"date_trunc('century', date(2000,6,1))", "1901-01-01"},    // 2000 is 20th century
		{"date_trunc('millennium', date(2021,1,1))", "2001-01-01"}, //
		{"date_trunc('millennium', date(2000,1,1))", "1001-01-01"}, // 2000 is 2nd millennium
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.Date || v.Format() != c.want {
			t.Errorf("%s = %v, want %s date", c.expr, v, c.want)
		}
	}
}

// TestDatePart covers every part field including the weekday/isoweekday
// aliases, the ISO week/year boundary (2021-01-01 is ISO week 53 of 2020 and
// 2016-01-01 is week 53 of 2015), pre-epoch epoch seconds, and the
// decade/century/millennium ordinals.
func TestDatePart(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct {
		expr string
		want int64
	}{
		{"date_part('dow', date(2021,3,21))", 6},        // Sunday, Monday=0
		{"date_part('dow', date(2021,3,15))", 0},        // Monday
		{"date_part('weekday', date(2021,3,21))", 6},    // alias of dow
		{"date_part('isodow', date(2021,3,21))", 7},     // Sunday, ISO
		{"date_part('isodow', date(2021,3,15))", 1},     // Monday, ISO
		{"date_part('isoweekday', date(2021,3,15))", 1}, // alias of isodow
		{"date_part('week', date(2021,1,1))", 53},       // ISO week 53 of 2020
		{"date_part('week', date(2021,1,4))", 1},        // first ISO week of 2021
		{"date_part('week', date(2016,1,1))", 53},       // ISO week 53 of 2015
		{"date_part('week', date(2021,3,15))", 11},      //
		{"date_part('isoyear', date(2021,1,1))", 2020},  // ISO year boundary
		{"date_part('isoyear', date(2016,1,1))", 2015},  //
		{"date_part('month', date(2021,3,15))", 3},      //
		{"date_part('quarter', date(2021,3,31))", 1},    // Q1 end
		{"date_part('quarter', date(2021,4,1))", 2},     // Q2 start
		{"date_part('quarter', date(2021,7,1))", 3},     // Q3
		{"date_part('quarter', date(2021,12,31))", 4},   // Q4 end
		{"date_part('year', date(2021,3,15))", 2021},    //
		{"date_part('decade', date(2021,1,1))", 202},    // 2021 // 10
		{"date_part('decade', date(1969,1,1))", 196},    // pre-epoch
		{"date_part('century', date(2021,1,1))", 21},    // 21st century
		{"date_part('century', date(2000,1,1))", 20},    // 2000 is 20th
		{"date_part('century', date(1900,1,1))", 19},    //
		{"date_part('millennium', date(2021,1,1))", 3},  // 3rd millennium
		{"date_part('millennium', date(2000,1,1))", 2},  // 2000 is 2nd
		{"date_part('epoch', date(2021,3,15))", 1615766400},
		{"date_part('epoch', date(1970,1,1))", 0},        // epoch itself
		{"date_part('epoch', date(1969,12,31))", -86400}, // one day before epoch
		{"date_part('epoch', date(1960,6,15))", -301276800},
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkInt(t, v, c.want)
	}
}

func TestDateTruncPartUnknownFieldNull(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT date_trunc('nonsense', date) AS d, date_part('nonsense', date) AS p
		 FROM postings LIMIT 1`)
	row := res.Rows[0]
	for _, name := range []string{"d", "p"} {
		if !row[column(t, res, name)].IsNull() {
			t.Errorf("%s with unknown field = %v, want NULL", name, row[column(t, res, name)])
		}
	}
}

// TestToday checks today() against an injected query timestamp rather than the
// wall clock, so the result is deterministic, and confirms the time-of-day is
// dropped.
func TestToday(t *testing.T) {
	fn, err := env.Resolve("today", nil)
	if err != nil {
		t.Fatalf("Resolve(today): %v", err)
	}
	// Late in the day, to prove the time-of-day does not bleed into the date.
	fixed := time.Date(2021, 3, 15, 23, 59, 59, 0, time.UTC)
	ctx := price.NewQueryContextAt(nil, fixed)
	v, err := fn.Scalar(ctx, nil)
	if err != nil {
		t.Fatalf("today(): %v", err)
	}
	if v.Type() != types.Date || v.Format() != "2021-03-15" {
		t.Errorf("today() = %v, want 2021-03-15 date", v)
	}
}

// TestTodayConsistentWithinQuery proves every today() in one query observes
// the same instant — the reason the timestamp lives in QueryContext.
func TestTodayConsistentWithinQuery(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l, "SELECT today() AS a, today() AS b FROM postings LIMIT 1")
	a := res.Rows[0][column(t, res, "a")]
	b := res.Rows[0][column(t, res, "b")]
	if a.Type() != types.Date {
		t.Fatalf("today() type = %s, want date", a.Type())
	}
	if a.Compare(b) != 0 {
		t.Errorf("two today() calls disagree: %v vs %v", a, b)
	}
}
