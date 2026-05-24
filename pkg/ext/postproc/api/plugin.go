package api

import (
	"context"
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Plugin transforms a beancount ledger in response to a `plugin "name"`
// directive. The runner calls Apply once per matching directive.
// Implementations are called sequentially; the runner never invokes
// Apply concurrently on the same instance.
type Plugin interface {
	// Apply transforms the ledger. It receives the current state via in
	// and returns the transformation result. A non-nil error indicates
	// a fatal plugin runtime failure; the runner halts the pipeline,
	// wraps the error with the plugin name, and propagates it to its
	// caller. The ledger is left unchanged for this plugin.
	Apply(ctx context.Context, in Input) (Result, error)
}

// PluginFunc adapts an ordinary function to the [Plugin] interface.
type PluginFunc func(ctx context.Context, in Input) (Result, error)

// Apply calls f(ctx, in).
func (f PluginFunc) Apply(ctx context.Context, in Input) (Result, error) {
	return f(ctx, in)
}

// Input is the read-only snapshot passed to each [Plugin.Apply] call.
type Input struct {
	// Directives iterates the ledger's current contents in canonical
	// chronological order. The iterator is re-runnable: iterating
	// multiple times is safe and yields the same result within a single
	// Apply call. Do not mutate the directive structs it yields; to
	// change the ledger, build a new slice and return it via
	// [Result.Directives]. For random access, materialize with
	// slices.Collect.
	Directives iter.Seq2[int, ast.Directive]

	// Options is the typed snapshot of option values taken at ledger
	// construction (see [ast.Ledger.Options]). Plugins must not assume
	// mutations to Option directives later in the pipeline are
	// reflected. Nil only when Input is constructed directly rather than
	// via [ast.Load], [ast.LoadFile], or [ast.LoadReader];
	// [ast.OptionValues] accessors are nil-safe and fall back to registry
	// defaults.
	Options *ast.OptionValues

	// Config is the second argument of the triggering plugin directive,
	// empty when omitted. Each plugin interprets it per its own
	// convention.
	Config string

	// Directive is the *ast.Plugin directive that triggered this call,
	// provided for source-location-aware error reporting.
	Directive *ast.Plugin

	// SourceFilename is the absolute or repository-relative path of the
	// root beancount source file that produced the ledger under
	// transformation. It is the filename of the first entry of
	// [ast.Ledger.Files] — the file the user named when invoking
	// [ast.LoadFile] (or the equivalent root for [ast.Load] /
	// [ast.LoadReader]). Plugins use it as the anchor directory for
	// path-bearing config (e.g. YAML side-files referenced relatively
	// from the ledger).
	//
	// Empty when the ledger was constructed programmatically without an
	// associated source file (i.e. len(ledger.Files) == 0) or when the
	// runner is invoked on a hand-built &Ledger{} with no Files slice.
	// Plugins that require a non-empty value must report a Diagnostic
	// rather than returning an error.
	SourceFilename string
}

// Result is what a [Plugin] returns to the runner. Diagnostics never
// halt the pipeline; the runner appends them to [ast.Ledger.Diagnostics]
// alongside any directive replacement signaled by Directives.
type Result struct {
	// Directives, when non-nil, replaces the ledger's contents in one
	// operation — enabling add, modify, delete, and reorder through a
	// single uniform primitive matching beancount upstream.
	//
	//   - nil              → no change (common for diagnostic-only plugins)
	//   - non-nil empty    → clears the ledger
	//   - non-nil non-empty → replaces ledger contents verbatim
	//
	// To modify an existing directive, construct a new value with the
	// desired changes and include it in the returned slice. Do not
	// mutate directives obtained from [Input.Directives].
	Directives []ast.Directive

	// Diagnostics collects plugin-emitted findings. The runner appends
	// them to [ast.Ledger.Diagnostics] so the ledger carries every
	// ledger-content problem on a single channel. A non-nil returned
	// error (the second return value of Apply) is distinct: the runner
	// halts the pipeline and propagates it to its caller rather than
	// converting it to a diagnostic.
	Diagnostics []ast.Diagnostic
}
