// Package importer defines the Beancount import framework: the Importer
// interface, the Factory pattern for creating configured instances, and the
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
	// DiagImporterNotRegistered is emitted when the caller forced an instance
	// name that is not in the per-run Registry. Severity: Error.
	DiagImporterNotRegistered = "importer-not-registered"

	// DiagImporterNone is emitted by Dispatch when every instance's Identify
	// returned false. Severity: Error.
	DiagImporterNone = "importer-none"

	// DiagImporterAmbiguous is reserved for a future strict-mode that probes
	// all importers and reports collisions. Dispatch does NOT emit it in ABI v1.
	DiagImporterAmbiguous = "importer-ambiguous"
)

// Importer is a fully-configured import driver for one declared instance
// (e.g. "boa_checking"). Implementations must be safe for concurrent
// calls to Identify and Extract after construction.
type Importer interface {
	// Name returns the instance name supplied to the Factory that
	// produced this Importer. The value is stable for the lifetime of
	// the instance and is the key under which a Registry holds it.
	Name() string

	// Identify is a cheap, side-effect-free check. It MUST NOT consume
	// in.Opener unless Path/MIME/Sniff are insufficient; if it does, it
	// MUST close the returned io.ReadCloser before returning. A true
	// result is a non-binding preference; Dispatch picks the first
	// match in Registry.Names() order. Identify reports no error: a
	// failure to identify is simply false.
	Identify(ctx context.Context, in Input) bool

	// Extract returns directives in source-encounter order plus
	// per-record diagnostics. A non-nil error is reserved for
	// system-level failures (I/O, ctx cancellation, structural format
	// corruption); ledger-content problems are Diagnostics, not errors.
	// Context cancellation MUST surface as a non-nil error.
	Extract(ctx context.Context, in Input) (Output, error)
}

// Factory produces a single fully-configured Importer instance. The
// New call IS the Configure step: there is no separately exposed
// Configure method on Importer. A non-nil error aborts creation and
// MUST be returned without a partially-constructed Importer leaking
// out; on error the first return MUST be nil.
//
// The decode callback decodes the caller's per-instance configuration
// (the TOML table body, with the reserved keys "kind" and "name"
// stripped) into a destination the factory supplies. It MUST NOT be
// nil; factories that take no configuration may simply ignore it.
//
// Factory.New is called at most once per (name, decode) pair by the
// caller building a Registry. Multiple New calls for distinct
// instances of the same kind MAY run concurrently; a Factory that
// holds shared state across calls is responsible for its own
// synchronisation.
type Factory interface {
	New(name string, decode func(dest any) error) (Importer, error)
}

// FactoryFunc adapts a plain function to the Factory interface,
// analogous to http.HandlerFunc.
type FactoryFunc func(name string, decode func(dest any) error) (Importer, error)

// New implements [Factory].
func (f FactoryFunc) New(name string, decode func(dest any) error) (Importer, error) {
	return f(name, decode)
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
