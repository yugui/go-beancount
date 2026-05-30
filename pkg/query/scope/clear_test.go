package scope_test

import (
	"slices"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/scope"
)

// findClearings extracts synthesized clearing transactions, keyed by the
// account being cleared (the first posting's Account).
func findClearings(t *testing.T, ds []ast.Directive) map[ast.Account]*ast.Transaction {
	t.Helper()
	out := map[ast.Account]*ast.Transaction{}
	for _, d := range ds {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if txn.Meta.Props == nil {
			continue
		}
		v, ok := txn.Meta.Props[scope.SyntheticMetaKey]
		if !ok || v.String != "clearing" {
			continue
		}
		if len(txn.Postings) == 0 {
			t.Fatalf("synthesized clearing has no postings")
		}
		out[txn.Postings[0].Account] = txn
	}
	return out
}

// TestClearSynthesizesIncomeExpenseClearings exercises the simplest case:
// CLEAR alone over a ledger with mixed account roots. Income and Expenses
// accounts get clearing transactions; Assets does not.
func TestClearSynthesizesIncomeExpenseClearings(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{Clear: true})

	clearings := findClearings(t, got)
	if len(clearings) != 2 {
		t.Fatalf("clearings = %d, want 2 (Income:Salary, Expenses:Food); got accounts: %v",
			len(clearings), keys(clearings))
	}
	if _, ok := clearings["Assets:Cash"]; ok {
		t.Errorf("Assets:Cash was cleared; only income/expense should be cleared")
	}

	// Expenses:Food net = +100 USD; clearing leg should be -100 to acct,
	// routing +100 to Earnings:Current.
	food, ok := clearings["Expenses:Food"]
	if !ok {
		t.Fatal("Expenses:Food clearing missing")
	}
	if len(food.Postings) != 2 {
		t.Fatalf("Expenses:Food postings = %d, want 2", len(food.Postings))
	}
	if got := food.Postings[0].Amount.Number.String(); got != "-100" {
		t.Errorf("Expenses:Food acct leg = %s, want -100", got)
	}
	if food.Postings[1].Account != "Earnings:Current" {
		t.Errorf("Expenses:Food routing = %q, want Earnings:Current", food.Postings[1].Account)
	}
	if got := food.Postings[1].Amount.Number.String(); got != "100" {
		t.Errorf("Expenses:Food routing leg = %s, want 100", got)
	}

	// Narration carries the account name.
	want := "Clear balance for 'Expenses:Food'"
	if food.Narration != want {
		t.Errorf("Expenses:Food narration = %q, want %q", food.Narration, want)
	}

	// Income:Salary net = -1050 USD (multiYearLedger includes a post-2022 txn).
	salary, ok := clearings["Income:Salary"]
	if !ok {
		t.Fatal("Income:Salary clearing missing")
	}
	if got := salary.Postings[0].Amount.Number.String(); got != "1050" {
		t.Errorf("Income:Salary acct leg = %s, want 1050 (negation of -1050)", got)
	}
}

// TestClearBalancesPerCurrency verifies posting pairs balance per currency.
func TestClearBalancesPerCurrency(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Income:Mixed"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "usd",
			Postings: []ast.Posting{
				{Account: "Income:Mixed", Amount: amt(t, "-100", "USD")},
				{Account: "Assets:Cash", Amount: amt(t, "100", "USD")},
			},
		},
		&ast.Transaction{
			Date: date(2020, 7, 1), Flag: '*', Narration: "eur",
			Postings: []ast.Posting{
				{Account: "Income:Mixed", Amount: amt(t, "-50", "EUR")},
				{Account: "Assets:Cash", Amount: amt(t, "50", "EUR")},
			},
		},
	})

	clearings := findClearings(t, collectView(l, scope.Spec{Clear: true}))
	txn, ok := clearings["Income:Mixed"]
	if !ok {
		t.Fatal("Income:Mixed clearing missing")
	}
	if len(txn.Postings) != 4 {
		t.Fatalf("postings = %d, want 4 (two currency pairs)", len(txn.Postings))
	}
	perCur := map[string]*apd.Decimal{}
	for _, p := range txn.Postings {
		cur := p.Amount.Currency
		sum, ok := perCur[cur]
		if !ok {
			sum = new(apd.Decimal)
			perCur[cur] = sum
		}
		if _, err := apd.BaseContext.Add(sum, sum, &p.Amount.Number); err != nil {
			t.Fatalf("apd Add: %v", err)
		}
	}
	if len(perCur) != 2 {
		t.Fatalf("currencies = %d, want 2 (USD, EUR)", len(perCur))
	}
	for cur, sum := range perCur {
		if sum.Sign() != 0 {
			t.Errorf("clearing for Income:Mixed in %s: sum = %s, want 0", cur, sum.String())
		}
	}
}

// TestClearWithCloseUsesCloseMinusOneDay verifies CLOSE + CLEAR: boundary =
// Close − 1 day, and only directives strictly before Close contribute to the
// walk.
func TestClearWithCloseUsesCloseMinusOneDay(t *testing.T) {
	l := multiYearLedger(t, nil)
	close := date(2022, 1, 1)
	got := collectView(l, scope.Spec{Close: close, Clear: true})

	clearings := findClearings(t, got)
	salary, ok := clearings["Income:Salary"]
	if !ok {
		t.Fatal("Income:Salary clearing missing")
	}
	if !salary.Date.Equal(date(2021, 12, 31)) {
		t.Errorf("clearing date = %v, want 2021-12-31 (Close − 1 day)", salary.Date)
	}
	// Income:Salary's pre-2022 balance is -1000 USD (the 2022-06-15 txn is
	// dropped by CLOSE).
	if got := salary.Postings[0].Amount.Number.String(); got != "1000" {
		t.Errorf("Income:Salary acct leg = %s, want 1000 (only pre-CLOSE txns counted)", got)
	}
}

// TestClearWithOpenUsesLastEntryDate verifies CLEAR + OPEN: boundary derives
// from the last directive in the kept-tail stream (synthesized opening txns
// are included in the walk, but their non-income/expense routing legs are
// ignored).
func TestClearWithOpenUsesLastEntryDate(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{
		Open:  date(2022, 1, 1),
		Clear: true,
	})

	clearings := findClearings(t, got)
	// Earnings:Previous is rooted in Equity (account_previous_earnings's
	// default is Earnings:Previous), so it must NOT be cleared.
	if _, ok := clearings["Earnings:Previous"]; ok {
		t.Errorf("Earnings:Previous was cleared; equity must not be cleared")
	}

	// Both income and expense get clearings: their pre-D balance comes via
	// synthesized openings, and the post-D 2022-06-15 txn adds to Income:Salary.
	if _, ok := clearings["Income:Salary"]; !ok {
		t.Errorf("Income:Salary clearing missing")
	}
	if _, ok := clearings["Expenses:Food"]; !ok {
		t.Errorf("Expenses:Food clearing missing")
	}

	// Boundary date = last entry's DirDate, which is the 2022-06-15 post-D txn.
	for _, txn := range clearings {
		if !txn.Date.Equal(date(2022, 6, 15)) {
			t.Errorf("clearing date = %v, want 2022-06-15 (last entry date)", txn.Date)
		}
	}
}

// TestClearWithOpenAndCloseCombination exercises the three-way combination.
// Boundary = Close − 1; income/expense balances over the OPEN-summarized,
// CLOSE-bounded stream get cleared.
func TestClearWithOpenAndCloseCombination(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{
		Open:  date(2021, 1, 1),
		Close: date(2022, 1, 1),
		Clear: true,
	})

	clearings := findClearings(t, got)
	for _, txn := range clearings {
		if !txn.Date.Equal(date(2021, 12, 31)) {
			t.Errorf("clearing date = %v, want 2021-12-31 (Close − 1)", txn.Date)
		}
	}
	// Income:Salary pre-OPEN balance is -1000 USD (one 2020-06-01 txn);
	// kept-window has no income/expense activity between [2021-01-01, 2022-01-01)
	// other than the 2021-03-01 groceries posting on Expenses:Food (+100 USD).
	// Earnings:Previous (the OPEN routing equity) is not income/expense and is
	// not cleared.
	salary, ok := clearings["Income:Salary"]
	if !ok {
		t.Fatal("Income:Salary clearing missing")
	}
	if got := salary.Postings[0].Amount.Number.String(); got != "1000" {
		t.Errorf("Income:Salary acct leg = %s, want 1000", got)
	}
}

// TestClearEmptyLedger verifies that CLEAR over an empty ledger emits no
// clearings (no income/expense balances exist) and does not panic on the
// boundary fallback to time.Now().
func TestClearEmptyLedger(t *testing.T) {
	l := &ast.Ledger{}
	got := collectView(l, scope.Spec{Clear: true})

	if len(got) != 0 {
		t.Fatalf("got %d directives, want 0", len(got))
	}
}

// TestClearCustomNameIncome verifies the income classifier respects a custom
// OptionValues.name_income.
func TestClearCustomNameIncome(t *testing.T) {
	opts := parseOptionsFromKV(t, map[string]string{"name_income": "Revenue"})
	l := &ast.Ledger{Options: opts}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "renamed",
			Postings: []ast.Posting{
				{Account: "Revenue:Sales", Amount: amt(t, "-500", "USD")},
				{Account: "Assets:Cash", Amount: amt(t, "500", "USD")},
			},
		},
	})

	clearings := findClearings(t, collectView(l, scope.Spec{Clear: true}))
	if _, ok := clearings["Revenue:Sales"]; !ok {
		t.Errorf("Revenue:Sales not cleared (custom name_income should pick it up)")
	}
	if _, ok := clearings["Assets:Cash"]; ok {
		t.Errorf("Assets:Cash cleared; only income/expense should be cleared")
	}
}

// TestClearCustomAccountCurrentEarnings verifies the routing account follows
// account_current_earnings.
func TestClearCustomAccountCurrentEarnings(t *testing.T) {
	opts := parseOptionsFromKV(t, map[string]string{
		"account_current_earnings": "Equity:Pnl",
	})
	l := &ast.Ledger{Options: opts}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "income",
			Postings: []ast.Posting{
				{Account: "Income:Salary", Amount: amt(t, "-100", "USD")},
				{Account: "Assets:Cash", Amount: amt(t, "100", "USD")},
			},
		},
	})

	clearings := findClearings(t, collectView(l, scope.Spec{Clear: true}))
	salary, ok := clearings["Income:Salary"]
	if !ok {
		t.Fatal("Income:Salary clearing missing")
	}
	if route := salary.Postings[1].Account; route != "Equity:Pnl" {
		t.Errorf("routing = %q, want Equity:Pnl (custom account_current_earnings)", route)
	}
}

// TestClearAppendsToTail verifies clearings come after the kept stream's
// original directives, sorted lexicographically by account.
func TestClearAppendsToTail(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{Clear: true})

	var clearingsStart int
	for i, d := range got {
		if txn, ok := d.(*ast.Transaction); ok {
			if v, marked := txn.Meta.Props[scope.SyntheticMetaKey]; marked && v.String == "clearing" {
				clearingsStart = i
				break
			}
		}
	}
	// Everything after the first clearing should be a clearing.
	var accounts []ast.Account
	for _, d := range got[clearingsStart:] {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			t.Fatalf("non-transaction after clearings: %T", d)
		}
		v, marked := txn.Meta.Props[scope.SyntheticMetaKey]
		if !marked || v.String != "clearing" {
			t.Fatalf("non-clearing after clearings: %+v", txn)
		}
		accounts = append(accounts, txn.Postings[0].Account)
	}
	sorted := slices.Clone(accounts)
	slices.Sort(sorted)
	if !slices.Equal(accounts, sorted) {
		t.Errorf("clearing order = %v, want sorted %v", accounts, sorted)
	}
}

// TestClearReplayable confirms that two iterations of the same iterator
// yield identical sequences; needed for re-Run.
func TestClearReplayable(t *testing.T) {
	l := multiYearLedger(t, nil)
	s := scope.Spec{Clear: true}

	seq := scope.View(l, s)

	var a, b []ast.Directive
	for _, d := range seq {
		a = append(a, d)
	}
	for _, d := range seq {
		b = append(b, d)
	}
	if len(a) == 0 {
		t.Fatal("empty first iteration")
	}
	if len(a) != len(b) {
		t.Fatalf("first len %d, second len %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("index %d differs: %T vs %T", i, a[i], b[i])
		}
	}
}

// TestClearBoundaryFallsBackToToday exercises the no-CLOSE,
// no-dated-directives boundary path: a header-only ledger produces no
// clearings but the boundary code path must not panic on the
// time.Now().UTC() fallback. The clearing-date assertion is on the
// OPEN-only synthesized-tail path elsewhere; here we only pin the no-panic
// + no-spurious-clearing contract.
func TestClearBoundaryFallsBackToToday(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Option{Key: "title", Value: "test"},
	})

	for _, d := range collectView(l, scope.Spec{Clear: true}) {
		if txn, ok := d.(*ast.Transaction); ok {
			if v, marked := txn.Meta.Props[scope.SyntheticMetaKey]; marked && v.String == "clearing" {
				t.Errorf("unexpected clearing: %+v", txn)
			}
		}
	}
}

func keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
