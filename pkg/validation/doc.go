// Package validation performs semantic checks on a loaded beancount ledger.
//
// The validator walks a [ast.Ledger] in chronological order and enforces the
// cross-directive invariants that parsing alone cannot verify: accounts must
// be opened before they are used and may not be used after being closed,
// transactions must balance per currency, balance assertions must match the
// running balance (within the inferred or explicit tolerance), pad directives
// must be followed by a matching balance assertion, and so on. Each problem
// it finds is reported as an [Error] tagged with a [Code] identifying the
// kind of failure.
//
// New code should drive validation through the 3-plugin pipeline
// (pad -> balance -> validations) exposed by the subpackages
// pkg/validation/pad, pkg/validation/balance, and
// pkg/validation/validations. Each subpackage exports a postproc/api.Plugin
// whose Apply method consumes the current ledger snapshot and emits
// api.Error diagnostics. Callers invoke the three plugins in order,
// committing any non-nil Directives between calls with
// [ast.Ledger.ReplaceAll] so later plugins observe earlier rewrites.
//
// [Check] is the legacy single-entry-point form, retained for backward
// compatibility while the plugin layer stabilizes:
//
//	ledger, err := ast.Load("main.beancount")
//	if err != nil {
//		log.Fatal(err)
//	}
//	for _, e := range validation.Check(ledger) {
//		fmt.Println(e)
//	}
//
// Check returns the errors sorted deterministically by
// (filename, byte offset, code) so that output is stable across runs.
// The plugin pipeline does not sort globally — each plugin's Errors
// slice is emitted in the order its internal walk visits directives,
// and callers that need a stable global ordering sort by
// (filename, offset, code) themselves.
package validation
