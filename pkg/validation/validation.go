// Package validation performs semantic checks on a loaded beancount ledger.
package validation

import (
	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// Check runs all semantic validations on the given ledger and returns any
// errors found.
func Check(ledger *ast.Ledger) []Error {
	return newChecker(ledger).run()
}

// checker holds the state for a single validation pass.
type checker struct {
	ledger      *ast.Ledger
	accounts    map[string]*accountState
	balances    map[balanceKey]*apd.Decimal
	pendingPads map[string]*pendingPad
	errors      []Error
}

// newChecker constructs a checker for the given ledger.
func newChecker(ledger *ast.Ledger) *checker {
	return &checker{
		ledger:      ledger,
		accounts:    make(map[string]*accountState),
		balances:    make(map[balanceKey]*apd.Decimal),
		pendingPads: make(map[string]*pendingPad),
	}
}

// emit records a validation error.
func (c *checker) emit(e Error) {
	c.errors = append(c.errors, e)
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
	c.reportUnresolvedPads()
	return c.errors
}

// visitCommodity is a stub; commodity directives have no cross-directive checks yet.
func (c *checker) visitCommodity(*ast.Commodity) {}

// visitNote verifies the referenced account is open on the note date.
func (c *checker) visitNote(d *ast.Note) {
	c.requireOpen(d.Account, d.Date, d.Span, "")
}

// visitDocument verifies the referenced account is open on the document date.
func (c *checker) visitDocument(d *ast.Document) {
	c.requireOpen(d.Account, d.Date, d.Span, "")
}

// visitEvent is a stub; event directives have no cross-directive checks yet.
func (c *checker) visitEvent(*ast.Event) {}

// visitQuery is a stub; query directives are not validated here.
func (c *checker) visitQuery(*ast.Query) {}

// visitPrice is a stub; price directives have no cross-directive checks yet.
func (c *checker) visitPrice(*ast.Price) {}

// visitTransaction verifies every posting's account is open on the
// transaction date, that any specified currency is allowed, and that the
// postings balance per currency (with at most one auto-computed posting).
func (c *checker) visitTransaction(d *ast.Transaction) {
	c.checkBalance(d)
}

// visitCustom dispatches the custom directive to the registered handler, if
// any. Unknown custom types are silently ignored for forward compatibility.
func (c *checker) visitCustom(d *ast.Custom) {
	handler, ok := customAssertions[d.TypeName]
	if !ok {
		return
	}
	state := &State{c: c}
	for _, e := range handler.Evaluate(state, d) {
		c.emit(e)
	}
}

// visitOption is a stub; options are processed elsewhere.
func (c *checker) visitOption(*ast.Option) {}

// visitPlugin is a stub; plugins are not executed by the validator.
func (c *checker) visitPlugin(*ast.Plugin) {}

// visitInclude is a stub; include directives are resolved during loading.
func (c *checker) visitInclude(*ast.Include) {}
