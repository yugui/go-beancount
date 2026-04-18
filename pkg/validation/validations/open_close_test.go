package validations

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

func TestOpenClose_EmptyBuildResult(t *testing.T) {
	v := newOpenClose(accountstate.BuildResult{})
	if got := v.ProcessEntry(nil); got != nil {
		t.Errorf("ProcessEntry(nil) = %v, want nil", got)
	}
	if got := v.Finish(); got != nil {
		t.Errorf("Finish() = %v, want nil (no duplicate opens)", got)
	}
}

func TestOpenClose_Name(t *testing.T) {
	v := newOpenClose(accountstate.BuildResult{})
	if got, want := v.Name(), "open_close"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestOpenClose_SingleDuplicateOpen(t *testing.T) {
	dupSpan := ast.Span{
		Start: ast.Position{Filename: "ledger.beancount", Line: 2, Column: 1, Offset: 42},
		End:   ast.Position{Filename: "ledger.beancount", Line: 2, Column: 30, Offset: 71},
	}
	dup := &ast.Open{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    dupSpan,
	}
	v := newOpenClose(accountstate.BuildResult{
		DuplicateOpens: []*ast.Open{dup},
	})

	// ProcessEntry is a no-op; it must never emit.
	for _, d := range []ast.Directive{dup} {
		if got := v.ProcessEntry(d); got != nil {
			t.Errorf("ProcessEntry(%T) = %v, want nil", d, got)
		}
	}

	errs := v.Finish()
	if len(errs) != 1 {
		t.Fatalf("Finish(): got %d errors, want 1; errs = %v", len(errs), errs)
	}
	e := errs[0]
	if e.Code != string(validation.CodeDuplicateOpen) {
		t.Errorf("Code = %q, want %q", e.Code, validation.CodeDuplicateOpen)
	}
	if e.Span != dupSpan {
		t.Errorf("Span = %#v, want %#v", e.Span, dupSpan)
	}
	if want := `account "Assets:Cash" already opened`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestOpenClose_MultipleDuplicateOpens(t *testing.T) {
	d1 := &ast.Open{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 2, Offset: 10}},
	}
	d2 := &ast.Open{
		Date:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 3, Offset: 20}},
	}
	d3 := &ast.Open{
		Date:    time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
		Account: "Liabilities:CC",
		Span:    ast.Span{Start: ast.Position{Line: 4, Offset: 30}},
	}

	v := newOpenClose(accountstate.BuildResult{
		DuplicateOpens: []*ast.Open{d1, d2, d3},
	})
	errs := v.Finish()
	if len(errs) != 3 {
		t.Fatalf("Finish(): got %d errors, want 3; errs = %v", len(errs), errs)
	}

	wantAccounts := []ast.Account{"Assets:Cash", "Assets:Cash", "Liabilities:CC"}
	for i, e := range errs {
		if e.Code != string(validation.CodeDuplicateOpen) {
			t.Errorf("errs[%d].Code = %q, want %q", i, e.Code, validation.CodeDuplicateOpen)
		}
		want := `account "` + string(wantAccounts[i]) + `" already opened`
		if e.Message != want {
			t.Errorf("errs[%d].Message = %q, want %q", i, e.Message, want)
		}
	}
}
