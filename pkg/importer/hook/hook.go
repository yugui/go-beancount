// Package hook defines the post-import hook pipeline. All exported symbols are
// part of the plugin ABI; any incompatible change requires a
// [github.com/yugui/go-beancount/pkg/ext/goplug.APIVersion] bump.
package hook

import (
	"context"

	"github.com/yugui/go-beancount/pkg/ast"
)

// DiagHookNotRegistered is emitted by [Chain] when a name returned by
// [Registry.Names] is not resolved by [Registry.Lookup] — indicating a
// Registry implementation that violates its own invariant. Severity: Error.
const DiagHookNotRegistered = "hook-not-registered"

// Hook transforms a directive list produced by an importer or a prior rung of
// [Chain]. A Hook is produced by a [Factory]; its internal state is frozen at
// that point and Apply is safe for concurrent invocation on the same value.
type Hook interface {
	// Name returns the instance name supplied to the Factory that
	// produced this Hook. The value is stable for the lifetime of the
	// instance and is the key under which a Registry holds it.
	Name() string

	// Apply returns the directive list and any per-directive Diagnostics.
	// The returned Directives MAY be in.Directives unmodified (no copy
	// is required when the hook makes no changes). A non-nil error is
	// reserved for system-level failures (ctx cancellation, I/O,
	// programmer error); ledger-content problems are Diagnostics.
	// Apply MUST NOT mutate in.Directives, in.Hints, or in.Options.
	// Context cancellation MUST surface as a non-nil error.
	Apply(ctx context.Context, in HookInput) (HookResult, error)
}

// Factory produces a single fully-configured Hook instance. The New call IS
// the Configure step; there is no separately exposed Configure method on Hook.
// A non-nil error aborts creation and MUST be returned without a
// partially-constructed Hook leaking out; on error the first return MUST be nil.
//
// The decode callback decodes the caller's per-instance configuration (the TOML
// table body, with reserved keys "kind" and "name" stripped) into a destination
// the factory supplies. It MUST NOT be nil; factories that take no configuration
// may ignore it.
//
// Factory.New is called at most once per (name, decode) pair by the caller
// building a Registry. Multiple New calls for distinct instances of the same
// kind MAY run concurrently; a Factory holding shared state across calls is
// responsible for its own synchronisation.
type Factory interface {
	New(name string, decode func(dest any) error) (Hook, error)
}

// FactoryFunc adapts a function to the [Factory] interface, analogous to
// http.HandlerFunc.
type FactoryFunc func(name string, decode func(dest any) error) (Hook, error)

func (f FactoryFunc) New(name string, decode func(dest any) error) (Hook, error) {
	return f(name, decode)
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
