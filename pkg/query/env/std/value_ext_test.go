package std_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

// sum(position) over the AAPL row yields a lot-bearing inventory
// (2 AAPL {10 USD}), exercising the cost-aware paths; sum(position) over the
// cash row is lot-free (-20 USD), exercising the lot-free branch.

func TestInventoryUnits(t *testing.T) {
	l := scalarLedger(t)
	// Lot-bearing: units strips the cost.
	v := mustQuery(t, l,
		"SELECT units(sum(position)) AS u FROM postings WHERE account = 'Assets:Brokerage:AAPL'").Rows[0][0]
	if v.Type() != types.Inventory || v.Format() != "(2 AAPL)" {
		t.Errorf("units = %v, want (2 AAPL)", v)
	}
	// Lot-free: units is the inventory unchanged.
	v = mustQuery(t, l,
		"SELECT units(sum(position)) AS u FROM postings WHERE account = 'Assets:Cash'").Rows[0][0]
	if v.Type() != types.Inventory || v.Format() != "(-20 USD)" {
		t.Errorf("units(lot-free) = %v, want (-20 USD)", v)
	}
}

func TestInventoryCost(t *testing.T) {
	l := scalarLedger(t)
	// Lot-bearing: cost realizes units x lot price in the lot currency.
	v := mustQuery(t, l,
		"SELECT cost(sum(position)) AS c FROM postings WHERE account = 'Assets:Brokerage:AAPL'").Rows[0][0]
	if v.Type() != types.Inventory || v.Format() != "(20 USD)" {
		t.Errorf("cost = %v, want (20 USD)", v)
	}
	// Lot-free: cost is the units amount unchanged.
	v = mustQuery(t, l,
		"SELECT cost(sum(position)) AS c FROM postings WHERE account = 'Assets:Cash'").Rows[0][0]
	if v.Type() != types.Inventory || v.Format() != "(-20 USD)" {
		t.Errorf("cost(lot-free) = %v, want (-20 USD)", v)
	}
}

func TestOnly(t *testing.T) {
	l := scalarLedger(t)
	// Present currency: sums the units of that currency.
	v := mustQuery(t, l,
		"SELECT only('AAPL', sum(position)) AS a FROM postings WHERE account = 'Assets:Brokerage:AAPL'").Rows[0][0]
	if v.Type() != types.Amount || v.Format() != "2 AAPL" {
		t.Errorf("only('AAPL', ...) = %v, want 2 AAPL", v)
	}
	// Absent currency: a zero amount in that currency, not NULL.
	v = mustQuery(t, l,
		"SELECT only('EUR', sum(position)) AS a FROM postings WHERE account = 'Assets:Brokerage:AAPL'").Rows[0][0]
	if v.Type() != types.Amount || v.Format() != "0 EUR" {
		t.Errorf("only('EUR', ...) = %v, want 0 EUR", v)
	}
}

func TestEmpty(t *testing.T) {
	l := scalarLedger(t)
	res := mustQuery(t, l,
		`SELECT empty(sum(position)) AS ne,
		        empty(filter_currency(sum(position), 'EUR')) AS e
		 FROM postings WHERE account = 'Assets:Brokerage:AAPL'`)
	row := res.Rows[0]
	if b, _ := types.AsBool(row[column(t, res, "ne")]); b {
		t.Errorf("empty(non-empty) = true, want false")
	}
	if b, _ := types.AsBool(row[column(t, res, "e")]); !b {
		t.Errorf("empty(filtered-to-nothing) = false, want true")
	}
}

func TestFilterCurrency(t *testing.T) {
	l := scalarLedger(t)
	// Position overload: matching currency returns the position, else NULL.
	res := mustQuery(t, l,
		`SELECT filter_currency(position, 'AAPL') AS keep,
		        filter_currency(position, 'EUR') AS drop
		 FROM postings WHERE account = 'Assets:Brokerage:AAPL'`)
	row := res.Rows[0]
	if k := row[column(t, res, "keep")]; k.Type() != types.Position {
		t.Errorf("filter_currency(position, 'AAPL') = %v, want a position", k)
	}
	if !row[column(t, res, "drop")].IsNull() {
		t.Errorf("filter_currency(position, 'EUR') = %v, want NULL", row[column(t, res, "drop")])
	}

	// Inventory overload: a match keeps the position (preserving its lot);
	// no match yields an empty inventory (not NULL — unlike the Position form).
	res = mustQuery(t, l,
		`SELECT filter_currency(sum(position), 'AAPL') AS keep,
		        filter_currency(sum(position), 'EUR') AS none
		 FROM postings WHERE account = 'Assets:Brokerage:AAPL'`)
	row = res.Rows[0]
	if inv := row[column(t, res, "keep")]; inv.Type() != types.Inventory || inv.Format() != "(2 AAPL {10 USD})" {
		t.Errorf("filter_currency(inv, 'AAPL') = %v, want (2 AAPL {10 USD})", inv)
	}
	if inv := row[column(t, res, "none")]; inv.Type() != types.Inventory || inv.Format() != "()" {
		t.Errorf("filter_currency(inv, 'EUR') = %v, want empty inventory ()", inv)
	}
}

func TestCommodity(t *testing.T) {
	l := scalarLedger(t)
	checkStr(t, mustQuery(t, l,
		"SELECT commodity(weight) AS v FROM postings WHERE account = 'Assets:Cash'").Rows[0][0], "USD")
}

func TestLengthSet(t *testing.T) {
	l := scalarLedger(t)
	// tags {food, weekly} -> 2; links is empty -> 0.
	res := mustQuery(t, l, "SELECT length(tags) AS t, length(links) AS l FROM postings LIMIT 1")
	row := res.Rows[0]
	checkInt(t, row[column(t, res, "t")], 2)
	checkInt(t, row[column(t, res, "l")], 0)
}

func TestRootN(t *testing.T) {
	l := scalarLedger(t)
	acct := "FROM postings WHERE account = 'Assets:Brokerage:AAPL'" // 3 components
	cases := []struct {
		expr string
		want string
	}{
		{"root(account, 0)", ""},                      // zero components
		{"root(account, 1)", "Assets"},                // first component
		{"root(account, 2)", "Assets:Brokerage"},      //
		{"root(account, 3)", "Assets:Brokerage:AAPL"}, // exactly the depth
		{"root(account, 9)", "Assets:Brokerage:AAPL"}, // beyond depth -> full
		{"root(account, -1)", ""},                     // negative clamps to 0
	}
	for _, c := range cases {
		v := mustQuery(t, l, "SELECT "+c.expr+" AS v "+acct).Rows[0][0]
		checkStr(t, v, c.want)
	}
}
