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
	ledger   *ast.Ledger
	accounts map[string]*accountState
	errors   []Error
}

// newChecker constructs a checker for the given ledger.
func newChecker(ledger *ast.Ledger) *checker {
	return &checker{
		ledger:   ledger,
		accounts: make(map[string]*accountState),
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
	return c.errors
}

// visitCommodity is a stub; commodity directives have no cross-directive checks yet.
func (c *checker) visitCommodity(*ast.Commodity) {}

// visitBalance verifies the asserted account is open on the balance date.
// Arithmetic verification of the assertion is performed in a later step.
func (c *checker) visitBalance(d *ast.Balance) {
	c.requireOpen(d.Account, d.Date, d.Span, d.Amount.Currency)
}

// visitPad verifies that both the target and source accounts are open on the
// pad date. The actual padding is resolved in a later step.
func (c *checker) visitPad(d *ast.Pad) {
	c.requireOpen(d.Account, d.Date, d.Span, "")
	c.requireOpen(d.PadAccount, d.Date, d.Span, "")
}

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
// transaction date and that any specified currency is allowed.
func (c *checker) visitTransaction(d *ast.Transaction) {
	for i := range d.Postings {
		p := &d.Postings[i]
		span := p.Span
		if span == (ast.Span{}) {
			span = d.Span
		}
		currency := ""
		if p.Amount != nil {
			currency = p.Amount.Currency
		}
		c.requireOpen(p.Account, d.Date, span, currency)
	}
}

// visitCustom is a stub; custom assertions are not evaluated here.
func (c *checker) visitCustom(*ast.Custom) {}

// visitOption is a stub; options are processed elsewhere.
func (c *checker) visitOption(*ast.Option) {}

// visitPlugin is a stub; plugins are not executed by the validator.
func (c *checker) visitPlugin(*ast.Plugin) {}

// visitInclude is a stub; include directives are resolved during loading.
func (c *checker) visitInclude(*ast.Include) {}
