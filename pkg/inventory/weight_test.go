package inventory

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

func amt(n int64, cur string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: cur}
}

func decimalFromString(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return *d
}

func amtStr(t *testing.T, s, cur string) ast.Amount {
	t.Helper()
	return ast.Amount{Number: decimalFromString(t, s), Currency: cur}
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
	w, cur, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
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
	w, cur, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if cur != "USD" {
		t.Errorf("currency = %q, want %q", cur, "USD")
	}
	if got := w.Text('f'); got != "-5031.15" {
		t.Errorf("weight = %q, want %q", got, "-5031.15")
	}
}
