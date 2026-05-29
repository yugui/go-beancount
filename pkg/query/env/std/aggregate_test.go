package std_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// groupLedger has two accounts. Expenses:Food has two postings (one without a
// payee, so payee is NULL there); Assets:Cash has three. It drives the
// aggregator tests through GROUP BY.
func groupLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Transaction{
			Date: date(2020, 1, 1), Flag: '*', Payee: "Grocer", Narration: "shop",
			Postings: []ast.Posting{
				{Account: "Expenses:Food", Amount: &ast.Amount{Number: dec(t, "20"), Currency: "USD"}},
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "-20"), Currency: "USD"}},
			},
		},
		&ast.Transaction{
			Date: date(2020, 2, 1), Flag: '*', Narration: "no payee",
			Postings: []ast.Posting{
				{Account: "Expenses:Food", Amount: &ast.Amount{Number: dec(t, "5"), Currency: "USD"}},
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "-5"), Currency: "USD"}},
			},
		},
		&ast.Transaction{
			Date: date(2020, 3, 1), Flag: '*', Payee: "Boss", Narration: "pay",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "100"), Currency: "USD"}},
			},
		},
	})
	return l
}

func TestCountIgnoresNulls(t *testing.T) {
	l := groupLedger(t)
	// count(account) counts every posting; count(payee) skips the NULL payee.
	res := mustQuery(t, l,
		"SELECT account, count(account) AS na, count(payee) AS np GROUP BY account")
	acctCol := column(t, res, "account")
	naCol := column(t, res, "na")
	npCol := column(t, res, "np")
	na := map[string]int64{}
	np := map[string]int64{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[acctCol])
		na[acct], _ = types.AsInt(row[naCol])
		np[acct], _ = types.AsInt(row[npCol])
	}
	if na["Expenses:Food"] != 2 || na["Assets:Cash"] != 3 {
		t.Errorf("count(account): Food=%d Cash=%d, want 2/3", na["Expenses:Food"], na["Assets:Cash"])
	}
	// Expenses:Food: one posting has NULL payee -> count(payee) = 1.
	if np["Expenses:Food"] != 1 {
		t.Errorf("count(payee) Food = %d, want 1 (NULL skipped)", np["Expenses:Food"])
	}
}

func TestSumIntOverload(t *testing.T) {
	l := groupLedger(t)
	// year(date) is an Int column; sum(year) selects the Int overload.
	res := mustQuery(t, l, "SELECT sum(year(date)) AS y FROM postings")
	v := res.Rows[0][0]
	if v.Type() != types.Int {
		t.Fatalf("sum(int) type = %s, want int", v.Type())
	}
	// 5 postings all in 2020 -> 5 * 2020 = 10100.
	if n, _ := types.AsInt(v); n != 10100 {
		t.Errorf("sum(year) = %d, want 10100", n)
	}
}

func TestSumDecimalOverload(t *testing.T) {
	l := groupLedger(t)
	res := mustQuery(t, l, "SELECT account, sum(number) AS total GROUP BY account")
	acctCol := column(t, res, "account")
	totalCol := column(t, res, "total")
	got := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[acctCol])
		if row[totalCol].Type() != types.Decimal {
			t.Fatalf("sum(number) type = %s, want decimal", row[totalCol].Type())
		}
		got[acct] = row[totalCol].Format()
	}
	if got["Expenses:Food"] != "25" {
		t.Errorf("sum Food = %s, want 25", got["Expenses:Food"])
	}
	if got["Assets:Cash"] != "75" {
		t.Errorf("sum Cash = %s, want 75", got["Assets:Cash"])
	}
}

func TestSumPositionOverloadToInventory(t *testing.T) {
	l := groupLedger(t)
	// sum(position) selects the Position overload (vs sum(int)/sum(decimal))
	// and folds into an Inventory.
	res := mustQuery(t, l,
		"SELECT sum(position) AS inv WHERE account = 'Assets:Cash'")
	inv := res.Rows[0][0]
	if inv.Type() != types.Inventory {
		t.Fatalf("sum(position) type = %s, want inventory", inv.Type())
	}
	// -20 + -5 + 100 = 75 USD.
	if got := inv.Format(); got != "(75 USD)" {
		t.Errorf("sum(position) = %s, want (75 USD)", got)
	}
}

func TestSumPositionEmptyGroup(t *testing.T) {
	l := groupLedger(t)
	res := mustQuery(t, l,
		"SELECT sum(position) AS inv WHERE account = 'Nonexistent'")
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (single empty group)", len(res.Rows))
	}
	inv := res.Rows[0][0]
	if inv.Type() != types.Inventory {
		t.Fatalf("type = %s, want inventory", inv.Type())
	}
	if got := inv.Format(); got != "()" {
		t.Errorf("empty sum(position) = %s, want () (empty inventory)", got)
	}
}

func TestSumIntEmptyGroup(t *testing.T) {
	l := groupLedger(t)
	res := mustQuery(t, l, "SELECT sum(year(date)) AS y WHERE account = 'Nonexistent'")
	if n, _ := types.AsInt(res.Rows[0][0]); n != 0 {
		t.Errorf("sum(int) over empty = %d, want 0", n)
	}
}

func TestMinMax(t *testing.T) {
	l := groupLedger(t)
	res := mustQuery(t, l,
		"SELECT account, min(number) AS lo, max(number) AS hi GROUP BY account")
	acctCol := column(t, res, "account")
	loCol := column(t, res, "lo")
	hiCol := column(t, res, "hi")
	lo := map[string]string{}
	hi := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[acctCol])
		lo[acct] = row[loCol].Format()
		hi[acct] = row[hiCol].Format()
	}
	if lo["Assets:Cash"] != "-20" || hi["Assets:Cash"] != "100" {
		t.Errorf("Cash min/max = %s/%s, want -20/100", lo["Assets:Cash"], hi["Assets:Cash"])
	}
	if lo["Expenses:Food"] != "5" || hi["Expenses:Food"] != "20" {
		t.Errorf("Food min/max = %s/%s, want 5/20", lo["Expenses:Food"], hi["Expenses:Food"])
	}
}

func TestFirstLast(t *testing.T) {
	l := groupLedger(t)
	// Over the whole stream ordered by ledger order, first(number) is the
	// first posting's amount (20) and last(number) is the last (100).
	res := mustQuery(t, l, "SELECT first(number) AS f, last(number) AS l FROM postings")
	row := res.Rows[0]
	if got := row[column(t, res, "f")].Format(); got != "20" {
		t.Errorf("first(number) = %s, want 20", got)
	}
	if got := row[column(t, res, "l")].Format(); got != "100" {
		t.Errorf("last(number) = %s, want 100", got)
	}
}

func TestFirstLastSkipsLeadingNullsAndType(t *testing.T) {
	l := groupLedger(t)
	// first(payee)/last(payee): the first transaction's payee is "Grocer",
	// the second has a NULL payee, the third "Boss". first skips no leading
	// NULL here (Grocer is first); last keeps the last non-null (Boss).
	res := mustQuery(t, l, "SELECT first(payee) AS f, last(payee) AS l FROM postings")
	row := res.Rows[0]
	checkStr(t, row[column(t, res, "f")], "Grocer")
	checkStr(t, row[column(t, res, "l")], "Boss")
}

func TestMinMaxFirstLastAllNullGroup(t *testing.T) {
	l := groupLedger(t)
	// Every Expenses:Food posting has a NULL payee for one row; restrict to
	// the NULL-payee transaction so the whole group is NULL on payee.
	res := mustQuery(t, l,
		"SELECT min(payee) AS mn, max(payee) AS mx, first(payee) AS f, last(payee) AS l "+
			"WHERE narration = 'no payee'")
	row := res.Rows[0]
	for _, name := range []string{"mn", "mx", "f", "l"} {
		if v := row[column(t, res, name)]; !v.IsNull() {
			t.Errorf("%s over all-NULL group = %v, want NULL", name, v)
		}
	}
}
