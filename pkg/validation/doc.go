// Package validation performs semantic checks on a loaded beancount ledger.
//
// The validator walks a [ast.Ledger] in chronological order and enforces the
// cross-directive invariants that parsing alone cannot verify: accounts must
// be opened before they are used and may not be used after being closed,
// transactions must balance per currency, balance assertions must match the
// running balance (within the inferred or explicit tolerance), pad directives
// must be followed by a matching balance assertion, and so on. Each problem
// it finds is reported as an [ast.Diagnostic] tagged with a Code identifying
// the kind of failure.
//
// Validation is delivered as a three-plugin pipeline implemented in
// subpackages:
//
//   - pkg/validation/pad resolves pad directives into synthesized
//     transactions against the following balance assertion.
//   - pkg/validation/balance verifies balance assertions against the
//     running per-account balance and consumes the pad-synthesized
//     residuals.
//   - pkg/validation/validations runs the per-directive validators
//     (open/close accounting, active-account enforcement, allowed-currency
//     constraints, transaction balancing).
//
// Each subpackage exposes an Apply function that consumes the current
// ledger snapshot and emits [ast.Diagnostic] values via Result.Diagnostics.
// Importing a subpackage also registers it in the global postproc registry
// under its canonical name, so a beancount `plugin "..."` directive can
// activate it.
//
// The simplest way to load and validate a ledger is via pkg/loader:
//
//	ledger, err := loader.LoadFile(ctx, "main.beancount")
//	// ledger.Diagnostics carries every problem found.
//
// For fine-grained control, wire the Apply functions manually in order
// (pad → balance → validations), committing any non-nil Result.Directives
// with [ast.Ledger.ReplaceAll] so later plugins observe earlier rewrites:
//
//	ctx := context.Background()
//	opts := options.BuildRaw(ledger)
//
//	var diags []ast.Diagnostic
//	for _, apply := range []func(context.Context, api.Input) (api.Result, error){
//		pad.Apply, balance.Apply, validations.Apply,
//	} {
//		res, err := apply(ctx, api.Input{
//			Directives: ledger.All(),
//			Options:    opts,
//		})
//		if err != nil {
//			log.Fatal(err)
//		}
//		if res.Directives != nil {
//			ledger.ReplaceAll(res.Directives)
//		}
//		diags = append(diags, res.Diagnostics...)
//	}
//
// The pipeline does not sort globally — each plugin's Diagnostics slice
// is emitted in the order its internal walk visits directives, and
// callers that need a stable global ordering sort by (filename, offset,
// code) themselves.
package validation
