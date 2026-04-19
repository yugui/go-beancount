// Package loader provides a high-level entry point for loading beancount
// files with plugin processing, mirroring beancount's upstream loader.py.
//
// The primary entry point is [Load], which calls [ast.Load] and then applies
// plugins according to the plugin_processing_mode option found in the ledger:
//
//   - "raw": applies only the plugin directives present in the ledger, in
//     canonical order, via the global postproc registry.
//   - "default" (or unset): mirrors the upstream _load pipeline —
//     pre-directive built-ins, then directive-specified plugins, then
//     pad → balance → validations.
//
// Example:
//
//	ledger, errs, err := loader.Load(ctx, "main.beancount")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, e := range errs {
//	    log.Println(e)
//	}
package loader

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/balance"
	"github.com/yugui/go-beancount/pkg/validation/document"
	"github.com/yugui/go-beancount/pkg/validation/pad"
	"github.com/yugui/go-beancount/pkg/validation/validations"
)

// preBuiltins are built-in plugins applied before directive-specified plugins
// in default mode: document directory scanning and file-existence verification.
var preBuiltins = []api.Plugin{
	document.Plugin,
}

// postBuiltins are built-in plugins applied after directive-specified plugins
// in default mode, in the order: pad → balance → validations.
var postBuiltins = []api.Plugin{
	pad.Plugin,
	balance.Plugin,
	validations.Plugin,
}

// Load parses filename via [ast.Load] and applies the plugin pipeline
// controlled by the plugin_processing_mode option in the ledger.
//
// It returns the processed ledger, any plugin diagnostics, and a non-nil
// error only if the file could not be read or parsed at all. Plugin errors
// are returned in the []api.Error slice and never cause the error return to
// be non-nil.
func Load(ctx context.Context, filename string) (*ast.Ledger, []api.Error, error) {
	ledger, err := ast.Load(filename)
	if err != nil {
		return nil, nil, err
	}

	rawOpts := options.BuildRaw(ledger)

	var errs []api.Error
	if rawOpts["plugin_processing_mode"] == "raw" {
		errs = postproc.Apply(ctx, ledger)
	} else {
		errs = applyDefault(ctx, ledger, rawOpts)
	}

	return ledger, errs, nil
}

// applyDefault runs the default pipeline:
//  1. preBuiltins: document directory scanning and file-existence verification
//  2. directive-specified plugins via the global registry
//  3. postBuiltins: pad → balance → validations
func applyDefault(ctx context.Context, ledger *ast.Ledger, opts map[string]string) []api.Error {
	var errs []api.Error

	for _, p := range preBuiltins {
		if ctx.Err() != nil {
			return errs
		}
		errs = append(errs, runBuiltin(ctx, ledger, opts, p)...)
	}

	if ctx.Err() != nil {
		return errs
	}
	errs = append(errs, postproc.Apply(ctx, ledger)...)

	for _, p := range postBuiltins {
		if ctx.Err() != nil {
			return errs
		}
		errs = append(errs, runBuiltin(ctx, ledger, opts, p)...)
	}

	return errs
}

// runBuiltin applies a single built-in plugin and commits any ledger changes.
func runBuiltin(ctx context.Context, ledger *ast.Ledger, opts map[string]string, p api.Plugin) []api.Error {
	res, err := p.Apply(ctx, api.Input{
		Directives: ledger.All(),
		Options:    opts,
	})
	if err != nil {
		return []api.Error{{
			Code:    "plugin-failed",
			Message: fmt.Sprintf("built-in plugin: %v", err),
		}}
	}
	if res.Directives != nil {
		ledger.ReplaceAll(res.Directives)
	}
	return res.Errors
}
