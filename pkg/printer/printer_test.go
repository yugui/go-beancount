package printer_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
	"github.com/yugui/go-beancount/pkg/printer"
)

func decimal(s string) apd.Decimal {
	d, _, _ := apd.NewFromString(s)
	return *d
}

func amount(num string, cur string) ast.Amount {
	return ast.Amount{Number: decimal(num), Currency: cur}
}

func date(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
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
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlugin(t *testing.T) {
	t.Run("with config", func(t *testing.T) {
		got := print(t, &ast.Plugin{Name: "beancount.plugins.auto", Config: "config_val"})
		want := "plugin \"beancount.plugins.auto\" \"config_val\"\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("without config", func(t *testing.T) {
		got := print(t, &ast.Plugin{Name: "beancount.plugins.auto"})
		want := "plugin \"beancount.plugins.auto\"\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestInclude(t *testing.T) {
	got := print(t, &ast.Include{Path: "other.beancount"})
	want := "include \"other.beancount\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("with currencies and booking", func(t *testing.T) {
		got := print(t, &ast.Open{
			Date:       date("2024-01-01"),
			Account:    "Assets:Bank",
			Currencies: []string{"USD", "EUR"},
			Booking:    "STRICT",
		})
		want := "2024-01-01 open Assets:Bank USD,EUR \"STRICT\"\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
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
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestClose(t *testing.T) {
	got := print(t, &ast.Close{Date: date("2024-06-01"), Account: "Assets:Old"})
	want := "2024-06-01 close Assets:Old\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCommodity(t *testing.T) {
	got := print(t, &ast.Commodity{Date: date("2024-01-01"), Currency: "BTC"})
	want := "2024-01-01 commodity BTC\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("with tolerance", func(t *testing.T) {
		got := print(t, &ast.Balance{
			Date:      date("2024-01-15"),
			Account:   "Assets:Bank",
			Amount:    amount("1234.56", "USD"),
			Tolerance: amountp("0.01", "USD"),
		})
		want := "2024-01-15 balance Assets:Bank 1234.56 USD ~ 0.01 USD\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
		t.Errorf("got %q, want %q", got, want)
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
	want := "" +
		"2024-01-15 * \"Grocery Store\" \"Weekly shopping\"\n" +
		"  Expenses:Food                            50.00 USD\n" +
		"  Assets:Bank\n"
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
	want := "" +
		"2024-01-15 * \"Transfer\"\n" +
		"  Assets:Savings                          100.00 USD\n" +
		"  Assets:Checking\n"
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
	want := "" +
		"2024-01-15 ! \"Pending\" #trip-2024 ^invoice-123\n" +
		"  Expenses:Travel                         200.00 USD\n" +
		"  Liabilities:Credit\n"
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
	want := "" +
		"2024-01-15 * \"Mixed\"\n" +
		"  ! Expenses:Food                          30.00 USD\n" +
		"  Assets:Bank\n"
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
	want := "" +
		"2024-01-15 * \"Lunch\"\n" +
		"  source: \"receipt\"\n" +
		"  Expenses:Food                            15.00 USD\n" +
		"    category: \"lunch\"\n" +
		"  Assets:Cash\n"
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
					Amount: amountp("150.00", "USD"),
					Date:   datep("2024-01-15"),
					Label:  "lot1",
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := "" +
		"2024-01-15 * \"Buy stock\"\n" +
		"  Assets:Brokerage                           10 AAPL {150.00 USD, 2024-01-15, \"lot1\"}\n" +
		"  Assets:Bank\n"
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
					Amount:  amountp("1500.00", "USD"),
					IsTotal: true,
				},
			},
			{Account: "Assets:Bank"},
		},
	})
	want := "" +
		"2024-01-15 * \"Buy stock total\"\n" +
		"  Assets:Brokerage                           10 AAPL {{1500.00 USD}}\n" +
		"  Assets:Bank\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
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
	want := "" +
		"2024-01-15 * \"Sell stock\"\n" +
		"  Assets:Brokerage                          -10 AAPL {}\n" +
		"  Income:Gains\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
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
	want := "" +
		"2024-01-15 * \"Exchange\"\n" +
		"  Assets:EUR                                 100 EUR @ 1.10 USD\n" +
		"  Assets:USD\n"
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
	want := "" +
		"2024-01-15 * \"Exchange total\"\n" +
		"  Assets:EUR                                 100 EUR @@ 110.00 USD\n" +
		"  Assets:USD\n"
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
	got := print(t, &f)
	want := "" +
		"option \"title\" \"Test\"\n" +
		"\n" +
		"2024-01-01 open Assets:Bank\n" +
		"\n" +
		"2024-12-31 close Assets:Bank\n"
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
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLedger(t *testing.T) {
	l := ast.Ledger{
		Directives: []ast.Directive{
			&ast.Option{Key: "title", Value: "Ledger"},
			&ast.Open{Date: date("2024-01-01"), Account: "Assets:Cash"},
		},
	}
	got := print(t, &l)
	want := "" +
		"option \"title\" \"Ledger\"\n" +
		"\n" +
		"2024-01-01 open Assets:Cash\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDirectiveSlice(t *testing.T) {
	dirs := []ast.Directive{
		&ast.Include{Path: "a.beancount"},
		&ast.Include{Path: "b.beancount"},
	}
	got := print(t, dirs)
	want := "" +
		"include \"a.beancount\"\n" +
		"\n" +
		"include \"b.beancount\"\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAmountDirect(t *testing.T) {
	a := amount("1234.56", "USD")
	got := print(t, a)
	want := "1234.56 USD"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAmountPointer(t *testing.T) {
	a := amountp("99.99", "EUR")
	got := print(t, a)
	want := "99.99 EUR"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAmountWithCommaGrouping(t *testing.T) {
	a := amount("1234567.89", "USD")
	got := print(t, a, format.WithCommaGrouping(true))
	want := "1,234,567.89 USD"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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
				t.Errorf("got %q, want %q", got, tt.want)
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
	want := "" +
		"2024-01-01 commodity USD\n" +
		"  alpha: \"a\"\n" +
		"  mid: \"m\"\n" +
		"  zebra: \"z\"\n"
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
	want := "" +
		"2024-01-15 * \"No align\"\n" +
		"  Expenses:Food  50.00 USD\n" +
		"  Assets:Bank\n"
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
	got := print(t, dirs, format.WithBlankLinesBetweenDirectives(2))
	want := "" +
		"include \"a.beancount\"\n" +
		"\n" +
		"\n" +
		"include \"b.beancount\"\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStringEscaping(t *testing.T) {
	got := print(t, &ast.Option{Key: "title", Value: "My \"Ledger\""})
	want := "option \"title\" \"My \\\"Ledger\\\"\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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
	want := "" +
		"2024-01-15 * \"Big purchase\"\n" +
		"  Expenses:Home                     1,234,567.89 USD\n" +
		"  Assets:Bank\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
