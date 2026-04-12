package validation

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestCheckEmptyLedger(t *testing.T) {
	errs := Check(&ast.Ledger{})
	if len(errs) != 0 {
		t.Errorf("Check(empty) returned %d errors, want 0: %v", len(errs), errs)
	}
}

func TestErrorString(t *testing.T) {
	e := Error{
		Code: CodeAccountNotOpen,
		Span: ast.Span{
			Start: ast.Position{Filename: "foo.beancount", Line: 12, Column: 3},
		},
		Message: "account not open",
	}
	got := e.Error()
	if !strings.Contains(got, "foo.beancount") || !strings.Contains(got, "12") || !strings.Contains(got, "account not open") {
		t.Errorf("Error() = %q; want it to contain %q, %q, and %q", got, "foo.beancount", "12", "account not open")
	}

	bare := Error{Message: "bare message"}
	if bare.Error() != "bare message" {
		t.Errorf("Error() = %q, want %q", bare.Error(), "bare message")
	}
}
