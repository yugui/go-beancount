package format

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// stubDC implements DisplayContext for tests.
type stubDC map[string]int

func (s stubDC) Precision(currency string) (int, bool) {
	n, ok := s[currency]
	return n, ok
}

func TestDisplayContextPad(t *testing.T) {
	// 50 USD with USD→2 should become 50.00 USD.
	src := `2024-01-15 * "Test"
  Expenses:Food  50 USD
  Assets:Cash
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "50.00 USD") {
		t.Errorf(`want 50.00 USD in output, got:
%s`, got)
	}
}

func TestDisplayContextTruncateHalfEven(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "half-even rounds down 1.125",
			src: `2024-01-15 * "Test"
  Expenses:Food  1.125 USD
  Assets:Cash
`,
			want: "1.12 USD",
		},
		{
			name: "half-even rounds up 1.135",
			src: `2024-01-15 * "Test"
  Expenses:Food  1.135 USD
  Assets:Cash
`,
			want: "1.14 USD",
		},
	}
	dc := stubDC{"USD": 2}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.src, WithDisplayContext(dc))
			if !strings.Contains(got, tt.want) {
				t.Errorf(`want %q in output, got:
%s`, tt.want, got)
			}
		})
	}
}

func TestDisplayContextNegative(t *testing.T) {
	src := `2024-01-15 * "Test"
  Expenses:Food  1.125 USD
  Assets:Cash  -1.125 USD
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "-1.12 USD") {
		t.Errorf(`want -1.12 USD in output, got:
%s`, got)
	}
}

func TestDisplayContextPassThroughUnknownCurrency(t *testing.T) {
	// EUR is not in the DisplayContext; its amounts pass through unchanged.
	src := `2024-01-15 * "Test"
  Expenses:Food  1.12345 EUR
  Assets:Cash
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "1.12345 EUR") {
		t.Errorf(`unknown currency: want 1.12345 EUR unchanged, got:
%s`, got)
	}
}

func TestDisplayContextPassThroughWhenNil(t *testing.T) {
	// Without WithDisplayContext the output is identical to Format(src).
	src := `2024-01-15 * "Test"
  Expenses:Food  50 USD
  Assets:Cash
`
	withoutDC := Format(src)
	withNilDC := Format(src, WithDisplayContext(nil))
	if withoutDC != withNilDC {
		t.Errorf(`nil DisplayContext should be a no-op:
withoutDC: %q
withNilDC: %q`, withoutDC, withNilDC)
	}
	// Source amount must not be rewritten.
	if strings.Contains(withNilDC, "50.00") {
		t.Errorf(`nil DisplayContext must not quantize amounts, got:
%s`, withNilDC)
	}
}

func TestDisplayContextBalanceTolerance(t *testing.T) {
	// The tolerance number in a balance directive shares the trailing CURRENCY
	// and should be quantized to the same precision.
	src := "2024-01-15 balance Assets:Cash 1000 ~ 0.05 JPY\n"
	dc := stubDC{"JPY": 0}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "1000 ~ 0 JPY") {
		t.Errorf(`want "1000 ~ 0 JPY" in output, got:
%s`, got)
	}
}

func TestDisplayContextCostNotQuantized(t *testing.T) {
	// Cost amounts inside {…} must never be rewritten; the outer unit amount
	// (HOOL) is quantized while the cost amount (USD) is not.
	src := `2024-01-15 * "Test"
  Assets:Stock  10 HOOL {502.123 USD}
  Assets:Cash
`
	dc := stubDC{"USD": 2, "HOOL": 0}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "10 HOOL {502.123 USD}") {
		t.Errorf(`want "10 HOOL {502.123 USD}" in output, got:
%s`, got)
	}
}

func TestDisplayContextPriceAnnotNotQuantized(t *testing.T) {
	// Price annotations (@/@@) must never be rewritten; the outer unit amount
	// (HOOL) is quantized while the price annotation (BTC) is not.
	src := `2024-01-15 * "Test"
  Assets:Stock  10 HOOL @ 0.0001 BTC
  Assets:Cash
`
	dc := stubDC{"BTC": 2, "HOOL": 0}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "10 HOOL @ 0.0001 BTC") {
		t.Errorf(`want "10 HOOL @ 0.0001 BTC" in output, got:
%s`, got)
	}
}

func TestDisplayContextMetadataNotQuantized(t *testing.T) {
	// NUMBER tokens in metadata values must not be rewritten.
	src := `2024-01-15 * "Test"
  Expenses:Food  50 USD
    shares: 1.23456
  Assets:Cash
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "1.23456") {
		t.Errorf(`metadata: want 1.23456 unchanged, got:
%s`, got)
	}
}

func TestDisplayContextAlignmentRegression(t *testing.T) {
	// After quantization pads from 0dp to 2dp the amount is wider; alignment
	// must still place the currency token at AmountColumn (52 by default).
	//
	// "  Expenses:Food" = 15 cols.
	// After quantize: "50.00 USD" = 9 cols.
	// padding = 52 - 15 - 9 = 28 spaces.
	src := `2024-01-15 * "Test"
  Expenses:Food  50 USD
  Assets:Cash
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc), WithAlignAmounts(true))
	want := "  Expenses:Food" + strings.Repeat(" ", 28) + "50.00 USD"
	if !strings.Contains(got, want) {
		t.Errorf(`alignment: want %q in output, got:
%s`, want, got)
	}
}

func TestDisplayContextCommaGroupingInteraction(t *testing.T) {
	// Quantize runs before comma grouping so the pipeline produces
	// comma-formatted quantized numbers.
	src := `2024-01-15 * "Test"
  Expenses:Food  1234 USD
  Assets:Cash
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc), WithCommaGrouping(true))
	if !strings.Contains(got, "1,234.00 USD") {
		t.Errorf(`comma+quantize: want 1,234.00 USD, got:
%s`, got)
	}
}

func TestDisplayContextPriceDirectiveAmount(t *testing.T) {
	// AmountNode in a price directive (not a posting) must be quantized.
	src := "2024-01-15 price USD 110.5 JPY\n"
	dc := stubDC{"JPY": 0}
	got := Format(src, WithDisplayContext(dc))
	if !strings.Contains(got, "110 JPY") {
		t.Errorf(`price directive: want "110 JPY" in output, got:
%s`, got)
	}
}

func TestDisplayContextArithExprPerOperand(t *testing.T) {
	// NUMBER tokens inside arithmetic expressions are quantized per-operand.
	// Each operand is a separate NUMBER token; they are all quantized individually.
	src := `2024-01-15 * "Test"
  Expenses:Food  (1.125 + 2.5) USD
  Assets:Cash
`
	dc := stubDC{"USD": 2}
	got := Format(src, WithDisplayContext(dc))
	// 1.125 rounds half-even to 1.12; 2.5 pads to 2.50.
	if !strings.Contains(got, "1.12") || !strings.Contains(got, "2.50") {
		t.Errorf(`arith expr: want per-operand quantization (1.12 and 2.50), got:
%s`, got)
	}
}

// TestDisplayContextWithOptionOverride verifies the end-to-end path from
// option "display_precision" through DisplayPrecisionContext to the formatter.
// Callers build the context inline from ledger fields; no helper exists in
// pkg/ast.
func TestDisplayContextWithOptionOverride(t *testing.T) {
	t.Run("override_forces_precision", func(t *testing.T) {
		src := `option "display_precision" "USD:0.01"

2024-01-15 * "Coffee"
  Expenses:Food  1.2 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		dc := &ast.DisplayPrecisionContext{
			Profile:   ledger.PrecisionProfile,
			Overrides: ledger.Options.IntMap("display_precision"),
		}
		got := Format(src, WithDisplayContext(dc))
		// 1.2 USD with display_precision "USD:0.01" → 2 fractional digits → 1.20 USD.
		if !strings.Contains(got, "1.20 USD") {
			t.Errorf("want 1.20 USD in output, got:\n%s", got)
		}
	})

	t.Run("empty_overrides_falls_back_to_profile", func(t *testing.T) {
		// Without display_precision option the override map is empty; the
		// context delegates to the inferred PrecisionProfile, which observed
		// "1.2" (one fractional digit). Output matches that profile's
		// precision.
		src := `2024-01-15 * "Coffee"
  Expenses:Food  1.2 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		dc := &ast.DisplayPrecisionContext{
			Profile:   ledger.PrecisionProfile,
			Overrides: ledger.Options.IntMap("display_precision"),
		}
		got := Format(src, WithDisplayContext(dc))
		if !strings.Contains(got, "1.2 USD") {
			t.Errorf("want 1.2 USD in output, got:\n%s", got)
		}
	})
}
