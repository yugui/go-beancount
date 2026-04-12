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
