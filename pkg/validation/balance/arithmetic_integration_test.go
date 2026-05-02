package balance_test

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/syntax"
	"github.com/yugui/go-beancount/pkg/validation/balance"
)

// TestPlugin_ArithmeticPostingsBalance feeds a ledger whose postings use
// arithmetic expressions whose evaluation triggers apd's Rounded /
// Inexact informational flags (1000000000000/7 has a non-terminating
// decimal expansion, truncated at 34 digits). The two postings use
// equal-magnitude opposite-sign quotients, so once both are evaluated
// to the same 34-digit truncation they cancel exactly and the residual
// is zero. The point this test pins is therefore not a tolerance edge
// case but the contract at the boundary between layers: lowering must
// hand the balance plugin apd values it can sum without surfacing
// Rounded/Inexact as a fatal diagnostic.
//
// (Constructing two literal-only quotient postings whose residual is
// non-zero yet still fits within the inferred JPY tolerance is not
// feasible: any non-zero quotient residual at 34-digit precision sits
// at exponent -22, which is at least an order of magnitude above the
// inferred tolerance derived from that same exponent. The reachable
// regimes are "exact cancellation to zero" or "residual >> tolerance",
// and the former is what we use here.)
//
// This is the end-to-end check that protects against the regression
// where mathematically valid arithmetic expressions were rejected at the
// AST layer purely because the intermediate decimal exceeded apd's
// 34-digit precision budget.
func TestPlugin_ArithmeticPostingsBalance(t *testing.T) {
	src := "2024-01-01 * \"arithmetic residual\"\n" +
		"  Income:A    -1000000000000/7 JPY\n" +
		"  Expenses:B   1000000000000/7 JPY\n"

	cst := syntax.Parse(src)
	f := ast.Lower("test.beancount", cst)
	if len(f.Diagnostics) > 0 {
		t.Fatalf("lower emitted diagnostics on arithmetic postings: %v", f.Diagnostics)
	}
	if len(f.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(f.Directives))
	}
	if _, ok := f.Directives[0].(*ast.Transaction); !ok {
		t.Fatalf("directive is %T, want *ast.Transaction", f.Directives[0])
	}

	in := api.Input{Directives: seqOf(f.Directives)}
	res, err := balance.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("balance.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty (residual must be within JPY tolerance)", res.Diagnostics)
	}
}
