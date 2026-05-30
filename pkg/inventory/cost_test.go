package inventory

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// decimal parses s into an *apd.Decimal and fails the test on error.
func decimal(t *testing.T, s string) *apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return d
}

// decimalVal is the value form of decimal, for embedding inside structs
// whose decimal field is not a pointer.
func decimalVal(t *testing.T, s string) apd.Decimal {
	t.Helper()
	return *decimal(t, s)
}

func TestCostEqual(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	base := Cost{
		Number:   decimalVal(t, "100.5"),
		Currency: "USD",
		Date:     date,
		Label:    "lot-a",
	}

	tests := []struct {
		name string
		a, b Cost
		want bool
	}{
		{
			name: "identical",
			a:    base,
			b: Cost{
				Number: decimalVal(t, "100.5"), Currency: "USD", Date: date, Label: "lot-a",
			},
			want: true,
		},
		{
			name: "different number",
			a:    base,
			b: Cost{
				Number: decimalVal(t, "100.6"), Currency: "USD", Date: date, Label: "lot-a",
			},
			want: false,
		},
		{
			name: "different currency",
			a:    base,
			b: Cost{
				Number: decimalVal(t, "100.5"), Currency: "EUR", Date: date, Label: "lot-a",
			},
			want: false,
		},
		{
			name: "different date",
			a:    base,
			b: Cost{
				Number: decimalVal(t, "100.5"), Currency: "USD",
				Date:  time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
				Label: "lot-a",
			},
			want: false,
		},
		{
			name: "different label",
			a:    base,
			b: Cost{
				Number: decimalVal(t, "100.5"), Currency: "USD", Date: date, Label: "lot-b",
			},
			want: false,
		},
		{
			name: "same value different scale",
			a:    Cost{Number: decimalVal(t, "100.50"), Currency: "USD", Date: date, Label: ""},
			b:    Cost{Number: decimalVal(t, "100.5"), Currency: "USD", Date: date, Label: ""},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(&tc.b); got != tc.want {
				t.Errorf("Equal = %v, want %v", got, tc.want)
			}
			// Equal is symmetric.
			if got := tc.b.Equal(&tc.a); got != tc.want {
				t.Errorf("Equal (reversed) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCostClone(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var c *Cost
		if got := c.Clone(); got != nil {
			t.Errorf("Clone() = %v, want nil", got)
		}
	})

	t.Run("deep copy", func(t *testing.T) {
		orig := &Cost{
			Number:   decimalVal(t, "42.5"),
			Currency: "USD",
			Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-1",
		}
		clone := orig.Clone()
		if clone == orig {
			t.Fatal("Clone returned the same pointer")
		}
		if !clone.Equal(orig) {
			t.Errorf("clone %+v not equal to original %+v", clone, orig)
		}

		// Mutate the clone; the original must be untouched.
		clone.Label = "lot-2"
		newNum := decimalVal(t, "99.9")
		clone.Number.Set(&newNum)

		if orig.Label != "lot-1" {
			t.Errorf("orig.Label mutated to %q", orig.Label)
		}
		if got := orig.Number.String(); got != "42.5" {
			t.Errorf("orig.Number mutated to %q", got)
		}
	})
}

func TestResolveLot(t *testing.T) {
	txnDate := time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC)
	specDate := time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)

	t.Run("nil spec", func(t *testing.T) {
		got, finding, err := ResolveLot(nil, ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"}, txnDate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finding != nil {
			t.Fatalf("unexpected finding: %v", finding)
		}
		if got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("empty spec", func(t *testing.T) {
		spec := &ast.CostSpec{}
		_, d, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"}, txnDate)
		if err != nil {

			t.Fatalf("system error: %v", err)

		}

		if d == nil {

			t.Fatal("expected finding, got nil")

		}
		if d.Code != CodeAugmentationRequiresCost {
			t.Errorf("Code = %v, want CodeAugmentationRequiresCost", d.Code)
		}
	})

	t.Run("per-unit only with spec date", func(t *testing.T) {
		perUnit := decimalVal(t, "100")
		spec := &ast.CostSpec{
			PerUnit:  &perUnit,
			Currency: "USD",
			Date:     &specDate,
			Label:    "lot-a",
		}
		cost, finding, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"}, txnDate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finding != nil {
			t.Fatalf("unexpected finding: %v", finding)
		}
		if got := cost.Number.String(); got != "100" {
			t.Errorf("Number = %q, want %q", got, "100")
		}
		if cost.Currency != "USD" {
			t.Errorf("Currency = %q, want %q", cost.Currency, "USD")
		}
		if !cost.Date.Equal(specDate) {
			t.Errorf("Date = %v, want %v", cost.Date, specDate)
		}
		if cost.Label != "lot-a" {
			t.Errorf("Label = %q, want %q", cost.Label, "lot-a")
		}
	})

	t.Run("per-unit only, date defaults to txn date", func(t *testing.T) {
		perUnit := decimalVal(t, "100")
		spec := &ast.CostSpec{
			PerUnit:  &perUnit,
			Currency: "USD",
		}
		cost, finding, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"}, txnDate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finding != nil {
			t.Fatalf("unexpected finding: %v", finding)
		}
		if !cost.Date.Equal(txnDate) {
			t.Errorf("Date = %v, want txnDate %v", cost.Date, txnDate)
		}
	})

	t.Run("total only", func(t *testing.T) {
		total := decimalVal(t, "500")
		spec := &ast.CostSpec{
			Total:    &total,
			Currency: "USD",
		}
		cost, finding, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"}, txnDate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finding != nil {
			t.Fatalf("unexpected finding: %v", finding)
		}
		want := decimalVal(t, "100")
		if cost.Number.Cmp(&want) != 0 {
			t.Errorf("Number = %s, want %s", cost.Number.String(), want.String())
		}
		// Exact total→per-unit division must carry the ideal exponent, not
		// apd's 34-digit padding (500/5 = 100, not 100.000…0).
		if s := cost.Number.Text('f'); s != "100" {
			t.Errorf("Number.Text('f') = %s, want 100 (unpadded)", s)
		}
		if cost.Currency != "USD" {
			t.Errorf("Currency = %q, want %q", cost.Currency, "USD")
		}
		if !cost.Date.Equal(txnDate) {
			t.Errorf("Date = %v, want txnDate %v", cost.Date, txnDate)
		}
	})

	t.Run("total only with negative units uses magnitude", func(t *testing.T) {
		total := decimalVal(t, "500")
		spec := &ast.CostSpec{
			Total:    &total,
			Currency: "USD",
		}
		cost, finding, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "-5"), Currency: "ACME"}, txnDate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finding != nil {
			t.Fatalf("unexpected finding: %v", finding)
		}
		want := decimalVal(t, "100")
		if cost.Number.Cmp(&want) != 0 {
			t.Errorf("Number = %s, want positive %s", cost.Number.String(), want.String())
		}
		if cost.Number.Negative {
			t.Errorf("Number is negative: %s", cost.Number.String())
		}
	})

	t.Run("combined form", func(t *testing.T) {
		perUnit := decimalVal(t, "100")
		total := decimalVal(t, "50")
		spec := &ast.CostSpec{
			PerUnit:  &perUnit,
			Total:    &total,
			Currency: "USD",
		}
		cost, finding, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "5"), Currency: "ACME"}, txnDate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finding != nil {
			t.Fatalf("unexpected finding: %v", finding)
		}
		// 100 + 50/5 = 110
		want := decimalVal(t, "110")
		if cost.Number.Cmp(&want) != 0 {
			t.Errorf("Number = %s, want %s", cost.Number.String(), want.String())
		}
		if cost.Currency != "USD" {
			t.Errorf("Currency = %q, want %q", cost.Currency, "USD")
		}
	})

	t.Run("total only with zero units", func(t *testing.T) {
		// A cost spec with a non-nil Total paired with zero units
		// makes the per-unit cost undefined. ResolveLot reports it
		// as a user finding with CodeZeroUnitsCostTotal rather than
		// letting the underlying division-by-zero surface as a
		// system error.
		total := decimalVal(t, "500")
		spec := &ast.CostSpec{
			Total:    &total,
			Currency: "USD",
		}
		_, d, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "0"), Currency: "ACME"}, txnDate)
		if err != nil {

			t.Fatalf("system error: %v", err)

		}

		if d == nil {

			t.Fatal("expected finding, got nil")

		}
		if d.Code != CodeZeroUnitsCostTotal {
			t.Errorf("Code = %q, want %q", d.Code, CodeZeroUnitsCostTotal)
		}
	})

	t.Run("per-unit and total with zero units", func(t *testing.T) {
		// The combined surcharge form also divides Total by units;
		// zero units triggers the same diagnostic.
		perUnit := decimalVal(t, "10")
		total := decimalVal(t, "50")
		spec := &ast.CostSpec{
			PerUnit:  &perUnit,
			Total:    &total,
			Currency: "USD",
		}
		_, d, err := ResolveLot(spec, ast.Amount{Number: decimalVal(t, "0"), Currency: "ACME"}, txnDate)
		if err != nil {

			t.Fatalf("system error: %v", err)

		}

		if d == nil {

			t.Fatal("expected finding, got nil")

		}
		if d.Code != CodeZeroUnitsCostTotal {
			t.Errorf("Code = %q, want %q", d.Code, CodeZeroUnitsCostTotal)
		}
	})
}
