package scope_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/scope"
)

// openingFor returns the synthesized opening for acct under spec, or fails
// if exactly one is not present.
func openingFor(t *testing.T, l *ast.Ledger, spec scope.Spec, acct ast.Account) *ast.Transaction {
	t.Helper()
	openings := findOpenings(t, collectView(l, spec))
	txn, ok := openings[acct]
	if !ok {
		t.Fatalf("no synthesized opening for %s; have %d openings", acct, len(openings))
	}
	return txn
}

// multiYearLedger builds a fixture spanning 2020 and 2021 across the five
// account roots so OPEN ON 2022-01-01 produces a known set of synthesized
// opening-balance transactions.
//
// Layout:
//
//	2020-01-01 open Assets:Cash
//	2020-01-01 open Income:Salary
//	2020-01-01 open Expenses:Food
//	2020-06-01 Income:Salary -1000 USD / Assets:Cash +1000 USD
//	2021-03-01 Expenses:Food   100 USD / Assets:Cash  -100 USD
//	2022-06-15 Assets:Cash    +50 USD / Income:Salary -50 USD  (post-D)
//
// Pre-2022 inventory:
//
//	Assets:Cash   = +900 USD
//	Income:Salary = -1000 USD
//	Expenses:Food = +100 USD
func multiYearLedger(t *testing.T, opts *ast.OptionValues) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{Options: opts}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Income:Salary"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Expenses:Food"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "salary",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "1000", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-1000", "USD")},
			},
		},
		&ast.Transaction{
			Date: date(2021, 3, 1), Flag: '*', Narration: "groceries",
			Postings: []ast.Posting{
				{Account: "Expenses:Food", Amount: amt(t, "100", "USD")},
				{Account: "Assets:Cash", Amount: amt(t, "-100", "USD")},
			},
		},
		&ast.Transaction{
			Date: date(2022, 6, 15), Flag: '*', Narration: "post-boundary",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "50", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-50", "USD")},
			},
		},
	})
	return l
}

func amt(t *testing.T, n, cur string) *ast.Amount {
	t.Helper()
	d, _, err := apd.NewFromString(n)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", n, err)
	}
	return &ast.Amount{Number: *d, Currency: cur}
}

// findOpenings extracts synthesized opening-balance transactions, keyed by
// the source account name they cover. The source account is parsed from the
// transaction's narration ("Opening balance for '<acct>'") because the
// first posting's Account is the source account for asset/liability legs
// but the equity routing account for income/expense legs.
func findOpenings(t *testing.T, ds []ast.Directive) map[ast.Account]*ast.Transaction {
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
		if !ok || v.String != "opening" {
			continue
		}
		if len(txn.Postings) == 0 {
			t.Fatalf("synthesized opening has no postings")
		}
		acct, ok := openingSourceAccount(txn.Narration)
		if !ok {
			t.Fatalf("synthesized opening has malformed narration: %q", txn.Narration)
		}
		out[acct] = txn
	}
	return out
}

func openingSourceAccount(narration string) (ast.Account, bool) {
	const prefix = "Opening balance for '"
	const suffix = "'"
	if !strings.HasPrefix(narration, prefix) || !strings.HasSuffix(narration, suffix) {
		return "", false
	}
	return ast.Account(narration[len(prefix) : len(narration)-len(suffix)]), true
}

func TestOpenSynthesizesOpeningBalances(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	openings := findOpenings(t, got)
	if len(openings) != 3 {
		t.Fatalf("openings = %d, want 3 (Assets:Cash, Income:Salary, Expenses:Food)", len(openings))
	}

	checkAssetLiabilityOpening := func(acct ast.Account, wantUnits, wantRoute string) {
		txn, ok := openings[acct]
		if !ok {
			t.Fatalf("no synthesized opening for %s", acct)
		}
		if !txn.Date.Equal(date(2022, 1, 1)) {
			t.Errorf("%s: date = %v, want 2022-01-01", acct, txn.Date)
		}
		want := "Opening balance for '" + string(acct) + "'"
		if txn.Narration != want {
			t.Errorf("%s: narration = %q, want %q", acct, txn.Narration, want)
		}
		if len(txn.Postings) != 2 {
			t.Fatalf("%s: postings = %d, want 2", acct, len(txn.Postings))
		}
		credit, debit := txn.Postings[0], txn.Postings[1]
		if credit.Account != acct {
			t.Errorf("%s: credit Account = %q, want %q", acct, credit.Account, acct)
		}
		if got := credit.Amount.Number.String(); got != wantUnits {
			t.Errorf("%s: credit units = %s, want %s", acct, got, wantUnits)
		}
		if debit.Account != ast.Account(wantRoute) {
			t.Errorf("%s: route = %s, want %s", acct, debit.Account, wantRoute)
		}
		assertBalanced(t, txn)
	}
	// Income/expense accounts must not be posted by the synthesized opening
	// (their running balance resets across D). The cumulative pre-D balance
	// transfers to account_previous_earnings, paired with
	// account_previous_balances.
	checkIncomeExpenseOpening := func(acct ast.Account, wantTransfer string) {
		txn, ok := openings[acct]
		if !ok {
			t.Fatalf("no synthesized opening for %s", acct)
		}
		if !txn.Date.Equal(date(2022, 1, 1)) {
			t.Errorf("%s: date = %v, want 2022-01-01", acct, txn.Date)
		}
		if len(txn.Postings) != 2 {
			t.Fatalf("%s: postings = %d, want 2", acct, len(txn.Postings))
		}
		earnings, balances := txn.Postings[0], txn.Postings[1]
		if earnings.Account != "Earnings:Previous" {
			t.Errorf("%s: first leg = %q, want Earnings:Previous", acct, earnings.Account)
		}
		if got := earnings.Amount.Number.String(); got != wantTransfer {
			t.Errorf("%s: Earnings:Previous units = %s, want %s", acct, got, wantTransfer)
		}
		if balances.Account != "Opening-Balances" {
			t.Errorf("%s: second leg = %q, want Opening-Balances", acct, balances.Account)
		}
		for _, p := range txn.Postings {
			if p.Account == acct {
				t.Errorf("%s: income/expense account must not appear in its own opening (got posting %+v)", acct, p)
			}
		}
		assertBalanced(t, txn)
	}
	checkAssetLiabilityOpening("Assets:Cash", "900", "Opening-Balances")
	checkIncomeExpenseOpening("Expenses:Food", "100")
	checkIncomeExpenseOpening("Income:Salary", "-1000")
}

func assertBalanced(t *testing.T, txn *ast.Transaction) {
	t.Helper()
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
	for cur, sum := range perCur {
		if sum.Sign() != 0 {
			t.Errorf("transaction not balanced in %s: sum = %s", cur, sum.String())
		}
	}
}

func TestOpenPreservesPreOpenDirectives(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	var opens int
	for _, d := range got {
		if _, ok := d.(*ast.Open); ok {
			opens++
		}
	}
	// All three Open directives are dated 2020-01-01 < 2022-01-01 and so
	// appear exactly once in the preserved prefix; the kept tail (>= D)
	// holds no Opens.
	if opens != 3 {
		t.Fatalf("Open directives = %d, want 3", opens)
	}
}

func TestOpenKeepsPostBoundaryTail(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	var found *ast.Transaction
	for _, d := range got {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if txn.Narration == "post-boundary" {
			found = txn
			break
		}
	}
	if found == nil {
		t.Fatal("post-boundary transaction missing from view")
	}
}

func TestOpenAtBoundaryDateNotDuplicated(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Open{Date: date(2022, 1, 1), Account: "Assets:Brokerage"},
		&ast.Transaction{
			Date: date(2021, 6, 1), Flag: '*', Narration: "txn",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "10", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-10", "USD")},
			},
		},
	})
	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	counts := map[ast.Account]int{}
	for _, d := range got {
		if o, ok := d.(*ast.Open); ok {
			counts[o.Account]++
		}
	}
	if counts["Assets:Cash"] != 1 {
		t.Errorf("Assets:Cash Open count = %d, want 1", counts["Assets:Cash"])
	}
	if counts["Assets:Brokerage"] != 1 {
		t.Errorf("Assets:Brokerage Open count = %d (boundary-date Open must not duplicate)", counts["Assets:Brokerage"])
	}
}

func TestOpenWithCloseBoundsTail(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{
		Open:  date(2021, 1, 1),
		Close: date(2022, 1, 1),
	})

	for _, d := range got {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if txn.Narration == "post-boundary" {
			t.Errorf("post-boundary present after CLOSE")
		}
	}

	openings := findOpenings(t, got)
	cash, ok := openings["Assets:Cash"]
	if !ok {
		t.Fatal("no Assets:Cash opening")
	}
	if got := cash.Postings[0].Amount.Number.String(); got != "1000" {
		t.Errorf("Assets:Cash opening units = %s, want 1000 (only the 2020-06-01 txn precedes 2021-01-01)", got)
	}
}

func TestOpenZeroSpecYieldsNoSyntheticEntries(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{})

	for _, d := range got {
		if txn, ok := d.(*ast.Transaction); ok {
			if _, marked := txn.Meta.Props[scope.SyntheticMetaKey]; marked {
				t.Errorf("synthesized transaction leaked into zero-Spec view: %v", txn)
			}
		}
	}
}

func TestOpenClassifyHonorsCustomNameIncome(t *testing.T) {
	opts := parseOptionsFromKV(t, map[string]string{
		"name_income": "Revenue",
	})

	l := &ast.Ledger{Options: opts}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "renamed-income",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "500", "USD")},
				{Account: "Revenue:Sales", Amount: amt(t, "-500", "USD")},
			},
		},
	})

	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})
	openings := findOpenings(t, got)

	revenue, ok := openings["Revenue:Sales"]
	if !ok {
		t.Fatalf("no opening for Revenue:Sales; have %d openings", len(openings))
	}
	// Custom name_income routes Revenue:Sales' cumulative balance into
	// Earnings:Previous; the income account itself is not posted.
	if route := revenue.Postings[0].Account; route != "Earnings:Previous" {
		t.Errorf("Revenue:Sales transfer leg = %q, want Earnings:Previous (custom name_income)", route)
	}
	if pair := revenue.Postings[1].Account; pair != "Opening-Balances" {
		t.Errorf("Revenue:Sales balancing leg = %q, want Opening-Balances", pair)
	}
}

func TestOpenSynthesisIsLexicographic(t *testing.T) {
	l := multiYearLedger(t, nil)
	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	var order []ast.Account
	for _, d := range got {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if v, ok := txn.Meta.Props[scope.SyntheticMetaKey]; ok && v.String == "opening" {
			acct, ok := openingSourceAccount(txn.Narration)
			if !ok {
				t.Fatalf("malformed opening narration: %q", txn.Narration)
			}
			order = append(order, acct)
		}
	}
	sorted := slices.Clone(order)
	slices.Sort(sorted)
	if !slices.Equal(order, sorted) {
		t.Fatalf("opening order = %v, want sorted %v", order, sorted)
	}
}

// TestOpenKeepsZeroDateDirectives pins that zero-date header directives
// (Option, Plugin, Include) pass through the kept tail under OPEN ON,
// analogous to TestViewCloseKeepsZeroDateDirectives for CLOSE.
func TestOpenKeepsZeroDateDirectives(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Option{Key: "title", Value: "test"},
		&ast.Transaction{
			Date: date(2022, 6, 15), Flag: '*', Narration: "post-D",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "1", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-1", "USD")},
			},
		},
	})

	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	var sawOption, sawTxn bool
	for _, d := range got {
		switch v := d.(type) {
		case *ast.Option:
			sawOption = true
		case *ast.Transaction:
			if v.Narration == "post-D" {
				sawTxn = true
			}
		}
	}
	if !sawOption {
		t.Errorf("zero-date Option dropped under OPEN ON")
	}
	if !sawTxn {
		t.Errorf("post-D transaction missing from view")
	}
}

// TestOpenPreservesCostLots verifies that a posting carrying a booked Cost
// lot has that lot preserved on the synthesized opening's credit posting,
// while the routing leg has no Cost.
func TestOpenPreservesCostLots(t *testing.T) {
	cost := &ast.Cost{
		Number:   dec(t, "100"),
		Currency: "USD",
		Date:     date(2020, 6, 1),
	}
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Brokerage"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "buy",
			Postings: []ast.Posting{
				{Account: "Assets:Brokerage", Amount: amt(t, "10", "STOCK"), Cost: cost},
				{Account: "Assets:Cash", Amount: amt(t, "-1000", "USD")},
			},
		},
	})

	txn := openingFor(t, l, scope.Spec{Open: date(2022, 1, 1)}, "Assets:Brokerage")
	if len(txn.Postings) != 2 {
		t.Fatalf("postings = %d, want 2", len(txn.Postings))
	}
	credit, debit := txn.Postings[0], txn.Postings[1]
	gotCost, ok := credit.Cost.(*ast.Cost)
	if !ok || gotCost == nil {
		t.Fatalf("credit Cost = %T %v, want non-nil *ast.Cost", credit.Cost, credit.Cost)
	}
	if !gotCost.Equal(cost) {
		t.Errorf("credit Cost = %+v, want %+v", gotCost, cost)
	}
	if debit.Cost != nil {
		t.Errorf("routing leg Cost = %v, want nil", debit.Cost)
	}
}

// TestOpenMultiCurrencyPerPositionPairs verifies the per-(account, position)
// synthesis: an account accumulating two currencies yields two posting pairs,
// each balancing per-currency.
func TestOpenMultiCurrencyPerPositionPairs(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "usd",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "100", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-100", "USD")},
			},
		},
		&ast.Transaction{
			Date: date(2020, 7, 1), Flag: '*', Narration: "eur",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "50", "EUR")},
				{Account: "Income:Salary", Amount: amt(t, "-50", "EUR")},
			},
		},
	})

	txn := openingFor(t, l, scope.Spec{Open: date(2022, 1, 1)}, "Assets:Cash")
	if len(txn.Postings) != 4 {
		t.Fatalf("postings = %d, want 4 (two pairs)", len(txn.Postings))
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
			t.Errorf("opening for Assets:Cash in %s: sum = %s, want 0", cur, sum.String())
		}
	}
}

// TestOpenNetZeroAccountOmitted verifies that an account whose pre-D postings
// sum to zero produces no synthesized opening.
func TestOpenNetZeroAccountOmitted(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "in",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "100", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-100", "USD")},
			},
		},
		&ast.Transaction{
			Date: date(2020, 7, 1), Flag: '*', Narration: "out",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "-100", "USD")},
				{Account: "Expenses:Food", Amount: amt(t, "100", "USD")},
			},
		},
	})

	openings := findOpenings(t, collectView(l, scope.Spec{Open: date(2022, 1, 1)}))
	if _, ok := openings["Assets:Cash"]; ok {
		t.Errorf("net-zero Assets:Cash produced a synthesized opening")
	}
	if _, ok := openings["Income:Salary"]; !ok {
		t.Errorf("Income:Salary opening missing")
	}
	if _, ok := openings["Expenses:Food"]; !ok {
		t.Errorf("Expenses:Food opening missing")
	}
}

// TestOpenLiabilityRoutesToOpeningBalances exercises the non-income/expense
// branch of the classifier for a Liabilities-rooted account.
func TestOpenLiabilityRoutesToOpeningBalances(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Liabilities:CreditCard"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "charge",
			Postings: []ast.Posting{
				{Account: "Liabilities:CreditCard", Amount: amt(t, "-200", "USD")},
				{Account: "Expenses:Food", Amount: amt(t, "200", "USD")},
			},
		},
	})

	txn := openingFor(t, l, scope.Spec{Open: date(2022, 1, 1)}, "Liabilities:CreditCard")
	if route := txn.Postings[1].Account; route != "Opening-Balances" {
		t.Errorf("Liabilities:CreditCard routed to %q, want Opening-Balances", route)
	}
}

// TestOpenBoundaryDayTransactionKept verifies that a transaction dated
// exactly D is preserved in the kept tail (not folded into the opening
// inventory), per the strict-< prefix predicate.
func TestOpenBoundaryDayTransactionKept(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2020, 6, 1), Flag: '*', Narration: "pre",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "100", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-100", "USD")},
			},
		},
		&ast.Transaction{
			Date: date(2022, 1, 1), Flag: '*', Narration: "boundary",
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: amt(t, "7", "USD")},
				{Account: "Income:Salary", Amount: amt(t, "-7", "USD")},
			},
		},
	})

	got := collectView(l, scope.Spec{Open: date(2022, 1, 1)})

	var boundary *ast.Transaction
	for _, d := range got {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if txn.Narration == "boundary" {
			boundary = txn
			break
		}
	}
	if boundary == nil {
		t.Fatal("boundary-day transaction missing")
	}
	if len(boundary.Postings) != 2 {
		t.Errorf("boundary postings = %d, want 2 (unmodified)", len(boundary.Postings))
	}

	txn := openingFor(t, l, scope.Spec{Open: date(2022, 1, 1)}, "Assets:Cash")
	if got := txn.Postings[0].Amount.Number.String(); got != "100" {
		t.Errorf("Assets:Cash opening units = %s, want 100 (boundary-day txn folded in)", got)
	}
}

// TestOpenViewEarlyBreak mirrors TestViewEarlyBreak for OPEN: breaking out
// of an iterator and re-iterating the same iterator must replay from the
// first element.
func TestOpenViewEarlyBreak(t *testing.T) {
	l := multiYearLedger(t, nil)
	s := scope.Spec{Open: date(2022, 1, 1)}

	seq := scope.View(l, s)

	var first ast.Directive
	for _, d := range seq {
		first = d
		break
	}
	if first == nil {
		t.Fatal("expected at least one directive")
	}

	var second []ast.Directive
	for _, d := range seq {
		second = append(second, d)
	}
	if len(second) == 0 {
		t.Fatal("expected non-empty replay")
	}
	if second[0] != first {
		t.Errorf("replay[0] = %T, want same as first = %T", second[0], first)
	}
}

// TestOpenReplayable confirms that two iterations of the same returned
// iterator yield identical (pointer-equal) sequences; this is what the
// re-Run path relies on.
func TestOpenReplayable(t *testing.T) {
	l := multiYearLedger(t, nil)
	s := scope.Spec{Open: date(2022, 1, 1)}

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

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

// parseOptionsFromKV builds an *OptionValues from a key/value map by
// applying Option directives through ParseOptions (the public path).
func parseOptionsFromKV(t *testing.T, kv map[string]string) *ast.OptionValues {
	t.Helper()
	l := &ast.Ledger{}
	for k, v := range kv {
		l.Insert(&ast.Option{Key: k, Value: v})
	}
	opts, diags := ast.ParseOptions(l)
	for _, d := range diags {
		if d.Severity == ast.Error {
			t.Fatalf("ParseOptions: %s", d.Message)
		}
	}
	return opts
}
