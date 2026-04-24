package ast_test

import (
	"path/filepath"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

func TestResolvePath_AbsolutePathReturnedUnchanged(t *testing.T) {
	got := ast.ResolvePath(nil, "/anywhere/main.bean", "/abs/doc.pdf")
	if got != "/abs/doc.pdf" {
		t.Errorf("got %q, want %q", got, "/abs/doc.pdf")
	}
}

func TestResolvePath_AnchorsToAbsoluteSpan(t *testing.T) {
	got := ast.ResolvePath(nil, "/inc/sub.bean", "doc.pdf")
	want := filepath.Join("/inc", "doc.pdf")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_AnchorsToLedgerRootWhenSpanIsRelative(t *testing.T) {
	ledger := &ast.Ledger{Files: []*ast.File{{Filename: "/root/main.bean"}}}
	got := ast.ResolvePath(ledger, "sub/include.bean", "doc.pdf")
	want := filepath.Join("/root/sub", "doc.pdf")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_AnchorsToLedgerRootWhenSpanEmpty(t *testing.T) {
	ledger := &ast.Ledger{Files: []*ast.File{{Filename: "/root/main.bean"}}}
	got := ast.ResolvePath(ledger, "", "doc.pdf")
	want := filepath.Join("/root", "doc.pdf")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_FallsBackToCwdWhenLedgerEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got := ast.ResolvePath(nil, "", "doc.pdf")
	want := filepath.Join(dir, "doc.pdf")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_FallsBackToCwdWhenLedgerHasNoFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got := ast.ResolvePath(&ast.Ledger{}, "", "doc.pdf")
	want := filepath.Join(dir, "doc.pdf")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_RelativeLedgerRootResolvedAgainstCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ledger := &ast.Ledger{Files: []*ast.File{{Filename: "main.bean"}}}
	got := ast.ResolvePath(ledger, "", "doc.pdf")
	want := filepath.Join(dir, "doc.pdf")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
