// Package loader provides a high-level entry point for loading beancount
// sources with plugin processing, mirroring beancount's upstream loader.py.
//
// Three entry points cover the source-shape variants: [Load] takes a
// source string, [LoadReader] takes an [io.Reader], and [LoadFile] takes
// a file path. [LoadReader] and [LoadFile] return a top-level error to
// surface I/O failures; [Load] has no I/O so it returns only the ledger
// and the plugin diagnostics.
//
// All three then apply plugins according to the plugin_processing_mode
// option found in the loaded ledger:
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
	document.Plugin,
}

// postBuiltins are built-in plugins applied after directive-specified plugins
// in default mode, in the order: pad → balance → validations.
var postBuiltins = []api.Plugin{
	pad.Plugin,
	balance.Plugin,
	validations.Plugin,
}

// LoadFile parses the beancount file at filename via [ast.LoadFile] and
// applies the plugin pipeline controlled by the plugin_processing_mode
// option in the ledger.
//
// A non-nil error is returned only when [ast.LoadFile] cannot make
// filename absolute (essentially, when the process working directory is
// unavailable). I/O failures opening the root file or any include
// surface as diagnostics on the returned ledger; plugin diagnostics
// surface in the returned []api.Error slice. Neither causes the error
// return to be non-nil.
func LoadFile(ctx context.Context, filename string) (*ast.Ledger, []api.Error, error) {
	ledger, err := ast.LoadFile(filename)
	if err != nil {
		return nil, nil, err
	}
	return ledger, applyPipeline(ctx, ledger), nil
}

// Load parses src as beancount source via [ast.Load] and applies the
// plugin pipeline. src is beancount source text, not a file path; pass
// a path to [LoadFile] (or use [ast.WithFilename] to give inline source
// a synthetic location for include resolution).
//
// Unlike LoadFile, Load has no top-level I/O, so it returns no error:
// parse and plugin diagnostics surface through the ledger's
// Diagnostics and the returned []api.Error respectively.
//
// Example:
//
//	ledger, errs := loader.Load(ctx, src)
func Load(ctx context.Context, src string, opts ...ast.LoadOption) (*ast.Ledger, []api.Error) {
	ledger := ast.Load(src, opts...)
	return ledger, applyPipeline(ctx, ledger)
}

// LoadReader reads beancount source from r via [ast.LoadReader] and applies
// the plugin pipeline. It returns a non-nil error only when reading from r
// fails; parse and plugin diagnostics surface through the ledger and the
// []api.Error slice respectively.
func LoadReader(ctx context.Context, r io.Reader, opts ...ast.LoadOption) (*ast.Ledger, []api.Error, error) {
	ledger, err := ast.LoadReader(r, opts...)
	if err != nil {
		return nil, nil, err
	}
	return ledger, applyPipeline(ctx, ledger), nil
}

// applyPipeline runs the plugin pipeline against ledger and returns any
// diagnostics. Mode selection follows the plugin_processing_mode option.
func applyPipeline(ctx context.Context, ledger *ast.Ledger) []api.Error {
	rawOpts := options.BuildRaw(ledger)
	if rawOpts["plugin_processing_mode"] == "raw" {
		return postproc.Apply(ctx, ledger)
	}
	return applyDefault(ctx, ledger, rawOpts)
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
	// LedgerRoot is deprecated; populated only until existing plugins
	// migrate to api.Input.Ledger.
	var ledgerRoot string
	if len(ledger.Files) > 0 {
		ledgerRoot = ledger.Files[0].Filename
	}
	res, err := p.Apply(ctx, api.Input{
		Directives: ledger.All(),
		Options:    opts,
		LedgerRoot: ledgerRoot,
		Ledger:     ledger,
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
