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
		{"option", "option \"title\" \"Test\"\n"},
		{"plugin", "plugin \"beancount.plugins.auto\"\n"},
		{"include", "include \"other.beancount\"\n"},
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
