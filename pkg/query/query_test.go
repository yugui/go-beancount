package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func mustQuery(t *testing.T, q string) query.Result {
	t.Helper()
	res, err := query.Query(context.Background(), q, sampleLedger(t))
	if err != nil {
		t.Fatalf("Query(%q): %v", q, err)
	}
	return res
}

func colNames(res query.Result) []string {
	names := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		names[i] = c.Name
	}
	return names
}

// column returns the index of the named output column.
func column(t *testing.T, res query.Result, name string) int {
	t.Helper()
	for i, c := range res.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("no output column %q (have %v)", name, colNames(res))
	return -1
}

func cell(res query.Result, row, col int) types.Value { return res.Rows[row][col] }

func TestSelectStar(t *testing.T) {
	res := mustQuery(t, "SELECT * FROM postings")
	if len(res.Rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(res.Rows))
	}
	// Schema mirrors the postings table column list, starting with type/date.
	if res.Columns[0].Name != "type" || res.Columns[1].Name != "date" {
		t.Fatalf("unexpected leading columns: %v", colNames(res))
	}
	if got := len(res.Columns); got != len(res.Rows[0]) {
		t.Fatalf("row width %d != column count %d", len(res.Rows[0]), got)
	}
}

func TestProjectionAliasAndBareName(t *testing.T) {
	res := mustQuery(t, "SELECT account, number AS amt FROM postings")
	if want := []string{"account", "amt"}; !equalStrings(colNames(res), want) {
		t.Fatalf("columns = %v, want %v", colNames(res), want)
	}
	if res.Columns[0].Type != types.String || res.Columns[1].Type != types.Decimal {
		t.Fatalf("unexpected column types: %+v", res.Columns)
	}
}

func TestWhereNumericComparison(t *testing.T) {
	res := mustQuery(t, "SELECT account WHERE number > 10")
	// Only the +20 and +100 postings exceed 10.
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
}

func TestWhereStringEquality(t *testing.T) {
	res := mustQuery(t, "SELECT account WHERE account = 'Assets:Cash'")
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}
}

func TestWhereRegexMatch(t *testing.T) {
	res := mustQuery(t, "SELECT account WHERE account ~ '^Expenses'")
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
}

func TestWhereBooleanLogic(t *testing.T) {
	res := mustQuery(t, "SELECT account WHERE number > 0 AND NOT account ~ 'Income'")
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (positive non-income postings)", len(res.Rows))
	}
	res = mustQuery(t, "SELECT account WHERE number > 50 OR account = 'Income:Salary'")
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
}

func TestWhereInList(t *testing.T) {
	res := mustQuery(t, "SELECT account WHERE account IN ('Income:Salary', 'Expenses:Food')")
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}
}

func TestWhereInSet(t *testing.T) {
	res := mustQuery(t, "SELECT narration WHERE 'food' IN tags")
	// Two transactions are tagged "food", 2 postings each = 4 rows.
	if len(res.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(res.Rows))
	}
}

func TestFromExprEqualsWhere(t *testing.T) {
	l := sampleLedger(t)
	fromRes, err := query.Query(context.Background(), "SELECT account, number FROM year >= 2021", l)
	if err != nil {
		t.Fatalf("FROM query: %v", err)
	}
	whereRes, err := query.Query(context.Background(), "SELECT account, number WHERE year >= 2021", l)
	if err != nil {
		t.Fatalf("WHERE query: %v", err)
	}
	assertResultsEqual(t, fromRes, whereRes)
}

func TestFromExprAndWhereCombine(t *testing.T) {
	res := mustQuery(t, "SELECT account FROM year >= 2021 WHERE number > 0")
	// 2021 coffee +5 and 2022 salary +100 are the positive postings on/after 2021.
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
}

func TestFromTableReferences(t *testing.T) {
	postings, err := query.Query(context.Background(), "SELECT account FROM postings", sampleLedger(t))
	if err != nil {
		t.Fatalf("FROM postings: %v", err)
	}
	if len(postings.Rows) != 6 {
		t.Fatalf("postings rows = %d, want 6", len(postings.Rows))
	}

	entries, err := query.Query(context.Background(), "SELECT type FROM entries", sampleLedger(t))
	if err != nil {
		t.Fatalf("FROM entries: %v", err)
	}
	if len(entries.Rows) != 3 {
		t.Fatalf("entries rows = %d, want 3 (one per directive)", len(entries.Rows))
	}
}

func TestNullPropagation(t *testing.T) {
	// payee is NULL on the salary-less postings... actually all txns have a
	// payee here; use cost_number which is NULL on every posting (no costs).
	res := mustQuery(t, "SELECT account WHERE cost_number > 0")
	if len(res.Rows) != 0 {
		t.Fatalf("rows = %d, want 0 (NULL > 0 is never TRUE)", len(res.Rows))
	}
	// A comparison with a NULL operand yields NULL, excluded by WHERE.
	res = mustQuery(t, "SELECT account WHERE cost_number = cost_number")
	if len(res.Rows) != 0 {
		t.Fatalf("rows = %d, want 0 (NULL = NULL is NULL)", len(res.Rows))
	}
}

func TestRegexBadPatternRuntimeError(t *testing.T) {
	l := sampleLedger(t)
	// Non-literal pattern forces per-row compilation; an invalid pattern is a
	// runtime error, not a panic. account is never NULL so eval reaches the
	// pattern compile.
	_, err := query.Query(context.Background(), "SELECT account WHERE account ~ payee", l)
	if err != nil {
		t.Fatalf("valid non-literal pattern errored: %v", err)
	}

	// A literal invalid pattern is caught at compile time.
	_, cerr := query.Compile("SELECT account WHERE account ~ '['", l)
	if cerr == nil {
		t.Fatal("compiling an invalid literal pattern did not error")
	}
}

func TestRegexBadPatternFromColumn(t *testing.T) {
	l := &ast.Ledger{}
	l.Insert(&ast.Transaction{
		Date:      date(2020, 1, 1),
		Flag:      '*',
		Narration: "[", // invalid regex
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "1"), Currency: "USD"}},
		},
	})
	res, err := query.Query(context.Background(), "SELECT account WHERE account ~ narration", l)
	if err == nil {
		t.Fatalf("expected runtime regex error, got %d rows", len(res.Rows))
	}
	if !strings.Contains(err.Error(), "regular expression") {
		t.Fatalf("error = %v, want a regex compile error", err)
	}
}

func TestMetaSugar(t *testing.T) {
	res := mustQuery(t, "SELECT account, meta('category') AS cat WHERE account = 'Expenses:Food'")
	catCol := column(t, res, "cat")
	var present, null int
	for _, row := range res.Rows {
		if row[catCol].IsNull() {
			null++
		} else {
			present++
			if s, _ := types.AsString(row[catCol]); s != "groceries" {
				t.Fatalf("category = %q, want groceries", s)
			}
		}
	}
	if present != 1 || null != 1 {
		t.Fatalf("present=%d null=%d, want 1 and 1 (only the grocery posting has the key)", present, null)
	}
}

// TestBalanceCumulativeOverSelectedRows pins the documented register
// semantics: balance is the cumulative inventory of the rows the query SELECTS
// (those passing FROM/WHERE), in scan order, inclusive of the current row — not
// the full-stream global inventory. Filtering to Expenses:Food yields a running
// total over just those rows (20, then 25), so the intervening -20 cash posting
// does not reset or contribute to it.
func TestBalanceCumulativeOverSelectedRows(t *testing.T) {
	res := mustQuery(t, "SELECT account, balance WHERE account = 'Expenses:Food'")
	bcol := column(t, res, "balance")
	if res.Columns[bcol].Type != types.Inventory {
		t.Fatalf("balance column type = %v, want Inventory", res.Columns[bcol].Type)
	}
	wantFood := []string{"(20 USD)", "(25 USD)"}
	if len(res.Rows) != len(wantFood) {
		t.Fatalf("WHERE account='Expenses:Food': rows = %d, want %d", len(res.Rows), len(wantFood))
	}
	for i, w := range wantFood {
		if got := cell(res, i, bcol).Format(); got != w {
			t.Errorf("balance (WHERE account='Expenses:Food'): row %d = %s, want %s", i, got, w)
		}
	}

	res = mustQuery(t, "SELECT account, balance WHERE account = 'Assets:Cash'")
	bcol = column(t, res, "balance")
	wantCash := []string{"(-20 USD)", "(-25 USD)", "(75 USD)"}
	if len(res.Rows) != len(wantCash) {
		t.Fatalf("cash rows = %d, want %d", len(res.Rows), len(wantCash))
	}
	for i, w := range wantCash {
		if got := cell(res, i, bcol).Format(); got != w {
			t.Errorf("balance (WHERE account='Assets:Cash'): row %d = %s, want %s", i, got, w)
		}
	}
}

// TestBalanceMultiCurrencyRegister reproduces the bug-report scenario: a
// multi-currency account statement. balance must show only the selected
// (Assets:Cash) rows' running inventory, posting-by-posting, never the lots
// held in the filtered-out Assets:A / Assets:B accounts.
func TestBalanceMultiCurrencyRegister(t *testing.T) {
	mkAmt := func(n, cur string) *ast.Amount { return &ast.Amount{Number: dec(t, n), Currency: cur} }
	l := &ast.Ledger{}
	l.Insert(&ast.Transaction{
		Date: date(1970, 1, 1), Flag: '*', Narration: "txn",
		Postings: []ast.Posting{
			{Account: "Assets:A", Amount: mkAmt("10", "A")},
			{Account: "Assets:Cash", Amount: mkAmt("-100", "JPY")},
		},
	})
	l.Insert(&ast.Transaction{
		Date: date(1970, 1, 1), Flag: '*', Narration: "txn",
		Postings: []ast.Posting{
			{Account: "Assets:B", Amount: mkAmt("10", "B")},
			{Account: "Assets:Cash", Amount: mkAmt("-1.00", "USD")},
		},
	})
	l.Insert(&ast.Transaction{
		Date: date(1970, 1, 2), Flag: '*', Narration: "sell",
		Postings: []ast.Posting{
			{Account: "Assets:A", Amount: mkAmt("-5", "A")},
			{Account: "Assets:B", Amount: mkAmt("-5", "B")},
			{Account: "Assets:Cash", Amount: mkAmt("150", "JPY")},
			{Account: "Assets:Cash", Amount: mkAmt("0.50", "USD")},
		},
	})

	res, err := query.Query(context.Background(),
		`SELECT date, account, position, balance WHERE account = "Assets:Cash"`, l)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	bcol := column(t, res, "balance")
	want := []string{
		"(-100 JPY)",
		"(-100 JPY, -1.00 USD)",
		"(50 JPY, -1.00 USD)",
		"(50 JPY, -0.50 USD)",
	}
	if len(res.Rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(res.Rows), len(want))
	}
	for i, w := range want {
		if got := cell(res, i, bcol).Format(); got != w {
			t.Errorf("register balance: row %d = %s, want %s", i, got, w)
		}
	}
}

// TestCloseOnPostings verifies that CLOSE ON D drops postings with dates on or
// after D from the postings table (strict less-than boundary).
func TestCloseOnPostings(t *testing.T) {
	// sampleLedger: 2020-01-15 (2 postings), 2021-06-01 (2), 2022-03-10 (2).
	cases := []struct {
		name string
		q    string
		want int
	}{
		{"before all entries", "SELECT account, number FROM postings CLOSE ON 2021-01-01", 2},
		{"between entries", "SELECT account, number FROM postings CLOSE ON 2022-01-01", 4},
		{"strict equality boundary", "SELECT account, number FROM postings CLOSE ON 2020-01-15", 0},
		{"after all entries", "SELECT account, number FROM postings CLOSE ON 2030-01-01", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := mustQuery(t, tc.q)
			if len(res.Rows) != tc.want {
				t.Fatalf("rows = %d, want %d", len(res.Rows), tc.want)
			}
		})
	}
}

// TestCloseOnEntries verifies that CLOSE ON D drops entries dated on or after D
// from the entries table.
func TestCloseOnEntries(t *testing.T) {
	// sampleLedger has 3 transactions; CLOSE ON 2022-01-01 drops the 2022-03-10 one.
	res := mustQuery(t, "SELECT type FROM entries CLOSE ON 2022-01-01")
	if len(res.Rows) != 2 {
		t.Fatalf("CLOSE ON entries: rows = %d, want 2", len(res.Rows))
	}
}

// TestCloseOnWithWhere verifies that CLOSE ON and WHERE combine correctly: a
// row must pass both the date boundary and the WHERE predicate.
func TestCloseOnWithWhere(t *testing.T) {
	// CLOSE ON 2022-01-01 (4 postings from 2020 and 2021),
	// then WHERE account = 'Assets:Cash' (one per transaction = 2 rows).
	res := mustQuery(t, "SELECT account, number FROM postings CLOSE ON 2022-01-01 WHERE account = 'Assets:Cash'")
	if len(res.Rows) != 2 {
		t.Fatalf("CLOSE ON + WHERE: rows = %d, want 2", len(res.Rows))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertResultsEqual(t *testing.T, a, b query.Result) {
	t.Helper()
	if !equalStrings(colNames(a), colNames(b)) {
		t.Fatalf("columns differ: %v vs %v", colNames(a), colNames(b))
	}
	if len(a.Rows) != len(b.Rows) {
		t.Fatalf("row counts differ: %d vs %d", len(a.Rows), len(b.Rows))
	}
	for i := range a.Rows {
		for j := range a.Rows[i] {
			if a.Rows[i][j].Compare(b.Rows[i][j]) != 0 {
				t.Fatalf("row %d col %d differ: %v vs %v", i, j, a.Rows[i][j], b.Rows[i][j])
			}
		}
	}
}
