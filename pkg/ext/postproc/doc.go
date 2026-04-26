// Package postproc provides the in-process plugin registry and runner for
// beancount ledger transformation plugins.
//
// Plugins are registered at init time via [Register] and executed via
// [Apply]. The runner walks a ledger's plugin directives in canonical
// source order, invoking each registered plugin sequentially. Between
// calls, non-nil [api.Result.Directives] are committed via
// [ast.Ledger.ReplaceAll] so later plugins see earlier output, and
// [api.Result.Diagnostics] are appended to [ast.Ledger.Diagnostics] so
// the ledger is the single source of truth for ledger-content findings.
//
// The runner returns only system-level errors: a plugin's non-nil error
// (treated as a plugin runtime failure) or ctx cancellation. Either
// halts the pipeline immediately and is propagated to the caller.
// Ledger-content problems — including unknown plugin names — surface as
// diagnostics on the ledger, never via the error return value.
//
// Usage:
//
//	ledger, err := ast.LoadFile(path)
//	if err != nil { ... }
//	if err := postproc.Apply(ctx, ledger); err != nil {
//	    // System-level failure (plugin runtime error or ctx cancel).
//	    return err
//	}
//	// Inspect ledger.Diagnostics for any plugin findings.
//	// Semantic validation is itself a 3-plugin pipeline: pad, balance,
//	// validations. Callers apply them in order, feeding each plugin the
//	// current ledger contents via api.Input and committing any returned
//	// Directives with ast.Ledger.ReplaceAll between calls. See
//	// pkg/validation/pad, pkg/validation/balance, and
//	// pkg/validation/validations for their Apply functions.
//
// Plugin names follow Go fully-qualified package path convention (e.g.
// "github.com/yugui/go-beancount/plugins/auto_accounts") to avoid
// collisions across independently developed plugins. When multiple
// instances of the same type are registered, prefix with the package
// path and append a distinguishing suffix.
package postproc
