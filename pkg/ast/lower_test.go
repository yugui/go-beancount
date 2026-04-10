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
		{"open", "2024-01-01 open Assets:Bank USD\n"},
		{"close", "2024-01-01 close Assets:Bank\n"},
		{"commodity", "2024-01-01 commodity USD\n"},
		{"balance", "2024-01-01 balance Assets:Bank 100 USD\n"},
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
