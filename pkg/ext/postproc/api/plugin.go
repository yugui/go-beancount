package api

import (
	"context"
	"fmt"
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Plugin transforms a beancount ledger in response to a `plugin "name"`
// directive. The runner calls Apply once per matching directive.
// Implementations are called sequentially; the runner never invokes
// Apply concurrently on the same instance.
type Plugin interface {
	// Apply transforms the ledger. It receives the current state via in
	// and returns the transformation result. A non-nil error indicates a
	// fatal plugin failure; the runner wraps it into an [Error] with Code
	// "plugin-failed" and leaves the ledger unchanged for this plugin.
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

	// Options snapshots option directives with last-wins semantics,
	// matching beancount upstream. Nil when the ledger has no options.
	Options map[string]string

	// Config is the second argument of the triggering plugin directive,
	// empty when omitted. Each plugin interprets it per its own
	// convention.
	Config string

	// Directive is the *ast.Plugin directive that triggered this call,
	// provided for source-location-aware error reporting.
	Directive *ast.Plugin

	// Ledger is the loaded ledger. It lets plugins call helpers such as
	// [ast.ResolvePath] that need the full ledger context. Plugins must
	// treat it as read-only with respect to its directive contents — any
	// mutation must go through [Result.Directives].
	//
	// Ledger may be nil when Input is constructed without a backing
	// ledger, for example in unit tests that build directives directly.
	// Plugins should pass it through to ledger-tolerant helpers
	// ([ast.ResolvePath] handles nil) or skip the dependent step rather
	// than panicking.
	Ledger *ast.Ledger

	// LedgerRoot is the filename of the root ledger file (the first file
	// in the load order).
	//
	// Deprecated: Use Ledger together with [ast.ResolvePath] instead.
	// LedgerRoot may be removed in a future release.
	LedgerRoot string
}

// Result is what a [Plugin] returns to the runner. Errors never halt the
// pipeline; they are collected and returned by the runner.
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

	// Errors collects plugin diagnostics. A non-nil returned error
	// (the second return value of Apply) is distinct: the runner wraps
	// it into an Error with Code "plugin-failed".
	Errors []Error
}

// Error is a plugin diagnostic. It mirrors [validation.Error] in shape so
// diagnostics format the same way across layers. Code is an open-ended
// string so plugins can pick their own codes; the runner uses
// "plugin-not-registered", "plugin-failed", and "plugin-canceled".
type Error struct {
	// Code categorizes the error. Runner-emitted codes are documented
	// in the parent pkg/ext/postproc package. Plugin-defined codes are
	// freeform.
	Code string

	// Span locates the error in source. Zero value is valid for errors
	// without a source location.
	Span ast.Span

	// Message is a human-readable description of the problem.
	Message string
}

// Error returns a human-readable description of the plugin error,
// including source location when available.
func (e Error) Error() string {
	pos := e.Span.Start
	if pos.Filename != "" {
		return fmt.Sprintf("%s:%d:%d: %s", pos.Filename, pos.Line, pos.Column, e.Message)
	}
	return e.Message
}
