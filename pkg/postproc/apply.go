package postproc

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
)

// Apply walks ledger for *ast.Plugin directives in canonical source
// order, looks each up in the registry, and calls [api.Plugin.Apply].
// Between calls, non-nil [api.Result.Directives] are committed via
// [ast.Ledger.ReplaceAll] so later plugins see earlier output.
//
// Errors from plugins — both [api.Result.Errors] and non-nil returned
// errors — are collected and returned; they never halt subsequent
// plugins. Unknown plugin names emit an [api.Error] with Code
// "plugin-not-registered". A non-nil error from [api.Plugin.Apply] is
// wrapped into an [api.Error] with Code "plugin-failed" and the ledger
// is left unchanged for that plugin. A canceled ctx causes Apply to
// stop before the next plugin runs and surface a "plugin-canceled"
// error.
func Apply(ctx context.Context, ledger *ast.Ledger) []api.Error {
	if ledger == nil {
		return nil
	}

	plugins := collectPluginDirectives(ledger)
	if len(plugins) == 0 {
		return nil
	}
	opts := buildOptions(ledger)

	var errs []api.Error
	for _, pd := range plugins {
		if err := ctx.Err(); err != nil {
			errs = append(errs, api.Error{
				Code:    "plugin-canceled",
				Span:    pd.Span,
				Message: fmt.Sprintf("plugin %q: %v", pd.Name, err),
			})
			return errs
		}

		p, ok := lookup(pd.Name)
		if !ok {
			errs = append(errs, api.Error{
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
			errs = append(errs, api.Error{
				Code:    "plugin-failed",
				Span:    pd.Span,
				Message: fmt.Sprintf("plugin %q: %v", pd.Name, err),
			})
			continue
		}

		errs = append(errs, result.Errors...)
		if result.Directives != nil {
			ledger.ReplaceAll(result.Directives)
		}
	}
	return errs
}

// buildOptions builds the options map from the ledger's option
// directives, with last-wins semantics for duplicate keys. The snapshot
// is taken once before any plugin runs and is not updated by plugin
// mutations.
func buildOptions(ledger *ast.Ledger) map[string]string {
	var opts map[string]string
	for _, d := range ledger.All() {
		o, ok := d.(*ast.Option)
		if !ok {
			continue
		}
		if opts == nil {
			opts = make(map[string]string)
		}
		opts[o.Key] = o.Value
	}
	return opts
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
