package validation

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// openAccounts returns a slice of Open directives for the given accounts on
// the given date with no currency restriction.
func openAccounts(t *testing.T, date string, names ...string) []ast.Directive {
	t.Helper()
	d := parseDay(t, date)
	out := make([]ast.Directive, 0, len(names))
	for _, n := range names {
		out = append(out, &ast.Open{Date: d, Account: n})
	}
	return out
}

func TestBalancedTwoPostings(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	a := amt(100, "USD")
	na := amt(-100, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Expenses:Food", Amount: &na},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	if len(errs) != 0 {
		t.Fatalf("balanced txn: got %v, want no errors", errs)
	}
}

func TestSingleAutoPosting(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	a := amt(100, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Expenses:Food"}, // auto-posting
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	if len(errs) != 0 {
		t.Fatalf("single auto-posting: got %v, want no errors", errs)
	}
}

func TestMultipleAutoPostings(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food", "Expenses:Misc")
	td := parseDay(t, "2024-02-01")
	a := amt(100, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Expenses:Food"},
			{Account: "Expenses:Misc"},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	wantCodes(t, errs, CodeMultipleAutoPostings)
}

func TestUnbalancedTransaction(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	a := amt(100, "USD")
	na := amt(-90, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &a},
			{Account: "Expenses:Food", Amount: &na},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	wantCodes(t, errs, CodeUnbalancedTransaction)
}

func TestBalancedWithPriceAnnotation(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Stocks", "Assets:Cash")
	td := parseDay(t, "2024-02-01")
	units := amt(10, "STOCK")
	price := amt(100, "USD")
	cash := amt(-1000, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Stocks",
				Amount:  &units,
				Price:   &ast.PriceAnnotation{Amount: price, IsTotal: false},
			},
			{Account: "Assets:Cash", Amount: &cash},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	if len(errs) != 0 {
		t.Fatalf("priced txn: got %v, want no errors", errs)
	}
}

func TestBalancedWithTotalPriceAnnotation(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Stocks", "Assets:Cash")
	td := parseDay(t, "2024-02-01")
	units := amt(10, "STOCK")
	total := amt(1000, "USD")
	cash := amt(-1000, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Stocks",
				Amount:  &units,
				Price:   &ast.PriceAnnotation{Amount: total, IsTotal: true},
			},
			{Account: "Assets:Cash", Amount: &cash},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	if len(errs) != 0 {
		t.Fatalf("total-priced txn: got %v, want no errors", errs)
	}
}

func TestUnbalancedMixedCurrencyPerCurrencyTolerance(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:A", "Assets:B")
	td := parseDay(t, "2024-02-01")
	jpyPos := amt(10, "JPY")
	jpyNeg := amt(-10, "JPY")
	usdPos := amtStr(t, "100.00", "USD")
	usdNeg := amtStr(t, "-99.60", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:A", Amount: &jpyPos},
			{Account: "Assets:B", Amount: &jpyNeg},
			{Account: "Assets:A", Amount: &usdPos},
			{Account: "Assets:B", Amount: &usdNeg},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	wantCodes(t, errs, CodeUnbalancedTransaction)
}

func TestBalancedMixedCurrencies(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:A", "Assets:B")
	td := parseDay(t, "2024-02-01")
	jpyPos := amt(10, "JPY")
	jpyNeg := amt(-10, "JPY")
	usdPos := amtStr(t, "100.00", "USD")
	usdNeg := amtStr(t, "-100.00", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:A", Amount: &jpyPos},
			{Account: "Assets:B", Amount: &jpyNeg},
			{Account: "Assets:A", Amount: &usdPos},
			{Account: "Assets:B", Amount: &usdNeg},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	if len(errs) != 0 {
		t.Fatalf("balanced mixed-currency txn: got %v, want no errors", errs)
	}
}

func TestTxnMultiplierAffectsResidualTolerance(t *testing.T) {
	// Residual is exactly 0.01 USD: 100.00 + (-99.99) = 0.01.
	// Default multiplier 0.5 → tolerance 0.005 → fail.
	// Multiplier 1 → tolerance 0.01 → pass.
	build := func(withOption bool) *ast.Ledger {
		dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
		pos := amtStr(t, "100.00", "USD")
		neg := amtStr(t, "-99.99", "USD")
		txn := &ast.Transaction{
			Date: parseDay(t, "2024-02-01"),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &pos},
				{Account: "Expenses:Food", Amount: &neg},
			},
		}
		all := append([]ast.Directive{}, dirs...)
		if withOption {
			all = append(all, &ast.Option{Key: "inferred_tolerance_multiplier", Value: "1"})
		}
		all = append(all, txn)
		return ledgerOf(all...)
	}

	errs := Check(build(false))
	wantCodes(t, errs, CodeUnbalancedTransaction)

	errs = Check(build(true))
	if len(errs) != 0 {
		t.Fatalf("TestTxnMultiplierAffectsResidualTolerance: multiplier=1: got %v, want no errors", errs)
	}
}

// TestInferToleranceFromCost exercises the `infer_tolerance_from_cost`
// option with a costed transaction whose residual is within the
// cost-derived tolerance but not within the units-derived tolerance.
//
// Postings:
//
//	Assets:Inv     1000 XYZ {1.0001 USD}   -> weight =  1000.1000 USD
//	Assets:Cash   -1000.15 USD             -> weight = -1000.1500 USD
//
// Residual USD = -0.05. Units-based USD tolerance comes from the cash
// posting (exp -2) -> 0.005. Cost-based contribution is
// |1000| * 0.00005 = 0.05.
//
//   - Disabled (default): cost contribution ignored, |-0.05| > 0.005 -> unbalanced.
//   - Enabled: max(0.005, 0.05) = 0.05, |-0.05| == 0.05 -> balanced.
func TestInferToleranceFromCost(t *testing.T) {
	build := func(withOption bool) *ast.Ledger {
		dirs := openAccounts(t, "2024-01-01", "Assets:Inv", "Assets:Cash")
		td := parseDay(t, "2024-02-01")
		units := amt(1000, "XYZ")
		costAmt := amtStr(t, "1.0001", "USD")
		cash := amtStr(t, "-1000.15", "USD")
		txn := &ast.Transaction{
			Date: td,
			Flag: '*',
			Postings: []ast.Posting{
				{
					Account: "Assets:Inv",
					Amount:  &units,
					Cost:    &ast.CostSpec{Amount: &costAmt, IsTotal: false},
				},
				{Account: "Assets:Cash", Amount: &cash},
			},
		}
		all := append([]ast.Directive{}, dirs...)
		if withOption {
			all = append(all, &ast.Option{Key: "infer_tolerance_from_cost", Value: "TRUE"})
		}
		all = append(all, txn)
		return ledgerOf(all...)
	}

	tests := []struct {
		name      string
		withOpt   bool
		wantCodes []Code
	}{
		{name: "disabled", withOpt: false, wantCodes: []Code{CodeUnbalancedTransaction}},
		{name: "enabled", withOpt: true, wantCodes: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := Check(build(tc.withOpt))
			wantCodes(t, errs, tc.wantCodes...)
		})
	}
}

// TestInferToleranceFromCostOnlyCostCurrency verifies the cost-based
// contribution applies only to the cost currency, not the units currency.
// Here USD balances exactly but XYZ has a 1-unit residual that must remain
// unbalanced regardless of the option being on.
func TestInferToleranceFromCostOnlyCostCurrency(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Inv", "Assets:Inv2", "Assets:Cash")
	td := parseDay(t, "2024-02-01")
	invUnits := amt(1000, "XYZ")
	invNeg := amt(-999, "XYZ")
	costAmt := amtStr(t, "1.0001", "USD")
	cash := amtStr(t, "-1000.10", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Inv",
				Amount:  &invUnits,
				Cost:    &ast.CostSpec{Amount: &costAmt, IsTotal: false},
			},
			{Account: "Assets:Inv2", Amount: &invNeg},
			{Account: "Assets:Cash", Amount: &cash},
		},
	}
	all := append([]ast.Directive{}, dirs...)
	all = append(all, &ast.Option{Key: "infer_tolerance_from_cost", Value: "TRUE"})
	all = append(all, txn)
	errs := Check(ledgerOf(all...))
	wantCodes(t, errs, CodeUnbalancedTransaction)
}

func TestUnbalancedMultiCurrencyAutoPosting(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Assets:EurCash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	usd := amt(100, "USD")
	eur := amt(50, "EUR")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &usd},
			{Account: "Assets:EurCash", Amount: &eur},
			{Account: "Expenses:Food"}, // auto cannot resolve 2 currencies
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	wantCodes(t, errs, CodeUnbalancedTransaction)
}
