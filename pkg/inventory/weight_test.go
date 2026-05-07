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
	w, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if w.Currency != "USD" {
		t.Errorf("PostingWeight() currency = %q, want %q", w.Currency, "USD")
	}
	if got := w.Number.Text('f'); got != "5031.15" {
		t.Errorf("PostingWeight() weight = %q, want %q", got, "5031.15")
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
	w, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if w.Currency != "USD" {
		t.Errorf("PostingWeight() currency = %q, want %q", w.Currency, "USD")
	}
	if got := w.Number.Text('f'); got != "-5031.15" {
		t.Errorf("PostingWeight() weight = %q, want %q", got, "-5031.15")
	}
}

// TestPostingWeight_TotalCostExact pins the regression that motivated
// unifying the reducer and validation weight paths through TotalCost.
// `3 STOCK {{1 JPY}}` must contribute exactly 1 JPY to the residual,
// without rounding through a 1/3 division. The weight's exponent must
// be 0 (i.e. an integer JPY) so tolerance.Infer does not narrow the
// JPY tolerance to 10⁻³⁴ and reject a balanced auto-posting.
func TestPostingWeight_TotalCostExact(t *testing.T) {
	units := amt(3, "STOCK")
	total := amt(1, "JPY")
	p := &ast.Posting{
		Account: "Assets:A",
		Amount:  &units,
		Cost:    &ast.CostSpec{Total: &total},
	}
	w, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if w.Currency != "JPY" {
		t.Errorf("PostingWeight() currency = %q, want %q", w.Currency, "JPY")
	}
	if got := w.Number.Text('f'); got != "1" {
		t.Errorf("PostingWeight() weight = %q, want %q", got, "1")
	}
	// The exponent is load-bearing for downstream tolerance.Infer:
	// when validation infers per-currency tolerance from posting
	// numbers, a weight stored at Exponent -34 (apd's full precision)
	// would narrow JPY tolerance to ~10⁻³⁴ and reject a balanced
	// auto-posting. Pinning Exponent == 0 here protects that contract.
	if got := w.Number.Exponent; got != 0 {
		t.Errorf("PostingWeight() weight.Exponent = %d, want 0 (exactness regression for tolerance.Infer)", got)
	}
}

// TestPostingWeight_CostAndPriceUsesCost verifies upstream Beancount's
// rule that when both a cost and a per-unit price annotation are
// present on a posting, the cost defines the balancing weight. The
// price feeds the prices database and the realized gain or loss is
// recorded by another posting in the transaction.
func TestPostingWeight_CostAndPriceUsesCost(t *testing.T) {
	units := amt(-10, "IVV")
	cost := amtStr(t, "183.07", "USD")
	price := amtStr(t, "197.90", "USD")
	p := &ast.Posting{
		Account: "Assets:ETrade:IVV",
		Amount:  &units,
		Cost:    &ast.CostSpec{PerUnit: &cost},
		Price:   &ast.PriceAnnotation{Amount: price, IsTotal: false},
	}
	w, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if w.Currency != "USD" {
		t.Errorf("PostingWeight() currency = %q, want %q", w.Currency, "USD")
	}
	if got := w.Number.Text('f'); got != "-1830.70" {
		t.Errorf("PostingWeight() weight = %q, want %q (cost wins over price)", got, "-1830.70")
	}
}

// TestPostingWeight_CostAndTotalPriceUsesCost confirms cost still wins
// when the price is given in total form (`@@`). The price magnitude
// (1979 USD) is irrelevant for balancing; only the cost matters.
func TestPostingWeight_CostAndTotalPriceUsesCost(t *testing.T) {
	units := amt(-10, "IVV")
	cost := amtStr(t, "183.07", "USD")
	totalPrice := amtStr(t, "1979.00", "USD")
	p := &ast.Posting{
		Account: "Assets:ETrade:IVV",
		Amount:  &units,
		Cost:    &ast.CostSpec{PerUnit: &cost},
		Price:   &ast.PriceAnnotation{Amount: totalPrice, IsTotal: true},
	}
	w, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if w.Currency != "USD" {
		t.Errorf("PostingWeight() currency = %q, want %q", w.Currency, "USD")
	}
	if got := w.Number.Text('f'); got != "-1830.70" {
		t.Errorf("PostingWeight() weight = %q, want %q (cost wins over @@ price)", got, "-1830.70")
	}
}

// TestPostingWeight_PriceOnlyStillUsesPrice guards against accidentally
// regressing the price-only path (FX-style conversions) when inverting
// the cost/price precedence. A posting with no Cost annotation but a
// price weight should still be denominated in the price currency.
func TestPostingWeight_PriceOnlyStillUsesPrice(t *testing.T) {
	units := amt(100, "USD")
	price := amtStr(t, "1.1", "EUR")
	p := &ast.Posting{
		Account: "Assets:Bank",
		Amount:  &units,
		Price:   &ast.PriceAnnotation{Amount: price, IsTotal: false},
	}
	w, err := PostingWeight(p)
	if err != nil {
		t.Fatalf("PostingWeight: unexpected error: %v", err)
	}
	if w.Currency != "EUR" {
		t.Errorf("PostingWeight() currency = %q, want %q", w.Currency, "EUR")
	}
	if got := w.Number.Text('f'); got != "110.0" {
		t.Errorf("PostingWeight() weight = %q, want %q", got, "110.0")
	}
}
