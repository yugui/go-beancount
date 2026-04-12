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
// The entry point is [Check]:
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
//
// Callers that need additional, project-specific checks can plug in handlers
// for beancount's `custom` directive via [RegisterCustomAssertion]. The
// built-in "assert" handler compares the running balance of an account to an
// expected amount and is registered automatically at package init time.
package validation
