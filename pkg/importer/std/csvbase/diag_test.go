package csvbase_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
)

func TestErrorDiag(t *testing.T) {
	got := csvbase.ErrorDiag("my-code", "/a/b.csv", 42, "something broke")
	want := ast.Diagnostic{
		Code: "my-code",
		Span: ast.Span{
			Start: ast.Position{Filename: "/a/b.csv", Line: 42},
		},
		Message:  "something broke",
		Severity: ast.Error,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ErrorDiag mismatch (-want +got):\n%s", diff)
	}
}

func TestWarnDiag(t *testing.T) {
	got := csvbase.WarnDiag("warn-code", "/x.tsv", 7, "advisory")
	want := ast.Diagnostic{
		Code: "warn-code",
		Span: ast.Span{
			Start: ast.Position{Filename: "/x.tsv", Line: 7},
		},
		Message:  "advisory",
		Severity: ast.Warning,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("WarnDiag mismatch (-want +got):\n%s", diff)
	}
}
