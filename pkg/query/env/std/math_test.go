package std_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

func checkDec(t *testing.T, v types.Value, want string) {
	t.Helper()
	if v.IsNull() {
		t.Fatalf("value is NULL, want %s", want)
	}
	if got := v.Format(); got != want {
		t.Errorf("decimal = %s, want %s", got, want)
	}
}

func TestAbsDecimal(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"abs(-20)", "20"},
		{"abs(20)", "20"},
		{"abs(0)", "0"},
		{"abs(-2.5)", "2.5"},
		{"abs(2.5)", "2.5"},
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkDec(t, v, c.want)
	}
}

func TestNegDecimal(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"neg(20)", "-20"},
		{"neg(-20)", "20"},
		{"neg(0)", "0"}, // zero negates to a plain zero, not -0
		{"neg(2.5)", "-2.5"},
		{"neg(neg(7))", "7"}, // double negation is identity
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkDec(t, v, c.want)
	}
}

// TestAbsNegTyped covers the Amount/Position/Inventory overloads, which map
// the units number while preserving currency and cost lots.
func TestAbsNegTyped(t *testing.T) {
	l := scalarLedger(t)

	// Amount neg keeps the currency.
	v := mustQuery(t, l,
		"SELECT neg(weight) AS n FROM postings WHERE account = 'Assets:Cash'").Rows[0][0]
	if v.Type() != types.Amount || v.Format() != "20 USD" {
		t.Errorf("neg(weight) = %v, want 20 USD", v)
	}

	// Position abs/neg map the units number.
	v = mustQuery(t, l,
		"SELECT number(abs(position)) AS a FROM postings WHERE account = 'Assets:Cash'").Rows[0][0]
	checkDec(t, v, "20")
	v = mustQuery(t, l,
		"SELECT number(neg(position)) AS n FROM postings WHERE account = 'Assets:Brokerage:AAPL'").Rows[0][0]
	checkDec(t, v, "-2")

	// Inventory neg flips a lot-free balance.
	v = mustQuery(t, l,
		"SELECT neg(balance) AS nb FROM postings WHERE account = 'Assets:Cash'").Rows[0][0]
	if v.Type() != types.Inventory || v.Format() != "(20 USD)" {
		t.Errorf("neg(balance) = %v, want (20 USD)", v)
	}
}

func TestSafediv(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		{"safediv(10, 4)", "2.5"},
		{"safediv(7, 2)", "3.5"},
		{"safediv(-10, 4)", "-2.5"},
		{"safediv(10, -4)", "-2.5"},
		{"safediv(-10, -4)", "2.5"},
		{"safediv(0, 5)", "0"},
		{"safediv(10, 0)", "0"}, // zero divisor is safe, yields 0
		{"safediv(0, 0)", "0"},  // zero/zero is also safe
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkDec(t, v, c.want)
	}

	// A non-terminating quotient is carried to the operator's working precision.
	v := mustQuery(t, l, "SELECT safediv(1, 3) AS v FROM postings LIMIT 1").Rows[0][0]
	if got := v.Format(); got != "0.3333333333333333333333333333" {
		t.Errorf("safediv(1, 3) = %s, want 28-digit 0.333...", got)
	}
}

// TestRound exhaustively covers half-to-even rounding in both sign directions,
// to a given number of places, and to the left of the point (negative digits).
func TestRound(t *testing.T) {
	l := scalarLedger(t)
	cases := []struct{ expr, want string }{
		// Half-to-even at the integer boundary, both directions.
		{"round(safediv(1, 2))", "0"},   // 0.5 -> 0 (even)
		{"round(safediv(3, 2))", "2"},   // 1.5 -> 2 (even)
		{"round(safediv(5, 2))", "2"},   // 2.5 -> 2 (even)
		{"round(safediv(7, 2))", "4"},   // 3.5 -> 4 (even)
		{"round(safediv(9, 2))", "4"},   // 4.5 -> 4 (even)
		{"round(safediv(-5, 2))", "-2"}, // -2.5 -> -2 (even)
		{"round(safediv(-7, 2))", "-4"}, // -3.5 -> -4 (even)
		// Non-half cases.
		{"round(safediv(12, 5))", "2"}, // 2.4 -> 2
		{"round(safediv(13, 5))", "3"}, // 2.6 -> 3
		// To a number of places.
		{"round(safediv(2567, 1000), 2)", "2.57"},
		{"round(safediv(-2567, 1000), 2)", "-2.57"},
		{"round(safediv(5, 2), 0)", "2"}, // explicit 0 places == 1-arg form
		// Negative digits round to the left of the point (half-to-even).
		{"round(1234.5, -2)", "1200"},
		{"round(1350, -2)", "1400"},
		{"round(1250, -2)", "1200"}, // 12.5 hundreds -> 12 (even)
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v FROM postings LIMIT 1").Rows[0][0]
		checkDec(t, v, c.want)
	}
}

// TestNumericNullPropagation verifies that a NULL argument short-circuits to a
// typed NULL across the numeric helpers.
func TestNumericNullPropagation(t *testing.T) {
	l := scalarLedger(t)
	// cost(position) is NULL for the cash posting (no lot).
	exprs := []string{
		"abs(number(cost(position)))",
		"neg(number(cost(position)))",
		"safediv(number(cost(position)), 2)",
		"round(number(cost(position)))",
	}
	for _, expr := range exprs {
		v := mustQuery(t, l,
			"SELECT "+expr+" AS v FROM postings WHERE account = 'Assets:Cash'").Rows[0][0]
		if !v.IsNull() {
			t.Errorf("%s over NULL = %v, want NULL", expr, v)
		}
	}
}
