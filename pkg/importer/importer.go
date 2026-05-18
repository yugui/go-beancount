// Package importer defines the Beancount import framework: the Importer
// interface, supporting types, optional sub-interfaces, and the
// registry-driven dispatch pipeline. All exported types are part of the
// plugin ABI; breaking changes require a
// [github.com/yugui/go-beancount/pkg/ext/goplug.APIVersion] bump.
package importer

import (
	"context"
	"io"
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Diagnostic codes emitted by this package. They are defined here (not in
// pkg/ast) because they are framework-specific. The Code field of
// [ast.Diagnostic] is a free-form string; these constants give callers a
// stable handle to match against.
const (
	// DiagImporterNotRegistered is emitted by Apply when the caller
	// forced an importer name that is not in the registry. Severity: Error.
	DiagImporterNotRegistered = "importer-not-registered"

	// DiagImporterNone is emitted by Dispatch when every registered
	// importer's Identify returned false. Severity: Error.
	DiagImporterNone = "importer-none"

	// DiagImporterAmbiguous is reserved for a future strict-mode that
	// probes all importers and reports collisions. Dispatch does NOT emit
	// it in ABI v1.
	DiagImporterAmbiguous = "importer-ambiguous"
)

// Importer converts an input file into beancount directives. Implementations
// are registered with [Register] and selected by [Dispatch] via [Identify].
//
// Name returns the registry key. By convention, use the upstream tool's own
// name (e.g. "csv", "ofx") for canonical reference importers, and the Go
// fully-qualified package path otherwise — the same convention pkg/quote uses.
//
// Identify is a pure, side-effect-free, cheap check: it MUST NOT consume
// in.Opener unless Path, MIME, and Sniff are insufficient. If it does call
// Opener it MUST close the returned io.ReadCloser before returning. A true
// result is a non-binding preference; Dispatch picks the first match in
// sorted-by-name order.
//
// Extract does the actual work. It returns directives in source-encounter
// order, per-record diagnostics for problems the importer recovered from, and
// a non-nil error ONLY for system-level failures (I/O, ctx cancellation,
// structural format corruption). Context cancellation MUST surface as a
// non-nil error. Ledger-content problems (bad date, malformed amount) are
// Diagnostics, not errors.
type Importer interface {
	Name() string
	Identify(ctx context.Context, in Input) bool
	Extract(ctx context.Context, in Input) (Output, error)
}

// Input carries everything an Importer needs to identify and extract
// directives from a single file.
type Input struct {
	// Path is the display name passed to diagnostics and used by Identify
	// for extension/regex checks. It SHOULD be the user-visible path the
	// CLI was invoked with. Empty Path is permitted (stdin, forced
	// importer); importers MUST tolerate Path == "".
	Path string

	// Opener returns a fresh io.ReadCloser on each call, positioned at the
	// start of the file. MAY be called zero, one, or many times. MUST NOT
	// be nil for any Input reaching a registered Importer.
	Opener func() (io.ReadCloser, error)

	// Sniff holds the pre-read prefix of the file, up to 4096 bytes.
	// Callers MUST NOT mutate the slice; importers MUST treat it as
	// read-only. An empty Sniff is permitted.
	Sniff []byte

	// MIME is a best-effort content-type hint. Empty means "no hint";
	// importers MUST NOT treat empty MIME as a refusal signal.
	MIME string

	// Hints is a caller-supplied key/value bag. Framework-reserved keys:
	//   "account" — primary account override (set by cmd/beanimport --account).
	// All other keys are importer-specific. Importers that consume Hints MUST
	// document which keys they read. Hints MAY be nil; importers MUST treat
	// nil Hints identically to an empty map.
	Hints map[string]string
}

// Output is the result of a successful or partially successful Extract call.
type Output struct {
	// Directives is in source-encounter order. An empty slice is a valid
	// successful result (e.g. a CSV with header only).
	Directives []ast.Directive

	// Diagnostics carries per-row / per-record problems the importer
	// recovered from. Severity is chosen by the importer; --strict
	// promotion (warning → error) is the CLI's responsibility.
	Diagnostics []ast.Diagnostic
}

// Configurable is an optional sub-interface for importers that accept
// structured configuration. Detected via type assertion; importers that do
// not implement it receive no Configure call.
//
// decode is a caller-supplied callback that decodes whatever the caller holds
// (TOML, JSON, an in-memory map) into the destination the importer provides.
// The importer writes the fixed pattern:
//
//	var c MyConfig
//	if err := decode(&c); err != nil { return err }
//	i.cfg = c
//
// decode MUST NOT be nil. Callers that have no configuration to supply do not
// call Configure at all. Configure MUST be called at most once per Importer
// instance, before any Identify or Extract call; calling it twice has
// undefined behaviour. A non-nil error fails the dispatch pipeline early:
// Apply propagates it as a framework error, not a Diagnostic.
type Configurable interface {
	Importer
	Configure(decode func(dest any) error) error
}

// Streaming is an optional sub-interface for importers that can produce
// directives incrementally. Each yield is either (directive, nil) for a
// successful directive or (zero, err) for a per-record problem.
//
// Importers that also want to emit Diagnostics in the streaming path SHOULD
// implement [StreamDiagnoser]; the caller invokes StreamDiagnostics AFTER
// the iterator is fully consumed or abandoned.
//
// Apply always uses the buffered Extract path in ABI v1 even when the
// importer satisfies Streaming. Streaming exists for downstream consumers
// that genuinely need incremental output. Importers implementing both MUST
// produce equivalent directive sequences from Extract and StreamExtract for
// the same Input.
type Streaming interface {
	Importer
	StreamExtract(ctx context.Context, in Input) iter.Seq2[ast.Directive, error]
}

// StreamDiagnoser is an optional companion interface for [Streaming]
// importers. The caller invokes StreamDiagnostics after the iterator
// returned by StreamExtract has been fully consumed or abandoned.
type StreamDiagnoser interface {
	StreamDiagnostics() []ast.Diagnostic
}
