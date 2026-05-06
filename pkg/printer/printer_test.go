package printer_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
	"github.com/yugui/go-beancount/pkg/printer"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// decimal parses a decimal literal for use as a fixture. Callers pass
// hard-coded strings, so a parse failure is a programmer error in the test
// itself; surface it loudly with the offending input rather than returning a
// silent zero value.
func decimal(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(fmt.Sprintf("decimal(%q): %v", s, err))
	}
	return *d
}

func amount(num string, cur string) ast.Amount {
	return ast.Amount{Number: decimal(num), Currency: cur}
}

// date parses a YYYY-MM-DD fixture date. Like decimal, a parse failure
// indicates a malformed test input and panics with the offending string.
func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(fmt.Sprintf("date(%q): %v", s, err))
	}
	return t
}

func datep(s string) *time.Time {
	t := date(s)
	return &t
}

func amountp(num, cur string) *ast.Amount {
	a := amount(num, cur)
	return &a
}

func print(t *testing.T, node any, opts ...format.Option) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, node, opts...); err != nil {
		t.Fatalf("Fprint: %v", err)
	}
	return buf.String()
}

func TestOption(t *testing.T) {
	got := print(t, &ast.Option{Key: "title", Value: "My Ledger"})
	want := "option \"title\" \"My Ledger\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestPlugin(t *testing.T) {
	t.Run("with config", func(t *testing.T) {
		got := print(t, &ast.Plugin{Name: "beancount.plugins.auto", Config: "config_val"})
		want := "plugin \"beancount.plugins.auto\" \"config_val\"\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
	t.Run("without config", func(t *testing.T) {
		got := print(t, &ast.Plugin{Name: "beancount.plugins.auto"})
		want := "plugin \"beancount.plugins.auto\"\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
}

func TestInclude(t *testing.T) {
	got := print(t, &ast.Include{Path: "other.beancount"})
	want := "include \"other.beancount\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestOpen(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		got := print(t, &ast.Open{
			Date:    date("2024-01-01"),
			Account: "Assets:Bank",
		})
		want := "2024-01-01 open Assets:Bank\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
	t.Run("with currencies and booking", func(t *testing.T) {
		got := print(t, &ast.Open{
			Date:       date("2024-01-01"),
			Account:    "Assets:Bank",
			Currencies: []string{"USD", "EUR"},
			Booking:    ast.BookingStrict,
		})
		want := "2024-01-01 open Assets:Bank USD,EUR \"STRICT\"\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
	t.Run("with metadata", func(t *testing.T) {
		got := print(t, &ast.Open{
			Date:    date("2024-01-01"),
			Account: "Assets:Bank",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"institution": {Kind: ast.MetaString, String: "Chase"},
			}},
		})
		want := "2024-01-01 open Assets:Bank\n  institution: \"Chase\"\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
}

func TestClose(t *testing.T) {
	got := print(t, &ast.Close{Date: date("2024-06-01"), Account: "Assets:Old"})
	want := "2024-06-01 close Assets:Old\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestCommodity(t *testing.T) {
	got := print(t, &ast.Commodity{Date: date("2024-01-01"), Currency: "BTC"})
	want := "2024-01-01 commodity BTC\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestBalance(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		got := print(t, &ast.Balance{
			Date:    date("2024-01-15"),
			Account: "Assets:Bank",
			Amount:  amount("1234.56", "USD"),
		})
		want := "2024-01-15 balance Assets:Bank 1234.56 USD\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
	t.Run("with tolerance", func(t *testing.T) {
		tol := decimal("0.01")
		got := print(t, &ast.Balance{
			Date:      date("2024-01-15"),
			Account:   "Assets:Bank",
			Amount:    amount("1234.56", "USD"),
			Tolerance: &tol,
		})
		want := "2024-01-15 balance Assets:Bank 1234.56 ~ 0.01 USD\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
	t.Run("official example", func(t *testing.T) {
		tol := decimal("0.002")
		got := print(t, &ast.Balance{
			Date:      date("2013-09-20"),
			Account:   "Assets:Investing:Funds",
			Amount:    amount("319.020", "RGAGX"),
			Tolerance: &tol,
		})
		want := "2013-09-20 balance Assets:Investing:Funds 319.020 ~ 0.002 RGAGX\n"
		if got != want {
			t.Errorf("Fprint() = %q, want %q", got, want)
		}
	})
}

func TestPad(t *testing.T) {
	got := print(t, &ast.Pad{
		Date:       date("2024-01-01"),
		Account:    "Assets:Bank",
		PadAccount: "Equity:Opening-Balances",
	})
	want := "2024-01-01 pad Assets:Bank Equity:Opening-Balances\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestNote(t *testing.T) {
	got := print(t, &ast.Note{
		Date:    date("2024-03-01"),
		Account: "Assets:Bank",
		Comment: "opened online",
	})
	want := "2024-03-01 note Assets:Bank \"opened online\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestDocument(t *testing.T) {
	got := print(t, &ast.Document{
		Date:    date("2024-04-01"),
		Account: "Assets:Bank",
		Path:    "/path/to/doc.pdf",
	})
	want := "2024-04-01 document Assets:Bank \"/path/to/doc.pdf\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestNoteWithTagsLinks(t *testing.T) {
	got := print(t, &ast.Note{
		Date:    date("2024-03-01"),
		Account: "Assets:Brokerage",
		Comment: "review",
		Tags:    []string{"trip-2024"},
		Links:   []string{"invoice-42"},
	})
	want := "2024-03-01 note Assets:Brokerage \"review\" #trip-2024 ^invoice-42\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestDocumentWithTagsLinks(t *testing.T) {
	got := print(t, &ast.Document{
		Date:    date("2024-04-01"),
		Account: "Assets:Brokerage",
		Path:    "receipt.pdf",
		Tags:    []string{"trip-2024"},
		Links:   []string{"invoice-42"},
	})
	want := "2024-04-01 document Assets:Brokerage \"receipt.pdf\" #trip-2024 ^invoice-42\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

// TestNoteWithTagsLinksAndMetadata verifies the print order when a note
// carries trailing tags/links AND metadata: the tags/links sit on the
// same line as the comment string and end with a newline, after which
// the metadata line is indented. This pins the boundary between the
// tags/links sequence and the metadata block.
func TestNoteWithTagsLinksAndMetadata(t *testing.T) {
	got := print(t, &ast.Note{
		Date:    date("2024-06-01"),
		Account: "Assets:Brokerage",
		Comment: "opened",
		Tags:    []string{"trip-2024"},
		Links:   []string{"invoice-42"},
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"ref": {Kind: ast.MetaString, String: "PR-7"},
		}},
	})
	want := "2024-06-01 note Assets:Brokerage \"opened\" #trip-2024 ^invoice-42\n  ref: \"PR-7\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

// TestDocumentWithTagsLinksAndMetadata verifies the print order when a
// document directive carries trailing tags/links AND metadata: the
// tags/links sit on the same line as the path string and end with a
// newline, after which the metadata line is indented. This pins the
// boundary between the tags/links sequence and the metadata block.
func TestDocumentWithTagsLinksAndMetadata(t *testing.T) {
	got := print(t, &ast.Document{
		Date:    date("2024-06-01"),
		Account: "Assets:Brokerage",
		Path:    "receipt.pdf",
		Tags:    []string{"trip-2024"},
		Links:   []string{"invoice-42"},
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"ref": {Kind: ast.MetaString, String: "PR-7"},
		}},
	})
	want := "2024-06-01 document Assets:Brokerage \"receipt.pdf\" #trip-2024 ^invoice-42\n  ref: \"PR-7\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestEvent(t *testing.T) {
	got := print(t, &ast.Event{
		Date:  date("2024-01-01"),
		Name:  "location",
		Value: "New York",
	})
	want := "2024-01-01 event \"location\" \"New York\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestQuery(t *testing.T) {
	got := print(t, &ast.Query{
		Date: date("2024-01-01"),
		Name: "balance-check",
		BQL:  "SELECT account, balance",
	})
	want := "2024-01-01 query \"balance-check\" \"SELECT account, balance\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestPriceDirective(t *testing.T) {
	got := print(t, &ast.Price{
		Date:      date("2024-01-01"),
		Commodity: "BTC",
		Amount:    amount("42000.00", "USD"),
	})
	want := "2024-01-01 price BTC 42000.00 USD\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestCustom(t *testing.T) {
	got := print(t, &ast.Custom{
		Date:     date("2024-01-01"),
		TypeName: "budget",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Expenses:Food"},
			{Kind: ast.MetaString, String: "monthly"},
			{Kind: ast.MetaAmount, Amount: amount("500.00", "USD")},
		},
	})
	want := "2024-01-01 custom \"budget\" Expenses:Food \"monthly\" 500.00 USD\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestTransactionBasic(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Payee:     "Grocery Store",
		Narration: "Weekly shopping",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("50.00", "USD")},
			{Account: "Assets:Bank"},
		},
	})
	// "  Expenses:Food" = 15, "50.00 USD" = 9, padding = 52-15-9 = 28
	want := `2024-01-15 * "Grocery Store" "Weekly shopping"
  Expenses:Food                            50.00 USD
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTransactionNarrationOnly(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Transfer",
		Postings: []ast.Posting{
			{Account: "Assets:Savings", Amount: amountp("100.00", "USD")},
			{Account: "Assets:Checking"},
		},
	})
	// "  Assets:Savings" = 16, "100.00 USD" = 10, padding = 52-16-10 = 26
	want := `2024-01-15 * "Transfer"
  Assets:Savings                          100.00 USD
  Assets:Checking
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTransactionWithTagsAndLinks(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '!',
		Narration: "Pending",
		Tags:      []string{"trip-2024"},
		Links:     []string{"invoice-123"},
		Postings: []ast.Posting{
			{Account: "Expenses:Travel", Amount: amountp("200.00", "USD")},
			{Account: "Liabilities:Credit"},
		},
	})
	want := `2024-01-15 ! "Pending" #trip-2024 ^invoice-123
  Expenses:Travel                         200.00 USD
  Liabilities:Credit
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTransactionWithPostingFlag(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Mixed",
		Postings: []ast.Posting{
			{Flag: '!', Account: "Expenses:Food", Amount: amountp("30.00", "USD")},
			{Account: "Assets:Bank"},
		},
	})
	want := `2024-01-15 * "Mixed"
  ! Expenses:Food                          30.00 USD
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTransactionWithMetadata(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Lunch",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"source": {Kind: ast.MetaString, String: "receipt"},
		}},
		Postings: []ast.Posting{
			{
				Account: "Expenses:Food",
				Amount:  amountp("15.00", "USD"),
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"category": {Kind: ast.MetaString, String: "lunch"},
				}},
			},
			{Account: "Assets:Cash"},
		},
	})
	want := `2024-01-15 * "Lunch"
  source: "receipt"
  Expenses:Food                            15.00 USD
    category: "lunch"
  Assets:Cash
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCostSpecPerUnit(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Buy stock",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amountp("10", "AAPL"),
				Cost: &ast.CostSpec{
					PerUnit: amountp("150.00", "USD"),
					Date:    datep("2024-01-15"),
					Label:   "lot1",
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := `2024-01-15 * "Buy stock"
  Assets:Brokerage                           10 AAPL {150.00 USD, 2024-01-15, "lot1"}
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCostSpecTotal(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Buy stock total",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amountp("10", "AAPL"),
				Cost: &ast.CostSpec{
					Total: amountp("1500.00", "USD"),
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := `2024-01-15 * "Buy stock total"
  Assets:Brokerage                           10 AAPL {{1500.00 USD}}
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCostSpecCombined(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Buy stock combined",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amountp("10", "AAPL"),
				Cost: &ast.CostSpec{
					PerUnit: amountp("502.12", "USD"),
					Total:   amountp("9.95", "USD"),
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := `2024-01-15 * "Buy stock combined"
  Assets:Brokerage                           10 AAPL {502.12 # 9.95 USD}
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCostSpecCombinedExplicitCurrencies(t *testing.T) {
	// Defensive case: mismatched currencies. The lowerer rejects this, but
	// a directly-constructed AST should still produce a valid-looking
	// rendering with both currencies emitted explicitly, never a panic.
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Mismatched cost",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amountp("10", "AAPL"),
				Cost: &ast.CostSpec{
					PerUnit: amountp("502.12", "EUR"),
					Total:   amountp("9.95", "USD"),
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := `2024-01-15 * "Mismatched cost"
  Assets:Brokerage                           10 AAPL {502.12 EUR # 9.95 USD}
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCostSpecCombinedWithDateAndLabel(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Buy stock combined annotated",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amountp("10", "AAPL"),
				Cost: &ast.CostSpec{
					PerUnit: amountp("502.12", "USD"),
					Total:   amountp("9.95", "USD"),
					Date:    datep("2024-01-15"),
					Label:   "lot1",
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := `2024-01-15 * "Buy stock combined annotated"
  Assets:Brokerage                           10 AAPL {502.12 # 9.95 USD, 2024-01-15, "lot1"}
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCostSpecCombinedRoundTrip(t *testing.T) {
	// First parse → lower → print test for the combined form. This test
	// deliberately skips validation because validation has not yet been updated
	// to accept the combined form.
	src := `2024-01-15 * "Buy stock combined"
  Assets:Brokerage  10 AAPL {502.12 # 9.95 USD}
  Assets:Bank
`

	cst := syntax.Parse(src)
	if len(cst.Errors) > 0 {
		t.Fatalf("parse errors: %v", cst.Errors)
	}
	file := ast.Lower("test.beancount", cst)
	if len(file.Diagnostics) > 0 {
		t.Fatalf("lower diagnostics: %v", file.Diagnostics)
	}

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, file); err != nil {
		t.Fatalf("Fprint: %v", err)
	}
	out := buf.String()
	// The printer normalizes the amount-column alignment, so the round-tripped
	// output is not byte-identical to src; compare against the canonical printer
	// output instead. Update this constant if the printer's formatting changes.
	const want = `2024-01-15 * "Buy stock combined"
  Assets:Brokerage                           10 AAPL {502.12 # 9.95 USD}
  Assets:Bank
`
	if out != want {
		t.Errorf("printer round-trip output mismatch\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestCostSpecEmpty(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Sell stock",
		Postings: []ast.Posting{
			{
				Account: "Assets:Brokerage",
				Amount:  amountp("-10", "AAPL"),
				Cost:    &ast.CostSpec{},
			},
			{Account: "Income:Gains"},
		},
	})
	want := `2024-01-15 * "Sell stock"
  Assets:Brokerage                          -10 AAPL {}
  Income:Gains
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// roundTripCostSpec parses src, lowers it (without any booking pass), prints
// the resulting AST, and compares against want. It centralizes the
// parse → lower → print → compare flow shared by the incomplete-CostSpec
// round-trip cases below so each individual test stays focused on its input
// and expected output.
func roundTripCostSpec(t *testing.T, src, want string) {
	t.Helper()
	cst := syntax.Parse(src)
	if len(cst.Errors) > 0 {
		t.Fatalf("parse errors: %v", cst.Errors)
	}
	file := ast.Lower("test.beancount", cst)
	if len(file.Diagnostics) > 0 {
		t.Fatalf("lower diagnostics: %v", file.Diagnostics)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, file); err != nil {
		t.Fatalf("Fprint: %v", err)
	}
	if got := buf.String(); got != want {
		t.Errorf("printer round-trip output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestCostSpecDateOnlyRoundTrip exercises the parse → lower → print pipeline
// for a CostSpec that carries only a date (no per-unit, total, or label).
// This is an incomplete cost shape that the lowerer accepts without booking,
// and the printer must round-trip it verbatim.
func TestCostSpecDateOnlyRoundTrip(t *testing.T) {
	src := `2025-01-01 * "test"
  Assets:Foo  -1 ABC {2024-05-14}
  Expenses:Bar  10 USD
`
	want := `2025-01-01 * "test"
  Assets:Foo                                  -1 ABC {2024-05-14}
  Expenses:Bar                                10 USD
`
	roundTripCostSpec(t, src, want)
}

// TestCostSpecLabelOnlyRoundTrip exercises the parse → lower → print pipeline
// for a CostSpec that carries only a label.
func TestCostSpecLabelOnlyRoundTrip(t *testing.T) {
	src := `2025-01-01 * "test"
  Assets:Foo  -1 ABC {"lot1"}
  Expenses:Bar  10 USD
`
	want := `2025-01-01 * "test"
  Assets:Foo                                  -1 ABC {"lot1"}
  Expenses:Bar                                10 USD
`
	roundTripCostSpec(t, src, want)
}

// TestCostSpecPerUnitOnlyRoundTrip exercises the parse → lower → print
// pipeline for a CostSpec that carries only a per-unit amount, with no date
// or label. The pre-existing TestCostSpecPerUnit always supplies date and
// label, so this case pins the bare per-unit rendering.
func TestCostSpecPerUnitOnlyRoundTrip(t *testing.T) {
	src := `2025-01-01 * "test"
  Assets:Foo  -1 ABC {100.00 USD}
  Expenses:Bar  10 USD
`
	want := `2025-01-01 * "test"
  Assets:Foo                                  -1 ABC {100.00 USD}
  Expenses:Bar                                10 USD
`
	roundTripCostSpec(t, src, want)
}

// TestCostSpecTotalOnlyRoundTrip exercises the parse → lower → print
// pipeline for the legacy total-only "{{...}}" form. The pre-existing
// TestCostSpecTotal builds the AST manually; this case pins the full
// source-to-source round-trip.
func TestCostSpecTotalOnlyRoundTrip(t *testing.T) {
	src := `2025-01-01 * "test"
  Assets:Foo  -1 ABC {{200.00 USD}}
  Expenses:Bar  10 USD
`
	want := `2025-01-01 * "test"
  Assets:Foo                                  -1 ABC {{200.00 USD}}
  Expenses:Bar                                10 USD
`
	roundTripCostSpec(t, src, want)
}

// TestCostSpecEmptyRoundTrip exercises the parse → lower → print pipeline
// for a fully empty cost annotation "{}". The pre-existing TestCostSpecEmpty
// builds the AST manually; this case pins the full source-to-source
// round-trip.
func TestCostSpecEmptyRoundTrip(t *testing.T) {
	src := `2025-01-01 * "test"
  Assets:Foo  -1 ABC {}
  Expenses:Bar  10 USD
`
	want := `2025-01-01 * "test"
  Assets:Foo                                  -1 ABC {}
  Expenses:Bar                                10 USD
`
	roundTripCostSpec(t, src, want)
}

func TestPriceAnnotationPerUnit(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Exchange",
		Postings: []ast.Posting{
			{
				Account: "Assets:EUR",
				Amount:  amountp("100", "EUR"),
				Price:   &ast.PriceAnnotation{Amount: amount("1.10", "USD")},
			},
			{Account: "Assets:USD"},
		},
	})
	want := `2024-01-15 * "Exchange"
  Assets:EUR                                 100 EUR @ 1.10 USD
  Assets:USD
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPriceAnnotationTotal(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Exchange total",
		Postings: []ast.Posting{
			{
				Account: "Assets:EUR",
				Amount:  amountp("100", "EUR"),
				Price:   &ast.PriceAnnotation{Amount: amount("110.00", "USD"), IsTotal: true},
			},
			{Account: "Assets:USD"},
		},
	})
	want := `2024-01-15 * "Exchange total"
  Assets:EUR                                 100 EUR @@ 110.00 USD
  Assets:USD
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFileWithMultipleDirectives(t *testing.T) {
	f := ast.File{
		Directives: []ast.Directive{
			&ast.Option{Key: "title", Value: "Test"},
			&ast.Open{Date: date("2024-01-01"), Account: "Assets:Bank"},
			&ast.Close{Date: date("2024-12-31"), Account: "Assets:Bank"},
		},
	}
	got := print(t, &f, format.WithInsertBlankLinesBetweenDirectives(true))
	want := `option "title" "Test"

2024-01-01 open Assets:Bank

2024-12-31 close Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFileByValue(t *testing.T) {
	f := ast.File{
		Directives: []ast.Directive{
			&ast.Option{Key: "title", Value: "Test"},
		},
	}
	got := print(t, f)
	want := "option \"title\" \"Test\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestLedger(t *testing.T) {
	l := ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Option{Key: "title", Value: "Ledger"},
		&ast.Open{Date: date("2024-01-01"), Account: "Assets:Cash"},
	})
	got := print(t, &l, format.WithInsertBlankLinesBetweenDirectives(true))
	want := `option "title" "Ledger"

2024-01-01 open Assets:Cash
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDirectiveSlice(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Include{Path: "a.beancount"},
		&ast.Include{Path: "b.beancount"},
	}
	got := print(t, dirs, format.WithInsertBlankLinesBetweenDirectives(true))
	want := `include "a.beancount"

include "b.beancount"
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDirectivesNoBlankLinesInsertedByDefault(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Include{Path: "a.beancount"},
		&ast.Include{Path: "b.beancount"},
		&ast.Include{Path: "c.beancount"},
	}
	got := print(t, dirs)
	want := `include "a.beancount"
include "b.beancount"
include "c.beancount"
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAmountDirect(t *testing.T) {
	a := amount("1234.56", "USD")
	got := print(t, a)
	want := "1234.56 USD"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestAmountPointer(t *testing.T) {
	a := amountp("99.99", "EUR")
	got := print(t, a)
	want := "99.99 EUR"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestAmountWithCommaGrouping(t *testing.T) {
	a := amount("1234567.89", "USD")
	got := print(t, a, format.WithCommaGrouping(true))
	want := "1,234,567.89 USD"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestAmountNilPointer(t *testing.T) {
	var a *ast.Amount
	got := print(t, a)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestMetaValueRendering(t *testing.T) {
	tests := []struct {
		name string
		meta ast.Metadata
		want string
	}{
		{
			name: "string",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"key": {Kind: ast.MetaString, String: "value"},
			}},
			want: "2024-01-01 commodity USD\n  key: \"value\"\n",
		},
		{
			name: "account",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"account": {Kind: ast.MetaAccount, String: "Assets:Bank"},
			}},
			want: "2024-01-01 commodity USD\n  account: Assets:Bank\n",
		},
		{
			name: "currency",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"cur": {Kind: ast.MetaCurrency, String: "EUR"},
			}},
			want: "2024-01-01 commodity USD\n  cur: EUR\n",
		},
		{
			name: "date",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"when": {Kind: ast.MetaDate, Date: date("2024-06-15")},
			}},
			want: "2024-01-01 commodity USD\n  when: 2024-06-15\n",
		},
		{
			name: "tag",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"tag": {Kind: ast.MetaTag, String: "trip"},
			}},
			want: "2024-01-01 commodity USD\n  tag: #trip\n",
		},
		{
			name: "link",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"link": {Kind: ast.MetaLink, String: "doc-123"},
			}},
			want: "2024-01-01 commodity USD\n  link: ^doc-123\n",
		},
		{
			name: "number",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"rate": {Kind: ast.MetaNumber, Number: decimal("3.14")},
			}},
			want: "2024-01-01 commodity USD\n  rate: 3.14\n",
		},
		{
			name: "amount",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"price": {Kind: ast.MetaAmount, Amount: amount("42.00", "USD")},
			}},
			want: "2024-01-01 commodity USD\n  price: 42.00 USD\n",
		},
		{
			name: "bool true",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"flag": {Kind: ast.MetaBool, Bool: true},
			}},
			want: "2024-01-01 commodity USD\n  flag: TRUE\n",
		},
		{
			name: "bool false",
			meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"flag": {Kind: ast.MetaBool, Bool: false},
			}},
			want: "2024-01-01 commodity USD\n  flag: FALSE\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := print(t, &ast.Commodity{
				Date:     date("2024-01-01"),
				Currency: "USD",
				Meta:     tt.meta,
			})
			if got != tt.want {
				t.Errorf("Fprint(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestMetadataKeysSorted(t *testing.T) {
	got := print(t, &ast.Commodity{
		Date:     date("2024-01-01"),
		Currency: "USD",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"zebra": {Kind: ast.MetaString, String: "z"},
			"alpha": {Kind: ast.MetaString, String: "a"},
			"mid":   {Kind: ast.MetaString, String: "m"},
		}},
	})
	want := `2024-01-01 commodity USD
  alpha: "a"
  mid: "m"
  zebra: "z"
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAmountAlignmentDisabled(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "No align",
		Postings: []ast.Posting{
			{Account: "Expenses:Food", Amount: amountp("50.00", "USD")},
			{Account: "Assets:Bank"},
		},
	}, format.WithAlignAmounts(false))
	want := `2024-01-15 * "No align"
  Expenses:Food  50.00 USD
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnsupportedType(t *testing.T) {
	var buf bytes.Buffer
	err := printer.Fprint(&buf, 42)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestBlankLinesBetweenDirectives(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Include{Path: "a.beancount"},
		&ast.Include{Path: "b.beancount"},
	}
	got := print(t, dirs, format.WithBlankLinesBetweenDirectives(2), format.WithInsertBlankLinesBetweenDirectives(true))
	want := `include "a.beancount"


include "b.beancount"
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStringEscaping(t *testing.T) {
	got := print(t, &ast.Option{Key: "title", Value: "My \"Ledger\""})
	want := "option \"title\" \"My \\\"Ledger\\\"\"\n"
	if got != want {
		t.Errorf("Fprint() = %q, want %q", got, want)
	}
}

func TestStringQuoting(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    string
	}{
		{
			name:    "multiline",
			comment: "Line one\nline two",
			want:    "2024-01-01 note Assets:A \"Line one\nline two\"\n",
		},
		{
			name:    "tab",
			comment: "before\tafter",
			want:    "2024-01-01 note Assets:A \"before\tafter\"\n",
		},
		{
			name:    "carriage return",
			comment: "before\rafter",
			want:    "2024-01-01 note Assets:A \"before\rafter\"\n",
		},
		{
			name:    "backslash",
			comment: `path\to\file`,
			want:    "2024-01-01 note Assets:A \"path\\\\to\\\\file\"\n",
		},
		{
			name:    "double quote",
			comment: `say "hello"`,
			want:    "2024-01-01 note Assets:A \"say \\\"hello\\\"\"\n",
		},
		{
			name:    "accented",
			comment: "café résumé",
			want:    "2024-01-01 note Assets:A \"café résumé\"\n",
		},
		{
			name:    "combining character",
			comment: "e\u0301", // e + combining acute accent
			want:    "2024-01-01 note Assets:A \"e\u0301\"\n",
		},
		{
			name:    "CJK",
			comment: "日本語テスト",
			want:    "2024-01-01 note Assets:A \"日本語テスト\"\n",
		},
		{
			name:    "emoji",
			comment: "🎉 party",
			want:    "2024-01-01 note Assets:A \"🎉 party\"\n",
		},
		{
			name:    "mixed newline and special",
			comment: "café\n日本語",
			want:    "2024-01-01 note Assets:A \"café\n日本語\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := print(t, &ast.Note{
				Date:    date("2024-01-01"),
				Account: "Assets:A",
				Comment: tt.comment,
			})
			if got != tt.want {
				t.Errorf("Fprint(Note with comment %q) = %q, want %q", tt.comment, got, tt.want)
			}
		})
	}
}

func TestCommaGroupingInTransaction(t *testing.T) {
	got := print(t, &ast.Transaction{
		Date:      date("2024-01-15"),
		Flag:      '*',
		Narration: "Big purchase",
		Postings: []ast.Posting{
			{Account: "Expenses:Home", Amount: amountp("1234567.89", "USD")},
			{Account: "Assets:Bank"},
		},
	}, format.WithCommaGrouping(true))
	want := `2024-01-15 * "Big purchase"
  Expenses:Home                     1,234,567.89 USD
  Assets:Bank
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
