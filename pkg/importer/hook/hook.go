// Package hook defines the post-import hook pipeline. All exported symbols are
// part of the plugin ABI; any incompatible change requires a
// [github.com/yugui/go-beancount/pkg/ext/goplug.APIVersion] bump.
package hook

import (
	"context"

	"github.com/yugui/go-beancount/pkg/ast"
)

// DiagHookNotRegistered is emitted by Chain when a name in the caller-supplied
// chain is not in the registry. Severity: Error.
const DiagHookNotRegistered = "hook-not-registered"

// Hook transforms a directive list produced by an importer or a prior hook
// rung. Implementations are registered with [Register] and composed by [Chain].
//
// Name returns the registry key. By convention, use the upstream tool's name
// for canonical reference hooks (e.g. "classify") and the Go fully-qualified
// package path otherwise — mirrors [github.com/yugui/go-beancount/pkg/importer.Importer.Name].
//
// Apply is the work-doing call. Error vs Diagnostic split mirrors
// [github.com/yugui/go-beancount/pkg/importer.Importer.Extract]: a non-nil
// error indicates a system-level failure (ctx cancellation, I/O, programmer
// error); per-directive problems are [ast.Diagnostic] entries in
// HookResult.Diagnostics. Apply MUST NOT mutate in.Directives, in.Hints, or
// in.Options.
type Hook interface {
	Name() string
	Apply(ctx context.Context, in HookInput) (HookResult, error)
}

// HookInput carries the directive list and metadata into each hook rung.
//
// A hook MUST NOT mutate Directives, Hints, or Options. Directives is
// never nil when reaching a registered hook (Chain normalises nil to an
// empty slice before invoking the first hook). Hints MAY be nil; hooks
// MUST treat nil identically to an empty map. Options MAY be nil; hooks
// MUST tolerate nil because [ast.OptionValues] accessors are nil-safe.
// Keys in Hints that begin with "hook." are reserved by the framework.
type HookInput struct {
	Directives []ast.Directive
	Hints      map[string]string
	Options    *ast.OptionValues
}

// HookResult is what a Hook's Apply returns to Chain.
//
// An Apply implementation that performs no transformation MAY return
// HookResult{Directives: in.Directives}; Chain treats the returned
// Directives as logically immutable after Apply returns. Each hook's
// Diagnostics are appended by Chain in chain order. When a hook returns
// a non-nil error, Chain preserves its Diagnostics in the composed
// output but discards its Directives.
type HookResult struct {
	Directives  []ast.Directive
	Diagnostics []ast.Diagnostic
}

// Configurable is an optional sub-interface for hooks that accept structured
// configuration. Detected via type assertion; hooks that do not implement it
// receive no Configure call. Configure is called at most once per instance,
// before any Apply call.
//
// Implementors must call decode to populate their config and return any
// resulting error. decode is guaranteed non-nil.
type Configurable interface {
	Hook
	Configure(decode func(dest any) error) error
}
