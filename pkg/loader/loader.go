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
// # Return-value contract
//
// All three entry points return a (*ast.Ledger, error) pair with a
// disciplined split of responsibilities:
//
//   - The returned [ast.Ledger] is the unified channel for every problem
//     attributable to the ledger contents itself: parse and lowering
//     failures, include resolution, plugin diagnostics, and the
//     pad/balance/validations pipeline. Each finding is recorded as an
//     [ast.Diagnostic] in [ast.Ledger.Diagnostics] with a Code, Span,
//     Message, and Severity.
//   - The returned error is reserved for failures that are NOT explained
//     by the ledger's contents: I/O failures from a caller-supplied
//     io.Reader, OS-level path resolution failures, and context
//     cancellation. Callers can use [errors.Is] against
//     [context.Canceled] or [context.DeadlineExceeded] to classify
//     cancellation.
//
// Errors from the pre-pipeline parse stage (e.g. [ast.LoadReader] /
// [ast.LoadFile] I/O failures) return a nil ledger because no ledger
// could be constructed. Errors that arise from the plugin pipeline —
// either ctx cancellation or a plugin runtime failure — return the
// partially-processed ledger alongside the error so callers can inspect
// any diagnostics accumulated before the halt.
//
// Example:
//
//	ledger, err := loader.LoadFile(ctx, "main.beancount")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, d := range ledger.Diagnostics {
//	    log.Println(d.Message)
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
func Load(ctx context.Context, src string, opts ...Option) (*ast.Ledger, error) {
	ledger, err := ast.Load(src, opts...)
	if err != nil {
		return nil, err
	}
	return runPipeline(ctx, ledger)
}

// LoadReader parses r via [ast.LoadReader] and applies the plugin pipeline.
// See the package documentation for the pipeline and return-value contract.
func LoadReader(ctx context.Context, r io.Reader, opts ...Option) (*ast.Ledger, error) {
	ledger, err := ast.LoadReader(r, opts...)
	if err != nil {
		return nil, err
	}
	return runPipeline(ctx, ledger)
}

// LoadFile parses path via [ast.LoadFile] and applies the plugin pipeline.
// See the package documentation for the pipeline and return-value contract.
func LoadFile(ctx context.Context, path string, opts ...Option) (*ast.Ledger, error) {
	ledger, err := ast.LoadFile(path, opts...)
	if err != nil {
		return nil, err
	}
	return runPipeline(ctx, ledger)
}

// runPipeline applies the plugin pipeline to a freshly loaded ledger.
// Diagnostics are written into [ast.Ledger.Diagnostics] by the runner
// and built-in helpers themselves; this function only forwards the
// system-level error from the pipeline (plugin runtime failure or ctx
// cancellation) and returns the ledger alongside it so callers can
// inspect partial results.
func runPipeline(ctx context.Context, ledger *ast.Ledger) (*ast.Ledger, error) {
	rawOpts := options.BuildRaw(ledger)
	if rawOpts["plugin_processing_mode"] == "raw" {
		return ledger, postproc.Apply(ctx, ledger)
	}
	return ledger, applyDefault(ctx, ledger, rawOpts)
}

// applyDefault runs the default pipeline:
//  1. preBuiltins: document directory scanning and file-existence verification
//  2. directive-specified plugins via the global registry
//  3. postBuiltins: pad → balance → validations
//
// Each step halts on the first system-level error and propagates it to
// the caller; subsequent steps are skipped.
func applyDefault(ctx context.Context, ledger *ast.Ledger, opts map[string]string) error {
	for _, p := range preBuiltins {
		if err := runBuiltin(ctx, ledger, opts, p); err != nil {
			return err
		}
	}
	if err := postproc.Apply(ctx, ledger); err != nil {
		return err
	}
	for _, p := range postBuiltins {
		if err := runBuiltin(ctx, ledger, opts, p); err != nil {
			return err
		}
	}
	return nil
}

// runBuiltin applies a single built-in plugin: it commits any returned
// Directives to the ledger and appends any returned Diagnostics to
// [ast.Ledger.Diagnostics]. A non-nil error from the plugin is treated
// as a system-level failure and returned wrapped with a "built-in
// plugin" prefix; ctx cancellation is also propagated.
func runBuiltin(ctx context.Context, ledger *ast.Ledger, opts map[string]string, p api.Plugin) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	res, err := p.Apply(ctx, api.Input{
		Directives: ledger.All(),
		Options:    opts,
	})
	if err != nil {
		return fmt.Errorf("built-in plugin: %w", err)
	}
	ledger.Diagnostics = append(ledger.Diagnostics, res.Diagnostics...)
	if res.Directives != nil {
		ledger.ReplaceAll(res.Directives)
	}
	return nil
}
