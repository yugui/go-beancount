package std_test

import (
	"context"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// TestDateScalarsBoundaries covers weekday/quarter/yearmonth across the
// year, quarter, and week boundaries that the single-date TestDateScalars
// cannot. It drives every case through one ledger (one posting per date)
// so it depends only on the slice-1 date scalars, not the date() cast.
func TestDateScalarsBoundaries(t *testing.T) {
	cases := []struct {
		y         int
		m         time.Month
		d         int
		weekday   string // strftime %a
		quarter   string // "YYYY-Qn"
		yearmonth string // month-truncated date
	}{
		{2021, 1, 1, "Fri", "2021-Q1", "2021-01-01"},   // year start
		{2021, 3, 31, "Wed", "2021-Q1", "2021-03-01"},  // Q1 end
		{2021, 4, 1, "Thu", "2021-Q2", "2021-04-01"},   // Q2 start
		{2021, 6, 30, "Wed", "2021-Q2", "2021-06-01"},  // Q2 end
		{2021, 7, 1, "Thu", "2021-Q3", "2021-07-01"},   // Q3 start
		{2021, 9, 30, "Thu", "2021-Q3", "2021-09-01"},  // Q3 end
		{2021, 10, 1, "Fri", "2021-Q4", "2021-10-01"},  // Q4 start
		{2021, 12, 31, "Fri", "2021-Q4", "2021-12-01"}, // year/Q4 end
		{2021, 2, 15, "Mon", "2021-Q1", "2021-02-01"},  // mid-Q1, a Monday
		{2021, 5, 12, "Wed", "2021-Q2", "2021-05-01"},  // mid-Q2
		{2021, 8, 16, "Mon", "2021-Q3", "2021-08-01"},  // mid-Q3, a Monday
		{2021, 11, 14, "Sun", "2021-Q4", "2021-11-01"}, // mid-Q4, a Sunday
		{2021, 3, 21, "Sun", "2021-Q1", "2021-03-01"},  // Sunday
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

	res := mustQuery(t, l,
		`SELECT date AS dt, weekday(date) AS wd, quarter(date) AS q,
		        yearmonth(date) AS ym FROM postings ORDER BY date`)
	dtCol := column(t, res, "dt")
	wdCol := column(t, res, "wd")
	qCol := column(t, res, "q")
	ymCol := column(t, res, "ym")
	byDate := map[string][]types.Value{}
	for _, row := range res.Rows {
		byDate[row[dtCol].Format()] = row
	}

	for _, c := range cases {
		key := date(c.y, c.m, c.d).Format("2006-01-02")
		row, ok := byDate[key]
		if !ok {
			t.Fatalf("no result row for %s", key)
		}
		if s, _ := types.AsString(row[wdCol]); s != c.weekday {
			t.Errorf("weekday(%s) = %q, want %q", key, s, c.weekday)
		}
		if s, _ := types.AsString(row[qCol]); s != c.quarter {
			t.Errorf("quarter(%s) = %q, want %q", key, s, c.quarter)
		}
		if v := row[ymCol]; v.Type() != types.Date || v.Format() != c.yearmonth {
			t.Errorf("yearmonth(%s) = %v, want %s date", key, v, c.yearmonth)
		}
	}
}

func TestDateScalars(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT year(date) AS y, month(date) AS mo, day(date) AS d,
		        weekday(date) AS wd, quarter(date) AS q, yearmonth(date) AS ym
		 FROM postings LIMIT 1`)
	row := res.Rows[0]
	checkInt(t, row[column(t, res, "y")], 2021)
	checkInt(t, row[column(t, res, "mo")], 3)
	checkInt(t, row[column(t, res, "d")], 15)
	// Upstream parity: weekday is a 3-letter abbreviation (strftime %a),
	// quarter is "YYYY-Qn", yearmonth is a month-truncated date. 2021-03-15
	// is a Monday in Q1.
	checkStr(t, row[column(t, res, "wd")], "Mon")
	checkStr(t, row[column(t, res, "q")], "2021-Q1")
	ym := row[column(t, res, "ym")]
	if ym.Type() != types.Date || ym.Format() != "2021-03-01" {
		t.Errorf("yearmonth(date) = %v, want 2021-03-01 date", ym)
	}
}

func TestStringScalars(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT upper(payee) AS u, lower(payee) AS lo, length(narration) AS n
		 FROM postings LIMIT 1`)
	row := res.Rows[0]
	checkStr(t, row[column(t, res, "u")], "CAFÉ")
	checkStr(t, row[column(t, res, "lo")], "café")
	// "naïve résumé" is 12 runes (counting é/ï as single runes).
	checkInt(t, row[column(t, res, "n")], 12)
}

func TestSubstrRunesAndClamping(t *testing.T) {
	l := scalarLedger(t)
	// payee "Café": runes [C a f é]. substr from index 1, length 2 -> "af".
	res := mustQuery(t, l, "SELECT substr(payee, 1, 2) AS s FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "af")

	// Out-of-range start and length clamp to the rune length without error.
	res = mustQuery(t, l, "SELECT substr(payee, 10, 5) AS s FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "")

	res = mustQuery(t, l, "SELECT substr(payee, 0, 100) AS s FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "Café")

	// A multibyte character is returned whole (rune-based, not byte-based).
	res = mustQuery(t, l, "SELECT substr(payee, 3, 1) AS s FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "é")
}

func TestGrep(t *testing.T) {
	l := scalarLedger(t)

	res := mustQuery(t, l, "SELECT grep('Caf.', payee) AS m FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "Café")

	res = mustQuery(t, l, "SELECT grep('zzz', payee) AS m FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("grep no-match = %v, want NULL", res.Rows[0][0])
	}

	// A malformed pattern is a returned error, not a panic.
	_, err := query.Query(context.Background(),
		"SELECT grep('(', payee) FROM postings LIMIT 1", l)
	if err == nil {
		t.Fatal("grep with bad pattern: want error, got nil")
	}
}

func TestAccountScalars(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT root(account) AS r, parent(account) AS p, leaf(account) AS lf
		 FROM postings WHERE account = 'Assets:Brokerage:AAPL'`)
	row := res.Rows[0]
	checkStr(t, row[column(t, res, "r")], "Assets")
	checkStr(t, row[column(t, res, "p")], "Assets:Brokerage")
	checkStr(t, row[column(t, res, "lf")], "AAPL")
}

func TestValueExtractorsAmount(t *testing.T) {
	l := scalarLedger(t)
	// weight is an Amount column; over the cash posting it is -20 USD.
	res := mustQuery(t, l,
		`SELECT number(weight) AS n, currency(weight) AS c
		 FROM postings WHERE account = 'Assets:Cash'`)
	row := res.Rows[0]
	if got := row[column(t, res, "n")].Format(); got != "-20" {
		t.Errorf("number(weight) = %s, want -20", got)
	}
	checkStr(t, row[column(t, res, "c")], "USD")
}

func TestValueExtractorsPosition(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT number(position) AS n, currency(position) AS c, units(position) AS u
		 FROM postings WHERE account = 'Assets:Brokerage:AAPL'`)
	row := res.Rows[0]
	if got := row[column(t, res, "n")].Format(); got != "2" {
		t.Errorf("number(position) = %s, want 2", got)
	}
	checkStr(t, row[column(t, res, "c")], "AAPL")
	if u := row[column(t, res, "u")]; u.Type() != types.Amount || u.Format() != "2 AAPL" {
		t.Errorf("units(position) = %v, want 2 AAPL", u)
	}
}

func TestCostWithAndWithoutLot(t *testing.T) {
	l := scalarLedger(t)

	// AAPL posting: 2 units × 10 USD/unit = 20 USD total cost.
	res := mustQuery(t, l,
		"SELECT cost(position) AS c FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	c := res.Rows[0][0]
	if c.Type() != types.Amount || c.Format() != "20 USD" {
		t.Errorf("cost = %v, want 20 USD", c)
	}

	// Cash posting has no lot: cost is NULL.
	res = mustQuery(t, l,
		"SELECT cost(position) AS c FROM postings WHERE account = 'Assets:Cash'")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("cost (no lot) = %v, want NULL", res.Rows[0][0])
	}
}

func TestOverloadSelectionNumberCurrency(t *testing.T) {
	l := scalarLedger(t)
	// number/currency resolve the Amount overload over weight and the Position
	// overload over position; both yield the expected concrete results.
	res := mustQuery(t, l,
		`SELECT number(weight) AS na, number(position) AS np,
		        currency(weight) AS ca, currency(position) AS cp
		 FROM postings WHERE account = 'Assets:Brokerage:AAPL'`)
	row := res.Rows[0]
	if res.Columns[column(t, res, "na")].Type != types.Decimal {
		t.Errorf("number(weight) column type = %s, want decimal", res.Columns[0].Type)
	}
	checkStr(t, row[column(t, res, "ca")], "USD")
	checkStr(t, row[column(t, res, "cp")], "AAPL")
	if got := row[column(t, res, "np")].Format(); got != "2" {
		t.Errorf("number(position) = %s, want 2", got)
	}
}

func TestDateScalarNullPropagation(t *testing.T) {
	// An entries-table directive without a date-bearing field still has a
	// date; instead test NULL propagation by extracting over a NULL column.
	// payee is NULL on a transaction with no payee.
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Transaction{
			Date: date(2020, 1, 1),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
			},
		},
	})
	res := mustQuery(t, l, "SELECT upper(payee) AS u FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("upper(NULL payee) = %v, want NULL", res.Rows[0][0])
	}
}

func checkInt(t *testing.T, v types.Value, want int64) {
	t.Helper()
	n, ok := types.AsInt(v)
	if !ok {
		t.Fatalf("value %v is not Int", v)
	}
	if n != want {
		t.Errorf("int = %d, want %d", n, want)
	}
}

func checkStr(t *testing.T, v types.Value, want string) {
	t.Helper()
	s, ok := types.AsString(v)
	if !ok {
		t.Fatalf("value %v is not String", v)
	}
	if s != want {
		t.Errorf("string = %q, want %q", s, want)
	}
}
