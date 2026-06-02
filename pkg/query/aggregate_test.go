package query_test

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestGroupByCount(t *testing.T) {
	res := mustQuery(t, "SELECT account, count(account) AS n GROUP BY account")
	got := map[string]int64{}
	nCol := column(t, res, "n")
	acctCol := column(t, res, "account")
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[acctCol])
		n, _ := types.AsInt(row[nCol])
		got[acct] = n
	}
	want := map[string]int64{
		"Expenses:Food": 2,
		"Assets:Cash":   3,
		"Income:Salary": 1,
	}
	for acct, n := range want {
		if got[acct] != n {
			t.Errorf("count(%s) = %d, want %d", acct, got[acct], n)
		}
	}
	if len(res.Rows) != len(want) {
		t.Fatalf("group count = %d, want %d", len(res.Rows), len(want))
	}
}

func TestGroupBySumDecimal(t *testing.T) {
	res := mustQuery(t, "SELECT account, sum(number) AS total GROUP BY account")
	totalCol := column(t, res, "total")
	acctCol := column(t, res, "account")
	got := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[acctCol])
		got[acct] = row[totalCol].Format()
	}
	// Expenses:Food = 20 + 5 = 25; Assets:Cash = -20 -5 +100 = 75.
	if got["Expenses:Food"] != "25" {
		t.Errorf("sum(Expenses:Food) = %s, want 25", got["Expenses:Food"])
	}
	if got["Assets:Cash"] != "75" {
		t.Errorf("sum(Assets:Cash) = %s, want 75", got["Assets:Cash"])
	}
}

func TestAggregateNoGroupBy(t *testing.T) {
	res := mustQuery(t, "SELECT count(account)")
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (single implicit group)", len(res.Rows))
	}
	n, _ := types.AsInt(cell(res, 0, 0))
	if n != 6 {
		t.Fatalf("count(account) = %d, want 6", n)
	}
}

func TestAggregateOverEmptyLedger(t *testing.T) {
	res, err := query.Query(context.Background(), "SELECT count(account)", emptyLedger())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (count over empty is one row)", len(res.Rows))
	}
	n, _ := types.AsInt(cell(res, 0, 0))
	if n != 0 {
		t.Fatalf("count over empty = %d, want 0", n)
	}
}

func TestSumPositionToInventory(t *testing.T) {
	res := mustQuery(t, "SELECT sum(position) AS inv WHERE account = 'Expenses:Food'")
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	inv := cell(res, 0, 0)
	if inv.Type() != types.Inventory {
		t.Fatalf("result type = %s, want inventory", inv.Type())
	}
	// 20 USD + 5 USD = 25 USD.
	if got := inv.Format(); got != "(25 USD)" {
		t.Fatalf("inventory = %s, want (25 USD)", got)
	}
}

func TestOrderByAscDesc(t *testing.T) {
	asc := mustQuery(t, "SELECT number FROM postings ORDER BY number ASC")
	for i := 1; i < len(asc.Rows); i++ {
		if asc.Rows[i-1][0].Compare(asc.Rows[i][0]) > 0 {
			t.Fatalf("ASC not non-decreasing at row %d", i)
		}
	}

	desc := mustQuery(t, "SELECT number FROM postings ORDER BY number DESC")
	for i := 1; i < len(desc.Rows); i++ {
		if desc.Rows[i-1][0].Compare(desc.Rows[i][0]) < 0 {
			t.Fatalf("DESC not non-increasing at row %d", i)
		}
	}
	// DESC is the reverse of ASC.
	n := len(asc.Rows)
	if asc.Rows[0][0].Compare(desc.Rows[n-1][0]) != 0 {
		t.Fatal("DESC is not the reverse ordering of ASC")
	}
}

func TestOrderByAggregateAlias(t *testing.T) {
	res := mustQuery(t, "SELECT account, sum(number) AS total GROUP BY account ORDER BY total DESC")
	totalCol := column(t, res, "total")
	for i := 1; i < len(res.Rows); i++ {
		if res.Rows[i-1][totalCol].Compare(res.Rows[i][totalCol]) < 0 {
			t.Fatalf("ORDER BY total DESC not descending at row %d", i)
		}
	}
	// Highest total (Assets:Cash = 75) sorts first.
	acctCol := column(t, res, "account")
	first, _ := types.AsString(res.Rows[0][acctCol])
	if first != "Assets:Cash" {
		t.Fatalf("first group = %q, want Assets:Cash", first)
	}
}

func TestHavingFiltersGroupsByAggregate(t *testing.T) {
	// Per-account number sums: Expenses:Food=25, Assets:Cash=75,
	// Income:Salary=-100. HAVING keeps only the positive totals.
	res := mustQuery(t, "SELECT account, sum(number) AS total GROUP BY account HAVING sum(number) > 0")
	acctCol := column(t, res, "account")
	got := map[string]bool{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[acctCol])
		got[acct] = true
	}
	want := map[string]bool{"Expenses:Food": true, "Assets:Cash": true}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for acct := range want {
		if !got[acct] {
			t.Errorf("missing group %q", acct)
		}
	}
}

func TestHavingAggregateNotInSelect(t *testing.T) {
	// HAVING may reference an aggregate absent from the target list.
	// Per-account posting counts: Expenses:Food=2, Assets:Cash=3,
	// Income:Salary=1. Only counts greater than one survive.
	res := mustQuery(t, "SELECT account GROUP BY account HAVING count(account) > 1")
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
}

func TestHavingGroupedColumnNoAggregate(t *testing.T) {
	// Relaxation from upstream: HAVING need not contain an aggregate; a
	// grouped-column predicate is accepted.
	res := mustQuery(t, "SELECT account GROUP BY account HAVING account = 'Assets:Cash'")
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	acct, _ := types.AsString(cell(res, 0, column(t, res, "account")))
	if acct != "Assets:Cash" {
		t.Fatalf("account = %q, want Assets:Cash", acct)
	}
}

func TestBareHavingWholeTable(t *testing.T) {
	// Relaxation from upstream: HAVING without GROUP BY aggregates the whole
	// table into one group. The sample ledger has six postings.
	kept := mustQuery(t, "SELECT count(account) AS n HAVING count(account) > 3")
	if len(kept.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (whole-table group passes HAVING)", len(kept.Rows))
	}
	n, _ := types.AsInt(cell(kept, 0, 0))
	if n != 6 {
		t.Fatalf("count = %d, want 6", n)
	}

	dropped := mustQuery(t, "SELECT count(account) AS n HAVING count(account) > 100")
	if len(dropped.Rows) != 0 {
		t.Fatalf("rows = %d, want 0 (whole-table group fails HAVING)", len(dropped.Rows))
	}
}

func TestLimit(t *testing.T) {
	res := mustQuery(t, "SELECT account FROM postings ORDER BY number DESC LIMIT 2")
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
}

func TestDistinct(t *testing.T) {
	res := mustQuery(t, "SELECT DISTINCT account FROM postings")
	seen := map[string]bool{}
	for _, row := range res.Rows {
		s, _ := types.AsString(row[0])
		if seen[s] {
			t.Fatalf("duplicate account %q in DISTINCT result", s)
		}
		seen[s] = true
	}
	if len(res.Rows) != 3 {
		t.Fatalf("distinct accounts = %d, want 3", len(res.Rows))
	}
}
