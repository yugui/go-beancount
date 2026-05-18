package importer

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

func TestApply_Success(t *testing.T) {
	directive := &ast.Transaction{}
	diag := ast.Diagnostic{Code: "row-warning", Severity: ast.Warning, Message: "bad row"}

	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{
				name:       "csv",
				identifyFn: func(Input) bool { return true },
				extractFn: func(Input) (Output, error) {
					return Output{
						Directives:  []ast.Directive{directive},
						Diagnostics: []ast.Diagnostic{diag},
					}, nil
				},
			},
		},
	}
	in := newTestInput("test.csv", "")

	out, err := Apply(context.Background(), reg, in)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if diff := cmp.Diff([]ast.Directive{directive}, out.Directives); diff != "" {
		t.Errorf("Apply Directives mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]ast.Diagnostic{diag}, out.Diagnostics); diff != "" {
		t.Errorf("Apply Diagnostics mismatch (-want +got):\n%s", diff)
	}
}

func TestApply_NoMatch(t *testing.T) {
	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{name: "csv", identifyFn: func(Input) bool { return false }},
		},
	}
	in := newTestInput("data.xlsx", "")

	out, err := Apply(context.Background(), reg, in)
	if err != nil {
		t.Fatalf("Apply returned error on no-match: %v", err)
	}
	if len(out.Directives) != 0 {
		t.Errorf("Apply Directives = %v, want empty", out.Directives)
	}
	if len(out.Diagnostics) == 0 {
		t.Fatal("Apply returned no diagnostics on no-match")
	}
	if out.Diagnostics[0].Code != DiagImporterNone {
		t.Errorf("diag.Code = %q, want %q", out.Diagnostics[0].Code, DiagImporterNone)
	}
}

func TestApply_ExtractError(t *testing.T) {
	extractErr := errors.New("I/O failure")
	warnDiag := ast.Diagnostic{Code: "partial", Severity: ast.Warning, Message: "partial"}

	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{
				name:       "csv",
				identifyFn: func(Input) bool { return true },
				extractFn: func(Input) (Output, error) {
					return Output{Diagnostics: []ast.Diagnostic{warnDiag}}, extractErr
				},
			},
		},
	}
	in := newTestInput("test.csv", "")

	out, err := Apply(context.Background(), reg, in)
	if !errors.Is(err, extractErr) {
		t.Errorf("Apply error = %v, want %v", err, extractErr)
	}
	if diff := cmp.Diff([]ast.Diagnostic{warnDiag}, out.Diagnostics); diff != "" {
		t.Errorf("Apply Diagnostics mismatch (-want +got):\n%s", diff)
	}
	if len(out.Directives) != 0 {
		t.Errorf("Apply Directives = %v, want nil on error", out.Directives)
	}
}

func TestApply_DiagnosticsOrder(t *testing.T) {
	d1 := ast.Diagnostic{Code: "d1", Message: "first"}
	d2 := ast.Diagnostic{Code: "d2", Message: "second"}

	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{
				name:       "csv",
				identifyFn: func(Input) bool { return true },
				extractFn: func(Input) (Output, error) {
					return Output{Diagnostics: []ast.Diagnostic{d1, d2}}, nil
				},
			},
		},
	}
	in := newTestInput("test.csv", "")

	out, err := Apply(context.Background(), reg, in)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic{d1, d2}, out.Diagnostics); diff != "" {
		t.Errorf("Apply Diagnostics mismatch (-want +got):\n%s", diff)
	}
}

func TestApply_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{name: "csv", identifyFn: func(Input) bool { return true }},
		},
	}
	in := newTestInput("test.csv", "")

	_, err := Apply(ctx, reg, in)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Apply with cancelled ctx error = %v, want context.Canceled", err)
	}
}

func TestApply_DoesNotCallConfigure(t *testing.T) {
	ci := &configurableImporter{
		fakeImporter: fakeImporter{
			name:       "csv",
			identifyFn: func(Input) bool { return true },
			extractFn:  func(Input) (Output, error) { return Output{}, nil },
		},
	}

	reg := &fakeRegistry{imps: []Importer{ci}}
	if _, err := Apply(context.Background(), reg, newTestInput("", "")); err != nil {
		t.Fatal(err)
	}
	if ci.configureCalled {
		t.Error("Apply called Configure; it must not")
	}
}

func TestApply_StreamingImporterUsesExtract(t *testing.T) {
	extractCalled := false
	streamExtractCalled := false

	si := &streamingImporter{
		fakeImporter: fakeImporter{
			name:       "csv",
			identifyFn: func(Input) bool { return true },
			extractFn: func(Input) (Output, error) {
				extractCalled = true
				return Output{}, nil
			},
		},
		streamExtractFn: func(_ context.Context, _ Input) iter.Seq2[ast.Directive, error] {
			streamExtractCalled = true
			return func(yield func(ast.Directive, error) bool) {}
		},
	}

	reg := &fakeRegistry{imps: []Importer{si}}
	if _, err := Apply(context.Background(), reg, newTestInput("", "")); err != nil {
		t.Fatal(err)
	}
	if !extractCalled {
		t.Error("Apply did not call Extract on a Streaming importer")
	}
	if streamExtractCalled {
		t.Error("Apply called StreamExtract; it must use Extract in ABI v1")
	}
}

func TestApply_EmptyOutputHasNilDiagnostics(t *testing.T) {
	reg := &fakeRegistry{
		imps: []Importer{
			&fakeImporter{
				name:       "csv",
				identifyFn: func(Input) bool { return true },
				extractFn:  func(Input) (Output, error) { return Output{}, nil },
			},
		},
	}

	out, err := Apply(context.Background(), reg, newTestInput("", ""))
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if out.Diagnostics != nil {
		t.Errorf("Apply Diagnostics = %v, want nil when both sides produce none", out.Diagnostics)
	}
}

func TestOptionalInterface_ConfigurableAssertion(t *testing.T) {
	withCleanRegistry(t)

	ci := &configurableImporter{
		fakeImporter: fakeImporter{name: "cfg"},
	}
	Register("cfg", ci)

	imp, ok := Lookup("cfg")
	if !ok {
		t.Fatal("Lookup returned ok=false")
	}
	_, ok = imp.(Configurable)
	if !ok {
		t.Error("registered configurableImporter does not satisfy Configurable via type assertion")
	}
}

func TestOptionalInterface_StreamingAssertion(t *testing.T) {
	withCleanRegistry(t)

	directive := &ast.Transaction{}
	streamErr := errors.New("stream error")

	si := &streamingImporter{
		fakeImporter: fakeImporter{name: "stream"},
		streamExtractFn: func(_ context.Context, _ Input) iter.Seq2[ast.Directive, error] {
			return func(yield func(ast.Directive, error) bool) {
				if !yield(directive, nil) {
					return
				}
				yield(nil, streamErr)
			}
		},
	}
	Register("stream", si)

	imp, ok := Lookup("stream")
	if !ok {
		t.Fatal("Lookup returned ok=false")
	}
	streaming, ok := imp.(Streaming)
	if !ok {
		t.Fatal("registered streamingImporter does not satisfy Streaming via type assertion")
	}

	var directives []ast.Directive
	var errs []error
	for d, err := range streaming.StreamExtract(context.Background(), newTestInput("", "")) {
		if err != nil {
			errs = append(errs, err)
		} else {
			directives = append(directives, d)
		}
	}
	if len(directives) != 1 || directives[0] != directive {
		t.Errorf("StreamExtract directives = %v, want [%v]", directives, directive)
	}
	if len(errs) != 1 || !errors.Is(errs[0], streamErr) {
		t.Errorf("StreamExtract errors = %v, want [%v]", errs, streamErr)
	}
}

func TestOptionalInterface_StreamDiagnoserAssertion(t *testing.T) {
	withCleanRegistry(t)

	wantDiag := ast.Diagnostic{Code: "stream-diag", Severity: ast.Warning, Message: "test"}
	sdi := &streamDiagnoserImporter{
		streamingImporter: streamingImporter{
			fakeImporter: fakeImporter{name: "streamdiag"},
		},
		diags: []ast.Diagnostic{wantDiag},
	}
	Register("streamdiag", sdi)

	imp, ok := Lookup("streamdiag")
	if !ok {
		t.Fatal("Lookup returned ok=false")
	}
	streaming, ok := imp.(Streaming)
	if !ok {
		t.Fatal("streamDiagnoserImporter does not satisfy Streaming")
	}
	diagnoser, ok := imp.(StreamDiagnoser)
	if !ok {
		t.Fatal("streamDiagnoserImporter does not satisfy StreamDiagnoser")
	}

	// consume the iterator
	for range streaming.StreamExtract(context.Background(), newTestInput("", "")) {
	}

	got := diagnoser.StreamDiagnostics()
	if diff := cmp.Diff([]ast.Diagnostic{wantDiag}, got); diff != "" {
		t.Errorf("StreamDiagnostics mismatch (-want +got):\n%s", diff)
	}
}
