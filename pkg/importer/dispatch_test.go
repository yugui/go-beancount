package importer

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// fakeRegistry implements Registry without touching the global state.
type fakeRegistry struct {
	imps []Importer
}

func (r *fakeRegistry) Lookup(name string) (Importer, bool) {
	for _, imp := range r.imps {
		if imp.Name() == name {
			return imp, true
		}
	}
	return nil, false
}

func (r *fakeRegistry) Names() []string {
	names := make([]string, len(r.imps))
	for i, imp := range r.imps {
		names[i] = imp.Name()
	}
	return names
}

func TestDispatch_FirstMatchWins(t *testing.T) {
	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{name: "alpha", identifyFn: func(Input) bool { return true }},
			&fakeImporter{name: "beta", identifyFn: func(Input) bool { return true }},
		},
	}
	in := newTestInput("test.csv", "")

	got, ok, diags := Dispatch(context.Background(), reg, in)
	if !ok {
		t.Fatal("Dispatch returned ok=false, want true")
	}
	if got.Name() != "alpha" {
		t.Errorf("Dispatch returned %q, want %q", got.Name(), "alpha")
	}
	if len(diags) != 0 {
		t.Errorf("Dispatch returned unexpected diagnostics: %v", diags)
	}
}

func TestDispatch_DeclarationOrderMatters(t *testing.T) {
	// Declaration order: "zzz" first, then "bbb". Both identify; "zzz" must win.
	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{name: "zzz", identifyFn: func(Input) bool { return true }},
			&fakeImporter{name: "bbb", identifyFn: func(Input) bool { return true }},
			&fakeImporter{name: "aaa", identifyFn: func(Input) bool { return false }},
		},
	}
	in := newTestInput("data.csv", "")

	got, ok, _ := Dispatch(context.Background(), reg, in)
	if !ok {
		t.Fatal("Dispatch returned ok=false")
	}
	if got.Name() != "zzz" {
		t.Errorf("Dispatch returned %q, want %q (declaration order)", got.Name(), "zzz")
	}
}

func TestDispatch_MapRegistryDeclarationOrder(t *testing.T) {
	// Declared as zzz/bbb/aaa; lex order would be aaa/bbb/zzz.
	// zzz and aaa both identify; Dispatch must pick zzz (declaration order).
	reg, err := NewRegistry([]Importer{
		&fakeImporter{name: "zzz", identifyFn: func(Input) bool { return true }},
		&fakeImporter{name: "bbb", identifyFn: func(Input) bool { return false }},
		&fakeImporter{name: "aaa", identifyFn: func(Input) bool { return true }},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := newTestInput("data.csv", "")

	got, ok, _ := Dispatch(context.Background(), reg, in)
	if !ok {
		t.Fatal("Dispatch returned ok=false")
	}
	if got.Name() != "zzz" {
		t.Errorf("Dispatch returned %q, want %q (declaration order)", got.Name(), "zzz")
	}
}

func TestDispatch_NoMatch(t *testing.T) {
	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{name: "csv", identifyFn: func(Input) bool { return false }},
		},
	}
	in := newTestInput("data.xlsx", "")

	got, ok, diags := Dispatch(context.Background(), reg, in)
	if ok {
		t.Errorf("Dispatch returned ok=true with %v", got)
	}
	if got != nil {
		t.Errorf("Dispatch returned non-nil importer on no-match")
	}
	if len(diags) != 1 {
		t.Fatalf("Dispatch returned %d diagnostics, want 1", len(diags))
	}
	d := diags[0]
	if d.Code != DiagImporterNone {
		t.Errorf("diag.Code = %q, want %q", d.Code, DiagImporterNone)
	}
	if d.Severity != ast.Error {
		t.Errorf("diag.Severity = %v, want Error", d.Severity)
	}
	if d.Span.Start.Filename != in.Path {
		t.Errorf("diag.Span.Start.Filename = %q, want %q", d.Span.Start.Filename, in.Path)
	}
}

func TestDispatch_EmptyRegistry(t *testing.T) {
	reg := &fakeRegistry{}
	in := newTestInput("test.csv", "")

	_, ok, diags := Dispatch(context.Background(), reg, in)
	if ok {
		t.Error("Dispatch on empty registry returned ok=true")
	}
	if len(diags) != 1 {
		t.Fatalf("Dispatch returned %d diagnostics, want 1; got %v", len(diags), diags)
	}
	if diags[0].Code != DiagImporterNone {
		t.Errorf("diags[0].Code = %q, want %q; full diags: %v", diags[0].Code, DiagImporterNone, diags)
	}
}

func TestDispatch_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{name: "csv", identifyFn: func(Input) bool {
				t.Error("Identify called on cancelled context")
				return true
			}},
		},
	}
	in := newTestInput("test.csv", "")

	got, ok, diags := Dispatch(ctx, reg, in)
	if ok {
		t.Errorf("Dispatch returned ok=true on cancelled ctx, want false; got %v", got)
	}
	if len(diags) != 0 {
		t.Errorf("Dispatch returned diagnostics on cancellation, want none; got %v", diags)
	}
}
