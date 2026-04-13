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
					Cost:    &ast.CostSpec{PerUnit: &costAmt},
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
				Cost:    &ast.CostSpec{PerUnit: &costAmt},
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

// TestPostingWeight_CombinedCost verifies the combined-form CostSpec
// `{per # total CUR}` weight: units*per + sign(units)*total.
func TestPostingWeight_CombinedCost(t *testing.T) {
	units := amt(10, "GOOG")
	perUnit := amtStr(t, "502.12", "USD")
	total := amtStr(t, "9.95", "USD")
	p := &ast.Posting{
		Account: "Assets:Brokerage",
		Amount:  &units,
		Cost:    &ast.CostSpec{PerUnit: &perUnit, Total: &total},
	}
	w, cur, err := postingWeight(p)
	if err != nil {
		t.Fatalf("postingWeight: unexpected error: %v", err)
	}
	if cur != "USD" {
		t.Errorf("currency = %q, want %q", cur, "USD")
	}
	if got := w.Text('f'); got != "5031.15" {
		t.Errorf("weight = %q, want %q", got, "5031.15")
	}
}

// TestPostingWeight_CombinedCostNegativeUnits verifies the combined-form
// CostSpec weight respects the sign of units for the flat total component.
// For -10 units: perPart = -5021.20, totalPart = sign(-10)*9.95 = -9.95,
// sum = -5031.15.
func TestPostingWeight_CombinedCostNegativeUnits(t *testing.T) {
	units := amt(-10, "GOOG")
	perUnit := amtStr(t, "502.12", "USD")
	total := amtStr(t, "9.95", "USD")
	p := &ast.Posting{
		Account: "Assets:Brokerage",
		Amount:  &units,
		Cost:    &ast.CostSpec{PerUnit: &perUnit, Total: &total},
	}
	w, cur, err := postingWeight(p)
	if err != nil {
		t.Fatalf("postingWeight: unexpected error: %v", err)
	}
	if cur != "USD" {
		t.Errorf("currency = %q, want %q", cur, "USD")
	}
	if got := w.Text('f'); got != "-5031.15" {
		t.Errorf("weight = %q, want %q", got, "-5031.15")
	}
}

// TestTxnTolerance_CombinedCost verifies that when the combined-form
// CostSpec has different precisions on PerUnit and Total, the inferred
// cost-based tolerance uses the more precise (more negative) exponent
// — i.e. min(PerUnit.Exp, Total.Exp).
func TestTxnTolerance_CombinedCost(t *testing.T) {
	// PerUnit=1.00 USD (exp -2), Total=0.0001 USD (exp -4), Units=1.
	// Combined form picks min(-2,-4) = -4, so cost contribution is
	// |1| * 0.5*10^-4 = 0.00005. Two sub-cases:
	//   1) cash leg -1 USD (exp 0) → units-based tolerance 0.5
	//      dominates the cost contribution of 0.00005.
	//   2) cash leg -1.000000 USD (exp -6) → cost contribution 0.00005
	//      dominates the units-based tolerance of 0.0000005.
	dirs := openAccounts(t, "2024-01-01", "Assets:Brokerage", "Assets:Cash")
	td := parseDay(t, "2024-02-01")
	units := amt(1, "GOOG")
	perUnit := amtStr(t, "1.00", "USD")
	total := amtStr(t, "0.0001", "USD")
	// Use a low-precision cash leg so the cost contribution is the
	// only thing driving the USD tolerance.
	cash := amt(-1, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{PerUnit: &perUnit, Total: &total},
			},
			{Account: "Assets:Cash", Amount: &cash},
		},
	}
	all := append([]ast.Directive{}, dirs...)
	all = append(all, &ast.Option{Key: "infer_tolerance_from_cost", Value: "TRUE"})
	all = append(all, txn)

	c := newChecker(ledgerOf(all...))
	c.collectOptions()
	tol, err := c.txnTolerance(txn, []string{"USD"})
	if err != nil {
		t.Fatalf("txnTolerance: unexpected error: %v", err)
	}
	got := tol["USD"]
	if got == nil {
		t.Fatalf("txnTolerance: USD tolerance missing")
	}
	// Expected: max(units-based, cost-based)
	//   units-based from cash (exp 0) = 0.5
	//   cost-based  = |1| * 0.5*10^-4 = 0.00005 (using Total's exp -4)
	// Max = 0.5.
	if got.Text('f') != "0.5" {
		t.Errorf("tolerance = %q, want %q", got.Text('f'), "0.5")
	}

	// Now flip the cash leg to high precision so the cost contribution
	// dominates, and verify the more-precise exponent is honoured.
	cash2 := amtStr(t, "-1.000000", "USD") // exp -6, units tol 0.0000005
	txn2 := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{PerUnit: &perUnit, Total: &total},
			},
			{Account: "Assets:Cash", Amount: &cash2},
		},
	}
	all2 := append([]ast.Directive{}, dirs...)
	all2 = append(all2, &ast.Option{Key: "infer_tolerance_from_cost", Value: "TRUE"})
	all2 = append(all2, txn2)
	c2 := newChecker(ledgerOf(all2...))
	c2.collectOptions()
	tol2, err := c2.txnTolerance(txn2, []string{"USD"})
	if err != nil {
		t.Fatalf("txnTolerance: unexpected error: %v", err)
	}
	got2 := tol2["USD"]
	if got2 == nil {
		t.Fatalf("txnTolerance: USD tolerance missing")
	}
	// cost-based (using Total's exp -4) = 0.00005
	// units-based from cash2 (exp -6)   = 0.0000005
	// Max = 0.00005. If the implementation used PerUnit's exp -2 only,
	// it would be 0.005 instead — so this asserts the min-exponent
	// selection.
	if got2.Text('f') != "0.00005" {
		t.Errorf("tolerance = %q, want %q", got2.Text('f'), "0.00005")
	}
}

// TestBalancedCombinedCostEndToEnd verifies a full transaction with the
// combined-form CostSpec validates cleanly with zero residual.
//
//	Assets:Brokerage   10 GOOG {502.12 # 9.95 USD}  ->  5031.15 USD
//	Assets:Cash    -5031.15 USD                      -> -5031.15 USD
func TestBalancedCombinedCostEndToEnd(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Brokerage", "Assets:Cash")
	td := parseDay(t, "2024-01-15")
	units := amt(10, "GOOG")
	perUnit := amtStr(t, "502.12", "USD")
	total := amtStr(t, "9.95", "USD")
	cash := amtStr(t, "-5031.15", "USD")
	txn := &ast.Transaction{
		Date:      td,
		Flag:      '*',
		Narration: "Buy GOOG with commission",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  &units,
				Cost:    &ast.CostSpec{PerUnit: &perUnit, Total: &total},
			},
			{Account: "Assets:Cash", Amount: &cash},
		},
	}
	errs := Check(ledgerOf(append(dirs, txn)...))
	if len(errs) != 0 {
		t.Fatalf("combined-cost txn: got %v, want no errors", errs)
	}
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
