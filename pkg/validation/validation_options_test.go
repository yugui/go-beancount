package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

func TestOptionsCollectedBeforeWalk(t *testing.T) {
	ledger := &ast.Ledger{
		Directives: []ast.Directive{
			&ast.Option{Key: "operating_currency", Value: "USD"},
			&ast.Option{Key: "operating_currency", Value: "JPY"},
			&ast.Option{Key: "no_such_option", Value: "ignored"},
		},
	}
	c := newChecker(ledger)
	errs := c.run()
	if len(errs) != 0 {
		t.Fatalf("TestOptionsCollectedBeforeWalk: Check returned %d errors, want 0: %v", len(errs), errs)
	}
	got := c.options.StringList("operating_currency")
	want := []string{"USD", "JPY"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("TestOptionsCollectedBeforeWalk: StringList mismatch (-want +got):\n%s", diff)
	}
}

func TestOptionsInvalidValueEmitsError(t *testing.T) {
	ledger := &ast.Ledger{
		Directives: []ast.Directive{
			&ast.Option{
				Span:  ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 3, Column: 1}},
				Key:   "operating_currency",
				Value: "   ",
			},
		},
	}
	errs := Check(ledger)
	if len(errs) != 1 {
		t.Fatalf("TestOptionsInvalidValueEmitsError: Check returned %d errors, want 1: %v", len(errs), errs)
	}
	if errs[0].Code != CodeInvalidOption {
		t.Errorf("TestOptionsInvalidValueEmitsError: Code = %v, want CodeInvalidOption", errs[0].Code)
	}
}
