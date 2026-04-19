// Package validation performs semantic checks on a loaded beancount ledger.
//
// The validator walks a [ast.Ledger] in chronological order and enforces the
// cross-directive invariants that parsing alone cannot verify: accounts must
// be opened before they are used and may not be used after being closed,
// transactions must balance per currency, balance assertions must match the
// running balance (within the inferred or explicit tolerance), pad directives
// must be followed by a matching balance assertion, and so on. Each problem
// it finds is reported as a postproc/api.Error tagged with a Code identifying
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
// Each subpackage exports a postproc/api.Plugin whose Apply method consumes
// the current ledger snapshot and emits api.Error diagnostics. Callers
// invoke the three plugins in order, committing any non-nil
// Result.Directives with [ast.Ledger.ReplaceAll] so later plugins observe
// earlier rewrites, and merging Result.Errors from each call.
//
// A typical wiring looks like:
//
//	ledger, err := ast.Load("main.beancount")
//	if err != nil {
//		log.Fatal(err)
//	}
//	ctx := context.Background()
//	opts := options.BuildRaw(ledger)
//
//	var errs []api.Error
//	for _, p := range []api.Plugin{pad.Plugin{}, balance.Plugin{}, validations.Plugin{}} {
//		res, err := p.Apply(ctx, api.Input{
//			Directives: ledger.All(),
//			Options:    opts,
//		})
//		if err != nil {
//			log.Fatal(err)
//		}
//		if res.Directives != nil {
//			ledger.ReplaceAll(res.Directives)
//		}
//		errs = append(errs, res.Errors...)
//	}
//
// The pipeline does not sort globally — each plugin's Errors slice is
// emitted in the order its internal walk visits directives, and callers
// that need a stable global ordering sort by (filename, offset, code)
// themselves.
package validation
