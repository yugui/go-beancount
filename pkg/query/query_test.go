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

// TestOpenOnPostingsSyntheticOpenings verifies OPEN ON D folds pre-D
// postings into synthesized opening transactions per account: asset and
// liability accounts carry the cumulative balance themselves (paired with
// account_previous_balances), while income and expense accounts are reset
// across D — their cumulative balance is transferred to
// account_previous_earnings and the opposing leg lands on
// account_previous_balances.
//
// sampleLedger pre-2022 totals:
//
//	Assets:Cash   = -25 USD
//	Expenses:Food = +25 USD
//	Income:Salary =   0 USD (no pre-D activity, omitted)
//
// 2022-03-10 (post-D) contributes Assets:Cash +100 USD / Income:Salary -100 USD.
//
// Resulting per-account sums:
//
//	Assets:Cash       = -25 + 100         = 75
//	Income:Salary     =        -100       = -100
//	Earnings:Previous = +25 (transfer)    = 25
//	Opening-Balances  = +25 (Assets pair) + -25 (Expenses pair) = 0
//
// Expenses:Food does not appear in the result because the opening leaves
// the income/expense account itself unposted and there is no post-D
// activity on it.
func TestOpenOnPostingsSyntheticOpenings(t *testing.T) {
	res := mustQuery(t,
		"SELECT account, sum(number) AS total FROM postings OPEN ON 2022-01-01 GROUP BY account ORDER BY account")
	totals := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[0])
		d, _ := types.AsDecimal(row[1])
		totals[acct] = d.String()
	}

	want := map[string]string{
		"Assets:Cash":       "75",
		"Income:Salary":     "-100",
		"Opening-Balances":  "0",
		"Earnings:Previous": "25",
	}
	if len(totals) != len(want) {
		t.Fatalf("groups = %d, want %d (got %v)", len(totals), len(want), totals)
	}
	for k, v := range want {
		if totals[k] != v {
			t.Errorf("TestOpenOnPostingsSyntheticOpenings: sum(number) for %s = %s, want %s", k, totals[k], v)
		}
	}
}

// TestOpenOnEntriesShowsSyntheticNarrations confirms that synthesized
// opening-balance transactions surface on the entries table with their
// scope-set narration.
func TestOpenOnEntriesShowsSyntheticNarrations(t *testing.T) {
	res := mustQuery(t,
		"SELECT narration FROM entries OPEN ON 2022-01-01 WHERE type = 'transaction' ORDER BY narration")

	var openingsSeen int
	for _, row := range res.Rows {
		n, _ := types.AsString(row[0])
		if strings.HasPrefix(n, "Opening balance for '") {
			openingsSeen++
		}
	}
	if openingsSeen != 2 {
		t.Fatalf("opening narrations = %d, want 2 (Assets:Cash, Expenses:Food)", openingsSeen)
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

// TestClearZeroesIncomeAndExpenseTotals verifies CLEAR transfers income+expense
// balances to Earnings:Current: sum(number) per income/expense account is zero,
// and Earnings:Current carries the offset.
func TestClearZeroesIncomeAndExpenseTotals(t *testing.T) {
	res := mustQuery(t,
		"SELECT account, sum(number) AS total FROM postings CLEAR GROUP BY account ORDER BY account")
	totals := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[0])
		d, _ := types.AsDecimal(row[1])
		totals[acct] = d.String()
	}

	// sampleLedger:
	//   Expenses:Food sum = 20 + 5 = 25
	//   Income:Salary sum = -100
	//   Assets:Cash sum = -20 - 5 + 100 = 75
	// After CLEAR:
	//   Expenses:Food = 0 (cleared: -25 leg cancels the +25 balance)
	//   Income:Salary = 0 (cleared: +100 leg cancels the -100 balance)
	//   Assets:Cash = 75 (untouched)
	//   Earnings:Current carries the original balances: +25 + (-100) = -75
	want := map[string]string{
		"Assets:Cash":      "75",
		"Expenses:Food":    "0",
		"Income:Salary":    "0",
		"Earnings:Current": "-75",
	}
	if len(totals) != len(want) {
		t.Fatalf("groups = %d, want %d (got %v)", len(totals), len(want), totals)
	}
	for k, v := range want {
		if totals[k] != v {
			t.Errorf("sum(number) for %s = %s, want %s", k, totals[k], v)
		}
	}
}

// TestClearOnEntriesShowsClearingNarrations verifies that synthesized clearing
// transactions surface on the entries table, identifiable by their metadata.
func TestClearOnEntriesShowsClearingNarrations(t *testing.T) {
	res := mustQuery(t,
		"SELECT narration FROM entries CLEAR WHERE meta('__synthetic__') = 'clearing'")

	// sampleLedger has Income:Salary and Expenses:Food postings, so two
	// clearing transactions are synthesized.
	if len(res.Rows) != 2 {
		t.Fatalf("clearing rows = %d, want 2 (Income:Salary, Expenses:Food)", len(res.Rows))
	}
}

// TestClearWithCloseUsesCloseMinusOneDay verifies CLOSE + CLEAR through the
// facade: clearings reflect only the pre-CLOSE balance and are dated
// (Close − 1).
func TestClearWithCloseUsesCloseMinusOneDay(t *testing.T) {
	// CLOSE ON 2022-01-01 keeps the 2020 and 2021 postings only.
	// Expenses:Food pre-2022 = 20 + 5 = 25; Income:Salary pre-2022 = 0;
	// so only Expenses:Food gets a clearing. Earnings:Current carries +25
	// (the routing leg carries the original balance).
	res := mustQuery(t,
		"SELECT account, sum(number) AS total FROM postings CLOSE ON 2022-01-01 CLEAR GROUP BY account ORDER BY account")
	totals := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[0])
		d, _ := types.AsDecimal(row[1])
		totals[acct] = d.String()
	}
	want := map[string]string{
		"Assets:Cash":      "-25",
		"Expenses:Food":    "0",
		"Earnings:Current": "25",
	}
	if len(totals) != len(want) {
		t.Fatalf("groups = %d, want %d (got %v)", len(totals), len(want), totals)
	}
	for k, v := range want {
		if totals[k] != v {
			t.Errorf("sum(number) for %s = %s, want %s", k, totals[k], v)
		}
	}
}

// TestClearWithOpenZeroesIncomeAndExpense verifies OPEN + CLEAR: the
// OPEN-summarized stream (synthesized openings on income/expense + post-D
// activity) is then cleared, yielding zero sums on every income/expense
// account.
func TestClearWithOpenZeroesIncomeAndExpense(t *testing.T) {
	res := mustQuery(t,
		"SELECT account, sum(number) AS total FROM postings OPEN ON 2022-01-01 CLEAR GROUP BY account ORDER BY account")
	totals := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[0])
		d, _ := types.AsDecimal(row[1])
		totals[acct] = d.String()
	}
	// After OPEN ON D + CLEAR no income/expense balance leaks across either
	// boundary: any income/expense account that appears in the result sums
	// to zero. Income:Salary appears (it has post-D activity that CLEAR
	// then zeros). Expenses:Food does not appear at all — OPEN leaves the
	// income/expense account itself unposted and there is no post-D
	// expense activity for CLEAR to transfer.
	for acct, total := range totals {
		if !strings.HasPrefix(acct, "Income:") && !strings.HasPrefix(acct, "Expenses:") {
			continue
		}
		if total != "0" {
			t.Errorf("%s total after OPEN+CLEAR = %s, want 0", acct, total)
		}
	}
	if _, ok := totals["Earnings:Current"]; !ok {
		t.Errorf("Earnings:Current missing from results")
	}
}

// TestOpenResetsIncomeAccountAcrossBoundary is a regression test for the
// per-account opening shape: an income/expense account's pre-D balance
// must NOT remain on the account itself across OPEN ON D. The cumulative
// pre-D income/expense balance transfers to account_previous_earnings,
// and the account itself resets to zero so only postings dated >= D
// contribute to the period's totals. This matches beanquery's summarize
// semantics on the same fixture.
//
// Fixture: one yearly Assets/Income pair for 2020, 2021, 2022. With
// OPEN ON 2021-01-01 and CLOSE ON 2022-01-01 only the 2021 transaction
// is in the period; the 2020 pre-D activity (Assets +10 / Income -10)
// is summarized into the opening, the 2022 transaction is dropped.
func TestOpenResetsIncomeAccountAcrossBoundary(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:A"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Income:A"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "y2020",
			Postings: []ast.Posting{
				{Account: "Assets:A", Amount: &ast.Amount{Number: dec(t, "10"), Currency: "JPY"}},
				{Account: "Income:A", Amount: &ast.Amount{Number: dec(t, "-10"), Currency: "JPY"}},
			},
		},
		&ast.Transaction{
			Date: date(2021, 6, 1), Flag: '*', Narration: "y2021",
			Postings: []ast.Posting{
				{Account: "Assets:A", Amount: &ast.Amount{Number: dec(t, "100"), Currency: "JPY"}},
				{Account: "Income:A", Amount: &ast.Amount{Number: dec(t, "-100"), Currency: "JPY"}},
			},
		},
		&ast.Transaction{
			Date: date(2022, 6, 1), Flag: '*', Narration: "y2022",
			Postings: []ast.Posting{
				{Account: "Assets:A", Amount: &ast.Amount{Number: dec(t, "1000"), Currency: "JPY"}},
				{Account: "Income:A", Amount: &ast.Amount{Number: dec(t, "-1000"), Currency: "JPY"}},
			},
		},
	})

	res, err := query.Query(context.Background(),
		"SELECT account, sum(number) AS total FROM postings OPEN ON 2021-01-01 CLOSE ON 2022-01-01 GROUP BY account ORDER BY account", l)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	totals := map[string]string{}
	for _, row := range res.Rows {
		acct, _ := types.AsString(row[0])
		d, _ := types.AsDecimal(row[1])
		totals[acct] = d.String()
	}
	// Expected (matches beanquery):
	//   Assets:A          =  10 (opening) + 100 (period)            = 110
	//   Income:A          =        -100  (period only; pre-D reset) = -100
	//   Earnings:Previous =   -10        (pre-D Income transfer)    = -10
	//   Opening-Balances  =  -10 (Assets opening pair)
	//                       + 10 (Income transfer's opposing leg)   = 0
	want := map[string]string{
		"Assets:A":          "110",
		"Income:A":          "-100",
		"Earnings:Previous": "-10",
		"Opening-Balances":  "0",
	}
	if len(totals) != len(want) {
		t.Fatalf("groups = %d, want %d (got %v)", len(totals), len(want), totals)
	}
	for k, v := range want {
		if totals[k] != v {
			t.Errorf("sum(number) for %s = %s, want %s", k, totals[k], v)
		}
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
