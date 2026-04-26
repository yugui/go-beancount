// Package loader provides a high-level entry point for loading beancount
// source with plugin processing, mirroring beancount's upstream loader.py.
//
// Three entry points mirror the syntax and format packages:
//
//   - [Load] reads from an in-memory string,
//   - [LoadReader] reads from an io.Reader,
//   - [LoadFile] reads from a filesystem path.
//
// Each parses the source via the corresponding ast.Load* function and then
// applies plugins according to the plugin_processing_mode option found in
// the ledger:
//
//   - "raw": applies only the plugin directives present in the ledger, in
//     canonical order, via the global postproc registry.
//   - "default" (or unset): mirrors the upstream _load pipeline —
//     pre-directive built-ins, then directive-specified plugins, then
//     pad → balance → validations.
//
// Example:
//
//	ledger, errs, err := loader.LoadFile(ctx, "main.beancount")
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
	"io"

	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/balance"
	"github.com/yugui/go-beancount/pkg/validation/document"
	"github.com/yugui/go-beancount/pkg/validation/pad"
	"github.com/yugui/go-beancount/pkg/validation/validations"
)

// preBuiltins are built-in plugins applied before directive-specified plugins
// in default mode: document directory scanning and file-existence verification.
var preBuiltins = []api.Plugin{
	api.PluginFunc(document.Apply),
}

// postBuiltins are built-in plugins applied after directive-specified plugins
// in default mode, in the order: pad → balance → validations.
var postBuiltins = []api.Plugin{
	api.PluginFunc(pad.Apply),
	api.PluginFunc(balance.Apply),
	api.PluginFunc(validations.Apply),
}

// Load parses src via [ast.Load] and applies the plugin pipeline.
// See the package documentation for the pipeline and return-value contract.
func Load(ctx context.Context, src string, opts ...Option) (*ast.Ledger, []api.Error, error) {
	ledger, err := ast.Load(src, opts...)
	if err != nil {
		return nil, nil, err
	}
	return runPipeline(ctx, ledger)
}

// LoadReader parses r via [ast.LoadReader] and applies the plugin pipeline.
func LoadReader(ctx context.Context, r io.Reader, opts ...Option) (*ast.Ledger, []api.Error, error) {
	ledger, err := ast.LoadReader(r, opts...)
	if err != nil {
		return nil, nil, err
	}
	return runPipeline(ctx, ledger)
}

// LoadFile parses path via [ast.LoadFile] and applies the plugin pipeline.
func LoadFile(ctx context.Context, path string, opts ...Option) (*ast.Ledger, []api.Error, error) {
	ledger, err := ast.LoadFile(path, opts...)
	if err != nil {
		return nil, nil, err
	}
	return runPipeline(ctx, ledger)
}

// runPipeline applies the plugin pipeline to a freshly loaded ledger and
// returns the processed ledger together with plugin diagnostics. Plugin
// errors surface in the []api.Error slice; the returned error is always
// nil (load failures are handled by the caller).
func runPipeline(ctx context.Context, ledger *ast.Ledger) (*ast.Ledger, []api.Error, error) {

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
