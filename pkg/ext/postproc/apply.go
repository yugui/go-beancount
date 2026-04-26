package postproc

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// Apply walks ledger for *ast.Plugin directives in canonical source
// order, looks each up in the registry, and calls [api.Plugin.Apply].
// Between calls, non-nil [api.Result.Directives] are committed via
// [ast.Ledger.ReplaceAll] so later plugins see earlier output, and
// [api.Result.Diagnostics] are appended to [ast.Ledger.Diagnostics] so
// the ledger carries every plugin-emitted finding.
//
// The returned error is reserved for system-level failures: a non-nil
// error from [api.Plugin.Apply] (treated as a runtime failure of the
// plugin itself, not a ledger-content issue) and ctx cancellation. On
// such an error Apply halts the pipeline immediately — no further
// plugins run. Diagnostics already appended to the ledger before the
// halt are kept so callers can inspect partial progress.
//
// Non-nil errors returned by [api.Plugin.Apply] are wrapped with the
// plugin name so callers can see which plugin failed; the underlying
// error remains observable via [errors.Is] / [errors.As].
//
// Unknown plugin names are NOT system-level failures: they are caused
// by ledger contents (a `plugin "foo"` directive whose name is not
// registered with the host). Apply records an [ast.Diagnostic] with
// Code "plugin-not-registered" on the ledger and continues with the
// remaining plugin directives.
func Apply(ctx context.Context, ledger *ast.Ledger) error {
	if ledger == nil {
		return nil
	}

	plugins := collectPluginDirectives(ledger)
	if len(plugins) == 0 {
		return nil
	}
	opts := options.BuildRaw(ledger)

	for _, pd := range plugins {
		if err := ctx.Err(); err != nil {
			return err
		}

		p, ok := lookup(pd.Name)
		if !ok {
			ledger.Diagnostics = append(ledger.Diagnostics, ast.Diagnostic{
				Code:    "plugin-not-registered",
				Span:    pd.Span,
				Message: fmt.Sprintf("plugin %q is not registered", pd.Name),
			})
			continue
		}

		result, err := p.Apply(ctx, api.Input{
			Directives: ledger.All(),
			Options:    opts,
			Config:     pd.Config,
			Directive:  pd,
		})
		if err != nil {
			return fmt.Errorf("plugin %q: %w", pd.Name, err)
		}

		ledger.Diagnostics = append(ledger.Diagnostics, result.Diagnostics...)
		if result.Directives != nil {
			ledger.ReplaceAll(result.Directives)
		}
	}
	return nil
}

// collectPluginDirectives collects all *ast.Plugin directives from the
// ledger in canonical order. This is done before any [ast.Ledger.ReplaceAll]
// call to avoid invalidating the iterator.
func collectPluginDirectives(ledger *ast.Ledger) []*ast.Plugin {
	var plugins []*ast.Plugin
	for _, d := range ledger.All() {
		if p, ok := d.(*ast.Plugin); ok {
			plugins = append(plugins, p)
		}
	}
	return plugins
}
