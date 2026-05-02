package tolerance_test

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation/internal/tolerance"
)

func mustDefaults(t *testing.T) *options.Values {
	t.Helper()
	v, errs := options.Parse(nil)
	if len(errs) != 0 {
		t.Fatalf("options.Parse(nil): unexpected errors: %v", errs)
	}
	return v
}

func mustOpts(t *testing.T, raw map[string]string) *options.Values {
	t.Helper()
	v, errs := options.FromRaw(raw)
	if len(errs) != 0 {
		t.Fatalf("options.FromRaw(%v): unexpected errors: %v", raw, errs)
	}
	return v
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

// TestInfer_EmptyPostings verifies that Infer returns an empty tolerance
// map when given no postings and no residual currencies.
func TestInfer_EmptyPostings(t *testing.T) {
	tol, err := tolerance.Infer(nil, mustDefaults(t), nil)
	if err != nil {
		t.Fatalf("Infer(nil, defaults, nil): unexpected error: %v", err)
	}
	if len(tol) != 0 {
		t.Errorf("Infer: got %d entries, want 0", len(tol))
	}
}

// TestInfer_SingleCurrencyNoCost verifies units-based tolerance using the
// default multiplier 0.5 for postings without a cost spec. The result must
// be multiplier * 10^e where e is the most negative exponent in that
// currency.
func TestInfer_SingleCurrencyNoCost(t *testing.T) {
	pos := amtStr(t, "100.00", "USD")   // exp -2 -> tolerance 0.005
	neg := amtStr(t, "-100.000", "USD") // exp -3 -> tolerance 0.0005
	postings := []ast.Posting{
		{Account: "Assets:Cash", Amount: &pos},
		{Account: "Expenses:Food", Amount: &neg},
	}
	tol, err := tolerance.Infer(postings, mustDefaults(t), []string{"USD"})
	if err != nil {
		t.Fatalf("Infer: unexpected error: %v", err)
	}
	got := tol["USD"]
	if got == nil {
		t.Fatalf("Infer: USD tolerance missing")
	}
	if got.Text('f') != "0.0005" {
		t.Errorf("tolerance = %q, want %q", got.Text('f'), "0.0005")
	}
}

// TestInfer_UnknownResidualCurrencyIsZero asserts that residual currencies
// with no contributing posting (e.g. arising from price conversion) get a
// zero tolerance.
func TestInfer_UnknownResidualCurrencyIsZero(t *testing.T) {
	pos := amtStr(t, "100.00", "USD")
	postings := []ast.Posting{{Account: "Assets:Cash", Amount: &pos}}
	tol, err := tolerance.Infer(postings, mustDefaults(t), []string{"EUR"})
	if err != nil {
		t.Fatalf("Infer: unexpected error: %v", err)
	}
	got := tol["EUR"]
	if got == nil {
		t.Fatalf("Infer: EUR tolerance missing")
	}
	if !got.IsZero() {
		t.Errorf("tolerance = %q, want 0", got.Text('f'))
	}
}

// TestInfer_CostBased exercises infer_tolerance_from_cost with a posting
// that carries a CostSpec, verifying the cost contribution |units| *
// (multiplier * 10^costExp) is combined via max with the units-based
// tolerance.
func TestInfer_CostBased(t *testing.T) {
	// 1 GOOG {1.00 USD} (costExp = -2) balanced by -1 USD (exp 0).
	// Units-based USD tolerance from cash leg = 0.5 * 10^0 = 0.5.
	// Cost-based = |1| * 0.5 * 10^-2 = 0.005.
	// Max = 0.5.
	units := amtStr(t, "1", "GOOG")
	perUnit := amtStr(t, "1.00", "USD")
	cash := amtStr(t, "-1", "USD")
	postings := []ast.Posting{
		{
			Account: "Assets:Brokerage",
			Amount:  &units,
			Cost:    &ast.CostSpec{PerUnit: &perUnit},
		},
		{Account: "Assets:Cash", Amount: &cash},
	}
	opts := mustOpts(t, map[string]string{"infer_tolerance_from_cost": "TRUE"})
	tol, err := tolerance.Infer(postings, opts, []string{"USD"})
	if err != nil {
		t.Fatalf("Infer: unexpected error: %v", err)
	}
	got := tol["USD"]
	if got == nil {
		t.Fatalf("Infer: USD tolerance missing")
	}
	if got.Text('f') != "0.5" {
		t.Errorf("tolerance = %q, want %q", got.Text('f'), "0.5")
	}
}

// TestInfer_CostDominatesWhenCashIsPrecise flips the previous case so the
// cost-based contribution dominates: a high-precision cash leg gives a
// small units-based tolerance, and the cost contribution is larger.
func TestInfer_CostDominatesWhenCashIsPrecise(t *testing.T) {
	// 1 GOOG {1.00 USD} (costExp -2), cash -1.000000 USD (exp -6).
	// Units-based = 0.5 * 10^-6 = 0.0000005.
	// Cost-based  = |1| * 0.5 * 10^-2 = 0.005.
	// Max = 0.005.
	units := amtStr(t, "1", "GOOG")
	perUnit := amtStr(t, "1.00", "USD")
	cash := amtStr(t, "-1.000000", "USD")
	postings := []ast.Posting{
		{
			Account: "Assets:Brokerage",
			Amount:  &units,
			Cost:    &ast.CostSpec{PerUnit: &perUnit},
		},
		{Account: "Assets:Cash", Amount: &cash},
	}
	opts := mustOpts(t, map[string]string{"infer_tolerance_from_cost": "TRUE"})
	tol, err := tolerance.Infer(postings, opts, []string{"USD"})
	if err != nil {
		t.Fatalf("Infer: unexpected error: %v", err)
	}
	got := tol["USD"]
	if got == nil {
		t.Fatalf("Infer: USD tolerance missing")
	}
	if got.Text('f') != "0.005" {
		t.Errorf("tolerance = %q, want %q", got.Text('f'), "0.005")
	}
}

// TestInfer_CostDisabledIgnoresCost verifies that when
// infer_tolerance_from_cost is not set, the cost component is ignored and
// only the units-based tolerance drives the result.
func TestInfer_CostDisabledIgnoresCost(t *testing.T) {
	units := amtStr(t, "1", "GOOG")
	perUnit := amtStr(t, "1.00", "USD")
	cash := amtStr(t, "-1.000000", "USD") // exp -6
	postings := []ast.Posting{
		{
			Account: "Assets:Brokerage",
			Amount:  &units,
			Cost:    &ast.CostSpec{PerUnit: &perUnit},
		},
		{Account: "Assets:Cash", Amount: &cash},
	}
	// Default: infer_tolerance_from_cost = false.
	tol, err := tolerance.Infer(postings, mustDefaults(t), []string{"USD"})
	if err != nil {
		t.Fatalf("Infer: unexpected error: %v", err)
	}
	got := tol["USD"]
	if got == nil {
		t.Fatalf("Infer: USD tolerance missing")
	}
	if got.Text('f') != "0.0000005" {
		t.Errorf("tolerance = %q, want %q", got.Text('f'), "0.0000005")
	}
}

// TestForAmount_Exponents exercises ForAmount for a few representative
// precisions with the default multiplier 0.5.
func TestForAmount_Exponents(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"100.00", "0.005"},
		{"100", "0.5"},
		{"100.001", "0.0005"},
	}
	opts := mustDefaults(t)
	for _, tc := range cases {
		a := amtStr(t, tc.in, "USD")
		got := tolerance.ForAmount(opts, a)
		if got.Text('f') != tc.want {
			t.Errorf("ForAmount(%q) = %s, want %s", tc.in, got.Text('f'), tc.want)
		}
	}
}

// TestForBalanceAssertion exercises the doubled-factor tolerance used
// for balance assertions, mirroring upstream beancount's
// get_balance_tolerance: tol = 2 * multiplier * 10^expo. The doubled
// factor is upstream's deliberate relaxation for hand-written balance
// assertions where rounding can exceed transaction-internal precision.
func TestForBalanceAssertion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		mult string // empty -> defaults
		want string
	}{
		{name: "exp -1, default mult", in: "100.0", want: "0.1"},
		{name: "exp -2, default mult", in: "100.00", want: "0.01"},
		{name: "exp -3, default mult", in: "100.000", want: "0.001"},
		{name: "exp 0, default mult", in: "100", want: "1"},
		{name: "exp -2, mult 1.0", in: "100.00", mult: "1.0", want: "0.02"},
		{name: "exp -3, mult 2.0", in: "100.000", mult: "2.0", want: "0.004"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts *options.Values
			if tc.mult == "" {
				opts = mustDefaults(t)
			} else {
				opts = mustOpts(t, map[string]string{"inferred_tolerance_multiplier": tc.mult})
			}
			a := amtStr(t, tc.in, "USD")
			got := tolerance.ForBalanceAssertion(opts, a)
			want := decimalFromString(t, tc.want)
			if got.Cmp(&want) != 0 {
				t.Errorf("ForBalanceAssertion(%q, mult=%q) = %s, want %s", tc.in, tc.mult, got.Text('f'), tc.want)
			}
		})
	}
}
