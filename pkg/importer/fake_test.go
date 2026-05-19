package importer

import (
	"bytes"
	"context"
	"io"
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
)

// fakeImporter is a minimal Importer for tests.
type fakeImporter struct {
	name       string
	identifyFn func(in Input) bool
	extractFn  func(in Input) (Output, error)
}

func (f *fakeImporter) Name() string { return f.name }

func (f *fakeImporter) Identify(_ context.Context, in Input) bool {
	if f.identifyFn == nil {
		return false
	}
	return f.identifyFn(in)
}

func (f *fakeImporter) Extract(_ context.Context, in Input) (Output, error) {
	if f.extractFn == nil {
		return Output{}, nil
	}
	return f.extractFn(in)
}

// streamingImporter satisfies both Importer and Streaming.
type streamingImporter struct {
	fakeImporter
	streamExtractFn func(ctx context.Context, in Input) iter.Seq2[ast.Directive, error]
}

func (s *streamingImporter) StreamExtract(ctx context.Context, in Input) iter.Seq2[ast.Directive, error] {
	if s.streamExtractFn != nil {
		return s.streamExtractFn(ctx, in)
	}
	return func(yield func(ast.Directive, error) bool) {}
}

// streamDiagnoserImporter satisfies Importer, Streaming, and StreamDiagnoser.
type streamDiagnoserImporter struct {
	streamingImporter
	diags []ast.Diagnostic
}

func (s *streamDiagnoserImporter) StreamDiagnostics() []ast.Diagnostic {
	return s.diags
}

// newTestInput builds an Input from a path and body string; Sniff capped at 4096 bytes.
func newTestInput(path, body string) Input {
	b := []byte(body)
	sniff := b
	if len(sniff) > 4096 {
		sniff = sniff[:4096]
	}
	return Input{
		Path:  path,
		Sniff: sniff,
		Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(b)), nil
		},
	}
}

// withCleanKindRegistry swaps the global kind registry for an empty one for the
// duration of a single test and restores it in t.Cleanup. Direct access to
// the unexported global is justified here because the package has no exported
// reset API and the concurrent-stress test requires atomic swap.
// Must not be used in tests that call t.Parallel(); the global swap is process-wide.
func withCleanKindRegistry(t interface {
	Helper()
	Cleanup(func())
}) {
	t.Helper()
	kindMu.Lock()
	old := kinds
	kinds = map[string]Factory{}
	kindMu.Unlock()
	t.Cleanup(func() {
		kindMu.Lock()
		kinds = old
		kindMu.Unlock()
	})
}
