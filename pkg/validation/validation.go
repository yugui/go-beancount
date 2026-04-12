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
	if c.ledger == nil {
		return nil
	}
	ordered := orderDirectives(c.ledger)
	for _, od := range ordered {
		switch d := od.dir.(type) {
		case *ast.Open:
			c.visitOpen(d)
		case *ast.Balance:
			c.visitBalance(d)
		case *ast.Pad:
			c.visitPad(d)
		case *ast.Transaction:
			c.visitTransaction(d)
		case *ast.Note:
			c.visitNote(d)
		case *ast.Document:
			c.visitDocument(d)
		case *ast.Event:
			c.visitEvent(d)
		case *ast.Commodity:
			c.visitCommodity(d)
		case *ast.Query:
			c.visitQuery(d)
		case *ast.Custom:
			c.visitCustom(d)
		case *ast.Close:
			c.visitClose(d)
		case *ast.Price:
			c.visitPrice(d)
		case *ast.Option:
			c.visitOption(d)
		case *ast.Plugin:
			c.visitPlugin(d)
		case *ast.Include:
			c.visitInclude(d)
		}
	}
	return nil
}

// Visitor methods — all no-ops for now. Future steps will fill these in
// with the actual semantic checks.

func (c *checker) visitOpen(*ast.Open)               {}
func (c *checker) visitClose(*ast.Close)             {}
func (c *checker) visitCommodity(*ast.Commodity)     {}
func (c *checker) visitBalance(*ast.Balance)         {}
func (c *checker) visitPad(*ast.Pad)                 {}
func (c *checker) visitNote(*ast.Note)               {}
func (c *checker) visitDocument(*ast.Document)       {}
func (c *checker) visitEvent(*ast.Event)             {}
func (c *checker) visitQuery(*ast.Query)             {}
func (c *checker) visitPrice(*ast.Price)             {}
func (c *checker) visitTransaction(*ast.Transaction) {}
func (c *checker) visitCustom(*ast.Custom)           {}
func (c *checker) visitOption(*ast.Option)           {}
func (c *checker) visitPlugin(*ast.Plugin)           {}
func (c *checker) visitInclude(*ast.Include)         {}
