package ast_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
)

func TestLower_Empty(t *testing.T) {
	cst := syntax.Parse("")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 0 {
		t.Errorf("Lower: got %d directives, want 0", len(f.Directives))
	}
	if len(f.Diagnostics) != 0 {
		t.Errorf("Lower: got %d diagnostics, want 0", len(f.Diagnostics))
	}
	if f.Filename != "test.beancount" {
		t.Errorf("Lower: got filename %q, want %q", f.Filename, "test.beancount")
	}
}

func TestLower_SyntaxError(t *testing.T) {
	cst := syntax.Parse("this is not valid beancount\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) == 0 {
		t.Error("Lower: got 0 diagnostics, want at least 1 for syntax error")
	}
}

func TestLower_ValidDirectiveStubs(t *testing.T) {
	inputs := []struct {
		name  string
		input string
	}{
		{"pad", "2024-01-01 pad Assets:Bank Equity:Opening-Balances\n"},
		{"note", "2024-01-01 note Assets:Bank \"hello\"\n"},
		{"document", "2024-01-01 document Assets:Bank \"/path/to/doc\"\n"},
		{"price", "2024-01-01 price USD 1.2 EUR\n"},
		{"event", "2024-01-01 event \"location\" \"home\"\n"},
		{"query", "2024-01-01 query \"myquery\" \"SELECT *\"\n"},
		{"custom", "2024-01-01 custom \"budget\" Assets:Bank 100 USD\n"},
		{"transaction", "2024-01-01 * \"Payee\" \"Narration\"\n  Assets:Bank  100 USD\n  Expenses:Food\n"},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			cst := syntax.Parse(tc.input)
			f := ast.Lower("test.beancount", cst)
			// Stubs produce no directives, but should not panic.
			if len(f.Directives) != 0 {
				t.Errorf("Lower(%q): got %d directives, want 0", tc.name, len(f.Directives))
			}
		})
	}
}

func TestLower_Open(t *testing.T) {
	cst := syntax.Parse("2024-01-15 open Assets:US:BofA:Checking USD,EUR\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(open): got %d directives, want 1", len(f.Directives))
	}
	o, ok := f.Directives[0].(*ast.Open)
	if !ok {
		t.Fatalf("Lower(open): directive is %T, want *ast.Open", f.Directives[0])
	}
	if got := o.Date.Format("2006-01-02"); got != "2024-01-15" {
		t.Errorf("Lower(open): Date = %q, want %q", got, "2024-01-15")
	}
	if o.Account != "Assets:US:BofA:Checking" {
		t.Errorf("Lower(open): Account = %q, want %q", o.Account, "Assets:US:BofA:Checking")
	}
	if len(o.Currencies) != 2 || o.Currencies[0] != "USD" || o.Currencies[1] != "EUR" {
		t.Errorf("Lower(open): Currencies = %v, want %v", o.Currencies, []string{"USD", "EUR"})
	}
}

func TestLower_OpenWithBooking(t *testing.T) {
	cst := syntax.Parse("2024-01-15 open Assets:Bank USD \"STRICT\"\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(open-booking): got %d directives, want 1", len(f.Directives))
	}
	o, ok := f.Directives[0].(*ast.Open)
	if !ok {
		t.Fatalf("Lower(open-booking): directive is %T, want *ast.Open", f.Directives[0])
	}
	if o.Booking != "STRICT" {
		t.Errorf("Lower(open-booking): Booking = %q, want %q", o.Booking, "STRICT")
	}
	if len(o.Currencies) != 1 || o.Currencies[0] != "USD" {
		t.Errorf("Lower(open-booking): Currencies = %v, want %v", o.Currencies, []string{"USD"})
	}
}

func TestLower_OpenMinimal(t *testing.T) {
	cst := syntax.Parse("2024-01-15 open Expenses:Food\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(open-minimal): got %d directives, want 1", len(f.Directives))
	}
	o, ok := f.Directives[0].(*ast.Open)
	if !ok {
		t.Fatalf("Lower(open-minimal): directive is %T, want *ast.Open", f.Directives[0])
	}
	if len(o.Currencies) != 0 {
		t.Errorf("Lower(open-minimal): Currencies = %v, want empty", o.Currencies)
	}
	if o.Booking != "" {
		t.Errorf("Lower(open-minimal): Booking = %q, want empty", o.Booking)
	}
}

func TestLower_Close(t *testing.T) {
	cst := syntax.Parse("2024-06-30 close Assets:Bank\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(close): got %d directives, want 1", len(f.Directives))
	}
	c, ok := f.Directives[0].(*ast.Close)
	if !ok {
		t.Fatalf("Lower(close): directive is %T, want *ast.Close", f.Directives[0])
	}
	if got := c.Date.Format("2006-01-02"); got != "2024-06-30" {
		t.Errorf("Lower(close): Date = %q, want %q", got, "2024-06-30")
	}
	if c.Account != "Assets:Bank" {
		t.Errorf("Lower(close): Account = %q, want %q", c.Account, "Assets:Bank")
	}
}

func TestLower_OpenSlashDate(t *testing.T) {
	cst := syntax.Parse("2024/01/15 open Assets:Bank\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(open-slash-date): got %d directives, want 1", len(f.Directives))
	}
	o, ok := f.Directives[0].(*ast.Open)
	if !ok {
		t.Fatalf("Lower(open-slash-date): directive is %T, want *ast.Open", f.Directives[0])
	}
	if got := o.Date.Format("2006-01-02"); got != "2024-01-15" {
		t.Errorf("Lower(open-slash-date): Date = %q, want %q", got, "2024-01-15")
	}
}

func TestLower_Option(t *testing.T) {
	cst := syntax.Parse("option \"title\" \"My Ledger\"\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(option): got %d directives, want 1", len(f.Directives))
	}
	opt, ok := f.Directives[0].(*ast.Option)
	if !ok {
		t.Fatalf("Lower(option): directive is %T, want *ast.Option", f.Directives[0])
	}
	if opt.Key != "title" {
		t.Errorf("Lower(option): Key = %q, want %q", opt.Key, "title")
	}
	if opt.Value != "My Ledger" {
		t.Errorf("Lower(option): Value = %q, want %q", opt.Value, "My Ledger")
	}
	// Verify span is populated with non-zero offsets.
	if opt.Span.End.Offset == 0 {
		t.Errorf("Lower(option): Span.End.Offset = 0, want non-zero")
	}
}

func TestLower_Plugin(t *testing.T) {
	cst := syntax.Parse("plugin \"beancount.plugins.auto\" \"config\"\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(plugin): got %d directives, want 1", len(f.Directives))
	}
	p, ok := f.Directives[0].(*ast.Plugin)
	if !ok {
		t.Fatalf("Lower(plugin): directive is %T, want *ast.Plugin", f.Directives[0])
	}
	if p.Name != "beancount.plugins.auto" {
		t.Errorf("Lower(plugin): Name = %q, want %q", p.Name, "beancount.plugins.auto")
	}
	if p.Config != "config" {
		t.Errorf("Lower(plugin): Config = %q, want %q", p.Config, "config")
	}
	if p.Span.End.Offset == 0 {
		t.Errorf("Lower(plugin): Span.End.Offset = 0, want non-zero")
	}
}

func TestLower_PluginNoConfig(t *testing.T) {
	cst := syntax.Parse("plugin \"beancount.plugins.auto\"\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(plugin-no-config): got %d directives, want 1", len(f.Directives))
	}
	p, ok := f.Directives[0].(*ast.Plugin)
	if !ok {
		t.Fatalf("Lower(plugin-no-config): directive is %T, want *ast.Plugin", f.Directives[0])
	}
	if p.Name != "beancount.plugins.auto" {
		t.Errorf("Lower(plugin-no-config): Name = %q, want %q", p.Name, "beancount.plugins.auto")
	}
	if p.Config != "" {
		t.Errorf("Lower(plugin-no-config): Config = %q, want empty", p.Config)
	}
}

func TestLower_Include(t *testing.T) {
	cst := syntax.Parse("include \"other.beancount\"\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(include): got %d directives, want 1", len(f.Directives))
	}
	inc, ok := f.Directives[0].(*ast.Include)
	if !ok {
		t.Fatalf("Lower(include): directive is %T, want *ast.Include", f.Directives[0])
	}
	if inc.Path != "other.beancount" {
		t.Errorf("Lower(include): Path = %q, want %q", inc.Path, "other.beancount")
	}
	if inc.Span.End.Offset == 0 {
		t.Errorf("Lower(include): Span.End.Offset = 0, want non-zero")
	}
}

func TestLower_CSTErrorsConvertedToDiagnostics(t *testing.T) {
	// Construct a CST with explicit errors to verify conversion.
	cst := &syntax.File{
		Root: &syntax.Node{Kind: syntax.FileNode},
		Errors: []syntax.Error{
			{Pos: 10, Msg: "unexpected token"},
			{Pos: 20, Msg: "missing newline"},
		},
	}
	f := ast.Lower("errors.beancount", cst)
	if got := len(f.Diagnostics); got != 2 {
		t.Fatalf("Lower: got %d diagnostics, want 2", got)
	}
	for i, diag := range f.Diagnostics {
		if diag.Severity != ast.Error {
			t.Errorf("Lower: diagnostic[%d]: got severity %d, want Error", i, diag.Severity)
		}
		if diag.Span.Start.Filename != "errors.beancount" {
			t.Errorf("Lower: diagnostic[%d]: got filename %q, want %q", i, diag.Span.Start.Filename, "errors.beancount")
		}
	}
	if got := f.Diagnostics[0].Span.Start.Offset; got != 10 {
		t.Errorf("Lower: diagnostic[0]: got offset %d, want 10", got)
	}
	if got := f.Diagnostics[0].Message; got != "unexpected token" {
		t.Errorf("Lower: diagnostic[0]: got message %q, want %q", got, "unexpected token")
	}
	if got := f.Diagnostics[1].Span.Start.Offset; got != 20 {
		t.Errorf("Lower: diagnostic[1]: got offset %d, want 20", got)
	}
	if got := f.Diagnostics[1].Message; got != "missing newline" {
		t.Errorf("Lower: diagnostic[1]: got message %q, want %q", got, "missing newline")
	}
}

func TestLower_NilRoot(t *testing.T) {
	cst := &syntax.File{Root: nil}
	f := ast.Lower("nil.beancount", cst)
	if len(f.Directives) != 0 {
		t.Errorf("Lower: got %d directives, want 0", len(f.Directives))
	}
	if len(f.Diagnostics) != 0 {
		t.Errorf("Lower: got %d diagnostics, want 0", len(f.Diagnostics))
	}
}

func TestLower_Commodity(t *testing.T) {
	cst := syntax.Parse("2024-01-01 commodity USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(commodity): got %d directives, want 1", len(f.Directives))
	}
	c, ok := f.Directives[0].(*ast.Commodity)
	if !ok {
		t.Fatalf("Lower(commodity): directive is %T, want *ast.Commodity", f.Directives[0])
	}
	if got := c.Date.Format("2006-01-02"); got != "2024-01-01" {
		t.Errorf("Lower(commodity): Date = %q, want %q", got, "2024-01-01")
	}
	if c.Currency != "USD" {
		t.Errorf("Lower(commodity): Currency = %q, want %q", c.Currency, "USD")
	}
}

func TestLower_Balance(t *testing.T) {
	cst := syntax.Parse("2024-01-01 balance Assets:Bank 1234.56 USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("Lower(balance): unexpected diagnostics: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(balance): got %d directives, want 1", len(f.Directives))
	}
	b, ok := f.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("Lower(balance): directive is %T, want *ast.Balance", f.Directives[0])
	}
	if got := b.Date.Format("2006-01-02"); got != "2024-01-01" {
		t.Errorf("Lower(balance): Date = %q, want %q", got, "2024-01-01")
	}
	if b.Account != "Assets:Bank" {
		t.Errorf("Lower(balance): Account = %q, want %q", b.Account, "Assets:Bank")
	}
	if got := b.Amount.Number.String(); got != "1234.56" {
		t.Errorf("Lower(balance): Amount.Number = %q, want %q", got, "1234.56")
	}
	if b.Amount.Currency != "USD" {
		t.Errorf("Lower(balance): Amount.Currency = %q, want %q", b.Amount.Currency, "USD")
	}
	if b.Tolerance != nil {
		t.Errorf("Lower(balance): Tolerance = %v, want nil", b.Tolerance)
	}
}

func TestLower_BalanceWithCommas(t *testing.T) {
	cst := syntax.Parse("2024-01-01 balance Assets:Bank 1,234.56 USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("Lower(balance-commas): unexpected diagnostics: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(balance-commas): got %d directives, want 1", len(f.Directives))
	}
	b, ok := f.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("Lower(balance-commas): directive is %T, want *ast.Balance", f.Directives[0])
	}
	if got := b.Amount.Number.String(); got != "1234.56" {
		t.Errorf("Lower(balance-commas): Amount.Number = %q, want %q", got, "1234.56")
	}
}

func TestLower_BalanceWithExpr(t *testing.T) {
	cst := syntax.Parse("2024-01-01 balance Assets:Bank (100 + 200) USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("Lower(balance-expr): unexpected diagnostics: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(balance-expr): got %d directives, want 1", len(f.Directives))
	}
	b, ok := f.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("Lower(balance-expr): directive is %T, want *ast.Balance", f.Directives[0])
	}
	if got := b.Amount.Number.String(); got != "300" {
		t.Errorf("Lower(balance-expr): Amount.Number = %q, want %q", got, "300")
	}
}

func TestLower_BalanceNegative(t *testing.T) {
	cst := syntax.Parse("2024-01-01 balance Assets:Bank -500 USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("Lower(balance-negative): unexpected diagnostics: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(balance-negative): got %d directives, want 1", len(f.Directives))
	}
	b, ok := f.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("Lower(balance-negative): directive is %T, want *ast.Balance", f.Directives[0])
	}
	if got := b.Amount.Number.String(); got != "-500" {
		t.Errorf("Lower(balance-negative): Amount.Number = %q, want %q", got, "-500")
	}
}

func TestLower_BalanceArithmetic(t *testing.T) {
	cst := syntax.Parse("2024-01-01 balance Assets:Bank 100 * 3 + 50 USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("Lower(balance-arith): unexpected diagnostics: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(balance-arith): got %d directives, want 1", len(f.Directives))
	}
	b, ok := f.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("Lower(balance-arith): directive is %T, want *ast.Balance", f.Directives[0])
	}
	if got := b.Amount.Number.String(); got != "350" {
		t.Errorf("Lower(balance-arith): Amount.Number = %q, want %q", got, "350")
	}
}

func TestLower_BalanceWithTolerance(t *testing.T) {
	cst := syntax.Parse("2024-01-01 balance Assets:Bank 1000 USD ~ 5 USD\n")
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("Lower(balance-tolerance): unexpected diagnostics: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("Lower(balance-tolerance): got %d directives, want 1", len(f.Directives))
	}
	b, ok := f.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("Lower(balance-tolerance): directive is %T, want *ast.Balance", f.Directives[0])
	}
	if got := b.Amount.Number.String(); got != "1000" {
		t.Errorf("Lower(balance-tolerance): Amount.Number = %q, want %q", got, "1000")
	}
	if b.Amount.Currency != "USD" {
		t.Errorf("Lower(balance-tolerance): Amount.Currency = %q, want %q", b.Amount.Currency, "USD")
	}
	if b.Tolerance == nil {
		t.Fatal("Lower(balance-tolerance): Tolerance is nil, want non-nil")
	}
	if got := b.Tolerance.Number.String(); got != "5" {
		t.Errorf("Lower(balance-tolerance): Tolerance.Number = %q, want %q", got, "5")
	}
	if b.Tolerance.Currency != "USD" {
		t.Errorf("Lower(balance-tolerance): Tolerance.Currency = %q, want %q", b.Tolerance.Currency, "USD")
	}
}
