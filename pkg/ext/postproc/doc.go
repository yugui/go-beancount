// Package postproc provides the in-process plugin registry and runner for
// beancount ledger transformation plugins.
//
// Plugins are registered at init time via [Register] and executed via
// [Apply]. The runner walks a ledger's plugin directives in canonical
// source order, invoking each registered plugin sequentially. Between
// calls, non-nil [api.Result.Directives] are committed via
// [ast.Ledger.ReplaceAll] so later plugins see earlier output.
//
// Usage:
//
//	ledger, err := ast.Load(path)
//	if err != nil { ... }
//	pluginErrs := postproc.Apply(ctx, ledger)
//	// Semantic validation is itself a 3-plugin pipeline: pad, balance,
//	// validations. Callers apply them in order, feeding each plugin the
//	// current ledger contents via api.Input and committing any returned
//	// Directives with ast.Ledger.ReplaceAll between calls. See
//	// pkg/validation/pad, pkg/validation/balance, and
//	// pkg/validation/validations for the individual api.Plugin types.
//
// Plugin names follow Go fully-qualified package path convention (e.g.
// "github.com/yugui/go-beancount/plugins/auto_accounts") to avoid
// collisions across independently developed plugins. When multiple
// instances of the same type are registered, prefix with the package
// path and append a distinguishing suffix.
package postproc
