package std_test

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestCastStr(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"str(42)", "42"},
		{"str(-7)", "-7"},
		{"str(TRUE)", "TRUE"},
		{"str(FALSE)", "FALSE"},
		{"str('already')", "already"},
		{"str('')", ""},
		{"str(1.5)", "1.5"},
		{"str(date('2021-03-15'))", "2021-03-15"},
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkStr(t, v, c.want)
	}
}

// TestCastRepr pins repr's distinguishing behavior: it renders via the value's
// debug String(), which quotes a string, unlike str.
func TestCastRepr(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l, "SELECT repr('x') AS r, str('x') AS s FROM postings LIMIT 1")
	r, _ := types.AsString(res.Rows[0][column(t, res, "r")])
	s, _ := types.AsString(res.Rows[0][column(t, res, "s")])
	if r == s {
		t.Errorf("repr('x')=%q should differ from str('x')=%q", r, s)
	}
	if r != `"x"` {
		t.Errorf("repr('x') = %q, want quoted \"x\"", r)
	}
}

func TestCastBool(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct {
		expr string
		want bool
	}{
		{"bool(0)", false},
		{"bool(1)", true},
		{"bool(-1)", true},
		{"bool(0.0)", false},
		{"bool(2.5)", true},
		{"bool('')", false},     // empty string is falsey
		{"bool('x')", true},     // any non-empty string is truthy
		{"bool('FALSE')", true}, // a non-empty string, NOT parsed as a keyword
		{"bool(TRUE)", true},
		{"bool(FALSE)", false},
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if b, ok := types.AsBool(v); !ok || b != c.want {
			t.Errorf("%s = %v, want %v", c.expr, v, c.want)
		}
	}
}

func TestCastInt(t *testing.T) {
	l := scalarLedger(t)
	intCases := []struct {
		expr string
		want int64
	}{
		{"int('42')", 42},
		{"int('-42')", -42},
		{"int('+42')", 42},
		{"int(42)", 42},
		{"int(2.9)", 2},   // truncates toward zero
		{"int(-2.9)", -2}, // negative truncates toward zero
		{"int(2.0)", 2},
		{"int(0.4)", 0},
		{"int(TRUE)", 1},
		{"int(FALSE)", 0},
	}
	for _, c := range intCases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkInt(t, v, c.want)
	}

	// Inputs that cannot convert yield NULL (never an error). int parses
	// base-10 only and does no keyword/case handling.
	nullCases := []string{
		"int('abc')", "int('')", "int('  42')", "int('42.0')",
		"int('0x1F')", "int('TRUE')", "int('9223372036854775808')",
	}
	for _, expr := range nullCases {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}
}

func TestCastDecimal(t *testing.T) {
	l := scalarLedger(t)
	okCases := []struct{ expr, want string }{
		{"decimal('1.5')", "1.5"},
		{"decimal('-1.5')", "-1.5"},
		{"decimal(3)", "3"},
		{"decimal(-3)", "-3"},
		{"decimal(TRUE)", "1"},
		{"decimal(FALSE)", "0"},
		{"decimal(2.5)", "2.5"},
	}
	for _, c := range okCases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkDec(t, v, c.want)
	}

	for _, expr := range []string{"decimal('x')", "decimal('')", "decimal('1.5.5')"} {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}
}

func TestCastDate(t *testing.T) {
	l := scalarLedger(t)
	okCases := []struct{ expr, want string }{
		{"date('2021-03-15')", "2021-03-15"},
		{"date(date('2021-03-15'))", "2021-03-15"}, // Date identity
		{"date(2021, 3, 15)", "2021-03-15"},
		{"date(2020, 2, 29)", "2020-02-29"}, // valid leap day
	}
	for _, c := range okCases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.Date || v.Format() != c.want {
			t.Errorf("%s = %v, want %s date", c.expr, v, c.want)
		}
	}

	// Bad input yields NULL: bad string formats, and out-of-range Y/M/D that
	// the date(int,int,int) round-trip guard rejects (incl. non-leap Feb 29).
	nullCases := []string{
		"date('nope')", "date('')", "date('2021-3-15')", "date('2021/03/15')",
		"date('2021-13-01')", "date('2021-02-29')",
		"date(2021, 13, 40)", "date(2021, 2, 29)", "date(2021, 0, 1)",
		"date(2021, 1, 0)", "date(2021, -1, 15)",
	}
	for _, expr := range nullCases {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}
}

// TestCastAcceptsNullLiteral proves the types.Any parameter lets a NULL
// literal reach every cast, which then short-circuits to a typed NULL.
func TestCastAcceptsNullLiteral(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT str(NULL) AS s, repr(NULL) AS r, bool(NULL) AS b,
		        int(NULL) AS i, decimal(NULL) AS d, date(NULL) AS dt FROM postings LIMIT 1`)
	for _, name := range []string{"s", "r", "b", "i", "d", "dt"} {
		if !res.Rows[0][column(t, res, name)].IsNull() {
			t.Errorf("%s(NULL) = %v, want NULL", name, res.Rows[0][column(t, res, name)])
		}
	}
}

// TestCastInterval pins the cast behavior for Interval values, which hit the
// generic default branch of each cast function.
//
// str/repr render the canonical interval form (Format == String for Interval).
// bool is always true — truthy's default does not inspect components, so even
// an all-zero interval is a non-null value and therefore true.
// int/decimal/date yield NULL — no numeric conversion exists for a calendar offset.
func TestCastInterval(t *testing.T) {
	l := scalarLedger(t)

	// str and repr both render the canonical form, including negative values.
	for _, tc := range []struct{ expr, want string }{
		{"str(interval('5 day'))", "5 days"},
		{"str(interval('1 year'))", "1 year"},
		{"str(interval('-1 day'))", "-1 day"},
		{"str(interval('-2 year'))", "-2 years"},
		{"repr(interval('5 day'))", "5 days"},
		{"repr(interval('-1 month'))", "-1 month"},
	} {
		v := mustQuery(t, l, "SELECT "+tc.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if v.Type() != types.String {
			t.Errorf("%s: type = %v, want String", tc.expr, v.Type())
			continue
		}
		if got, _ := types.AsString(v); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}

	// bool is true for any non-null Interval, including all-zero.
	for _, expr := range []string{"bool(interval('5 day'))", "bool(interval('0 day'))"} {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if b, ok := types.AsBool(v); !ok || !b {
			t.Errorf("%s = %v, want TRUE", expr, v)
		}
	}

	// int, decimal, and date yield NULL — no conversion defined for Interval.
	for _, expr := range []string{
		"int(interval('5 day'))",
		"decimal(interval('5 day'))",
		"date(interval('5 day'))",
	} {
		v := mustQuery(t, l, "SELECT "+expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}
}

// TestConcreteRejectsNullLiteral confirms concrete-typed functions (no Any
// slot) still reject a NULL literal at compile time, matching upstream
// beanquery's Any-vs-concrete distinction.
func TestConcreteRejectsNullLiteral(t *testing.T) {
	l := scalarLedger(t)
	for _, q := range []string{
		"SELECT abs(NULL) FROM postings LIMIT 1",
		"SELECT date_add(NULL, 1) FROM postings LIMIT 1",
		"SELECT round(NULL) FROM postings LIMIT 1",
	} {
		if _, err := query.Query(context.Background(), q, l); err == nil {
			t.Errorf("%s: want compile error, got nil", q)
		}
	}
}
