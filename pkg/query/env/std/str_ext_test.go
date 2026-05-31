package std_test

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/query"
)

func TestGrepn(t *testing.T) {
	l := scalarLedger(t)
	// payee is "Café"; pattern comes first (upstream order).
	cases := []struct{ expr, want string }{
		{"grepn('(Ca)(f.)', payee, 0)", "Café"}, // group 0 is the whole match
		{"grepn('(Ca)(f.)', payee, 1)", "Ca"},
		{"grepn('(Ca)(f.)', payee, 2)", "fé"},
		{"grepn('a*', '', 0)", ""},       // empty input, empty match at 0
		{"grepn('(a)(b)?', 'a', 2)", ""}, // non-participating group -> ""
	}
	for _, c := range cases {
		v := mustQuery(t, l, `SELECT `+c.expr+` AS v FROM postings LIMIT 1`).Rows[0][0]
		checkStr(t, v, c.want)
	}

	// No match or out-of-range index yields NULL.
	for _, expr := range []string{
		"grepn('zzz', payee, 0)", "grepn('(a)', 'a', 5)", "grepn('(a)', 'a', -1)",
	} {
		v := mustQuery(t, l, `SELECT `+expr+` AS v FROM postings LIMIT 1`).Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s = %v, want NULL", expr, v)
		}
	}

	// A malformed pattern is a returned error.
	if _, err := query.Query(context.Background(),
		`SELECT grepn('(', payee, 1) FROM postings LIMIT 1`, l); err == nil {
		t.Fatal("grepn bad pattern: want error, got nil")
	}
}

func TestSubst(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"subst('f.', 'FE', payee)", "CaFE"},    // literal replacement
		{"subst('(a)(b)', '$2$1', 'ab')", "ba"}, // RE2 backreference $n
		{"subst('zzz', 'X', 'abc')", "abc"},     // no match -> unchanged
		{"subst('a', 'X', 'aaa')", "XXX"},       // global replacement
		{"subst('a', 'X', '')", ""},             // empty input
	}
	for _, c := range cases {
		v := mustQuery(t, l, `SELECT `+c.expr+` AS v FROM postings LIMIT 1`).Rows[0][0]
		checkStr(t, v, c.want)
	}

	if _, err := query.Query(context.Background(),
		`SELECT subst('(', 'X', payee) FROM postings LIMIT 1`, l); err == nil {
		t.Fatal("subst bad pattern: want error, got nil")
	}
}

func TestFindfirst(t *testing.T) {
	l := scalarLedger(t)
	// tags is {food, weekly}, sorted ascending.
	cases := []struct {
		expr   string
		want   string
		isNull bool
	}{
		{expr: "findfirst('we', tags)", want: "weekly"},
		{expr: "findfirst('foo', tags)", want: "food"},
		{expr: "findfirst('f', tags)", want: "food"},   // sorted: food before weekly
		{expr: "findfirst('ood', tags)", isNull: true}, // matches mid-string, not at start
		{expr: "findfirst('zz', tags)", isNull: true},  // no match
	}
	for _, c := range cases {
		v := mustQuery(t, l, `SELECT `+c.expr+` AS v FROM postings LIMIT 1`).Rows[0][0]
		if c.isNull {
			if !v.IsNull() {
				t.Errorf("%s = %v, want NULL", c.expr, v)
			}
			continue
		}
		checkStr(t, v, c.want)
	}

	if _, err := query.Query(context.Background(),
		`SELECT findfirst('(', tags) FROM postings LIMIT 1`, l); err == nil {
		t.Fatal("findfirst bad pattern: want error, got nil")
	}
}

func TestJoinstr(t *testing.T) {
	l := scalarLedger(t)
	// tags {food, weekly} joins sorted, comma-separated.
	checkStr(t, mustQuery(t, l, `SELECT joinstr(tags) AS v FROM postings LIMIT 1`).Rows[0][0], "food,weekly")
	// links is empty on this posting -> empty string.
	v := mustQuery(t, l, `SELECT joinstr(links) AS v FROM postings LIMIT 1`).Rows[0][0]
	checkStr(t, v, "")
}

func TestSplitcomp(t *testing.T) {
	l := scalarLedger(t)
	const where = ` FROM postings WHERE account = 'Assets:Brokerage:AAPL'`
	cases := []struct {
		expr   string
		want   string
		isNull bool
	}{
		{expr: "splitcomp(account, ':', 0)", want: "Assets"},
		{expr: "splitcomp(account, ':', 2)", want: "AAPL"},
		{expr: "splitcomp(account, ':', -1)", want: "AAPL"}, // negative index
		{expr: "splitcomp(account, ':', -3)", want: "Assets"},
		{expr: "splitcomp(account, ':', 9)", isNull: true},                  // out of range
		{expr: "splitcomp(account, ':', -9)", isNull: true},                 // negative out of range
		{expr: "splitcomp(account, '/', 0)", want: "Assets:Brokerage:AAPL"}, // delim absent
		{expr: "splitcomp(account, '/', 1)", isNull: true},
	}
	for _, c := range cases {
		v := mustQuery(t, l, `SELECT `+c.expr+` AS v`+where).Rows[0][0]
		if c.isNull {
			if !v.IsNull() {
				t.Errorf("%s = %v, want NULL", c.expr, v)
			}
			continue
		}
		checkStr(t, v, c.want)
	}

	// Empty-string input: one empty component at index 0.
	checkStr(t, mustQuery(t, l, `SELECT splitcomp('', ':', 0) AS v FROM postings LIMIT 1`).Rows[0][0], "")
}

func TestMaxwidth(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"maxwidth(payee, 20)", "Café"},                // fits, unchanged
		{"maxwidth(narration, 11)", "naïve [...]"},     // first word + placeholder fits (11 runes)
		{"maxwidth(narration, 7)", "[...]"},            // not even the first word fits
		{"maxwidth('a   b', 10)", "a b"},               // whitespace collapsed, fits
		{"maxwidth('  a  ', 10)", "a"},                 // leading/trailing trimmed
		{"maxwidth('one two three', 11)", "one [...]"}, // word-dropping loop
		{"maxwidth(payee, 0)", "[...]"},                // zero width
		{"maxwidth('naïve', 5)", "naïve"},              // exact rune width (5 runes, 6 bytes)
	}
	for _, c := range cases {
		v := mustQuery(t, l, `SELECT `+c.expr+` AS v FROM postings LIMIT 1`).Rows[0][0]
		checkStr(t, v, c.want)
	}
}
