// Package validation performs semantic checks on a loaded beancount ledger.
package validation

import "github.com/yugui/go-beancount/pkg/ast"

// Check runs all semantic validations on the given ledger and returns any
// errors found.
func Check(ledger *ast.Ledger) []Error {
	return newChecker(ledger).run()
}

// checker holds the state for a single validation pass.
type checker struct {
	ledger *ast.Ledger
}

// newChecker constructs a checker for the given ledger.
func newChecker(ledger *ast.Ledger) *checker {
	return &checker{ledger: ledger}
}

// run executes all validation passes and returns collected errors.
func (c *checker) run() []Error {
	return nil
}
