package printer_test

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
)

// stubDC implements format.DisplayContext (via formatopt.DisplayContext) for
// printer tests. Defined locally to prove the interface boundary: no import
// of *ast.PrecisionProfile is required.
type stubDC map[string]int

func (s stubDC) Precision(currency string) (int, bool) {
	n, ok := s[currency]
	return n, ok
}

func TestDisplayContextPostingAmountPad(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Test",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("50", "USD")},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"USD": 2}))
	if !strings.Contains(got, "50.00 USD") {
		t.Errorf("want 50.00 USD in output, got:\n%s", got)
	}
}

func TestDisplayContextHalfEven(t *testing.T) {
	tests := []struct {
		name string
		num  string
		want string
	}{
		{"rounds down 1.125", "1.125", "1.12 USD"},
		{"rounds up 1.135", "1.135", "1.14 USD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := print(t, &ast.Transaction{
				Date:      date("2024-01-15"),
				Flag:      '*',
				Narration: "Test",
				Postings: []ast.Posting{
					{Account: "Expenses:Food", Amount: amountp(tt.num, "USD")},
					{Account: "Assets:Cash"},
				},
			}, format.WithDisplayContext(stubDC{"USD": 2}))
			if !strings.Contains(got, tt.want) {
				t.Errorf("want %q in output, got:\n%s", tt.want, got)
			}
		})
	}
}

func TestDisplayContextPassThroughUnknownCurrency(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Test",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("1.12345", "EUR")},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"USD": 2}))
	if !strings.Contains(got, "1.12345 EUR") {
		t.Errorf("want 1.12345 EUR unchanged, got:\n%s", got)
	}
}

func TestDisplayContextPassThroughWhenNil(t *testing.T) {
	txn := &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Test",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("50", "USD")},
			{Account: "Assets:Cash"},
		},
	}
	without := print(t, txn)
	withNil := print(t, txn, format.WithDisplayContext(nil))
	if without != withNil {
		t.Errorf("nil DisplayContext should be a no-op:\nwithout: %q\nwithNil: %q", without, withNil)
	}
	if strings.Contains(withNil, "50.00") {
		t.Errorf("nil DisplayContext must not quantize, got:\n%s", withNil)
	}
}

func TestDisplayContextBalanceMainAndTolerance(t *testing.T) {
	tol := decimal("0.05")
	got := print(t, &ast.Balance{
		Date:      date("2024-01-15"),
		Account:   "Assets:Cash",
		Amount:    amount("1000", "JPY"),
		Tolerance: &tol,
	}, format.WithDisplayContext(stubDC{"JPY": 0}))
	want := "2024-01-15 balance Assets:Cash 1000 ~ 0 JPY\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDisplayContextPriceDirective(t *testing.T) {
	got := print(t, &ast.Price{
		Date:      date("2024-01-15"),
		Commodity: "USD",
		Amount:    amount("110.5", "JPY"),
	}, format.WithDisplayContext(stubDC{"JPY": 0}))
	want := "2024-01-15 price USD 110 JPY\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDisplayContextCostNotQuantized(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "buy",
		Postings: []ast.Posting{
			{
				Account: "Assets:Stock",
				Amount:  amountp("10", "HOOL"),
				Cost: &ast.CostSpec{
					PerUnit:  decp("502.123"),
					Currency: "USD",
				},
			},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"USD": 2, "HOOL": 0}))
	if !strings.Contains(got, "10 HOOL {502.123 USD}") {
		t.Errorf("want cost 502.123 unchanged, got:\n%s", got)
	}
}

func TestDisplayContextPriceAnnotationNotQuantized(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "fx",
		Postings: []ast.Posting{
			{
				Account: "Assets:Stock",
				Amount:  amountp("10", "HOOL"),
				Price:   &ast.PriceAnnotation{Amount: amount("0.0001", "BTC")},
			},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"BTC": 2, "HOOL": 0}))
	if !strings.Contains(got, "10 HOOL @ 0.0001 BTC") {
		t.Errorf("want price annotation 0.0001 unchanged, got:\n%s", got)
	}
}

func TestDisplayContextMetadataNotQuantized(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Test",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"shares": {Kind: ast.MetaNumber, Number: decimal("1.23456")},
		}},
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("50", "USD")},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"USD": 2}))
	if !strings.Contains(got, "1.23456") {
		t.Errorf("metadata number must not be quantized, got:\n%s", got)
	}
}

func TestDisplayContextAlignmentRegression(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Test",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("50", "USD")},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"USD": 2}))
	want := "  Expenses:Food" + strings.Repeat(" ", 28) + "50.00 USD"
	if !strings.Contains(got, want) {
		t.Errorf("want aligned %q, got:\n%s", want, got)
	}
}

func TestDisplayContextCommaGroupingInteraction(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Test",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("1234", "USD")},
			{Account: "Assets:Cash"},
		},
	}, format.WithDisplayContext(stubDC{"USD": 2}), format.WithCommaGrouping(true))
	if !strings.Contains(got, "1,234.00 USD") {
		t.Errorf("want 1,234.00 USD, got:\n%s", got)
	}
}

// TestDisplayContextWithOptionOverride verifies the end-to-end path from
// option "display_precision" through DisplayPrecisionContext to the AST
// printer. Callers build the context inline from ledger fields; no helper
// exists in pkg/ast.
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
		var txn *ast.Transaction
		for _, d := range ledger.All() {
			if t2, ok := d.(*ast.Transaction); ok {
				txn = t2
				break
			}
		}
		if txn == nil {
			t.Fatal("no Transaction in ledger")
		}
		dc := &ast.DisplayPrecisionContext{
			Profile:   ledger.PrecisionProfile,
			Overrides: ledger.Options.IntMap("display_precision"),
		}
		got := print(t, txn, format.WithDisplayContext(dc))
		// display_precision "USD:0.01" → 2 fractional digits → 1.20 USD.
		if !strings.Contains(got, "1.20 USD") {
			t.Errorf("want 1.20 USD in output, got:\n%s", got)
		}
	})

	t.Run("empty_overrides_falls_back_to_profile", func(t *testing.T) {
		// Without display_precision option the override map is empty; the
		// context delegates to the inferred PrecisionProfile, which
		// observed "1.2" (one fractional digit). Output matches that
		// profile's precision.
		src := `2024-01-15 * "Coffee"
  Expenses:Food  1.2 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		var txn *ast.Transaction
		for _, d := range ledger.All() {
			if t2, ok := d.(*ast.Transaction); ok {
				txn = t2
				break
			}
		}
		if txn == nil {
			t.Fatal("no Transaction in ledger")
		}
		dc := &ast.DisplayPrecisionContext{
			Profile:   ledger.PrecisionProfile,
			Overrides: ledger.Options.IntMap("display_precision"),
		}
		got := print(t, txn, format.WithDisplayContext(dc))
		if !strings.Contains(got, "1.2 USD") {
			t.Errorf("want 1.2 USD in output, got:\n%s", got)
		}
	})
}
