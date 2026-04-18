package validation

import (
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// Check runs all semantic validations on the given ledger and returns any
// errors found.
func Check(ledger *ast.Ledger) []Error {
	return newChecker(ledger).run()
}

// checker holds the state for a single validation pass.
type checker struct {
	ledger      *ast.Ledger
	accounts    map[ast.Account]*accountstate.State
	balances    map[balanceKey]*apd.Decimal
	pendingPads map[ast.Account]*pendingPad
	options     *options.Values
	errors      []Error
}

// newChecker constructs a checker for the given ledger.
func newChecker(ledger *ast.Ledger) *checker {
	return &checker{
		ledger:      ledger,
		accounts:    make(map[ast.Account]*accountstate.State),
		balances:    make(map[balanceKey]*apd.Decimal),
		pendingPads: make(map[ast.Account]*pendingPad),
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
	c.collectOptions()
	// ast.Ledger already keeps directives in canonical chronological
	// order; walk them directly via the Ledger iterator.
	for _, d := range c.ledger.All() {
		switch d := d.(type) {
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
	sort.SliceStable(c.errors, func(i, j int) bool {
		ai, aj := c.errors[i].Span.Start, c.errors[j].Span.Start
		if ai.Filename != aj.Filename {
			return ai.Filename < aj.Filename
		}
		if ai.Offset != aj.Offset {
			return ai.Offset < aj.Offset
		}
		return c.errors[i].Code < c.errors[j].Code
	})
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

// visitOption is a no-op; option directives are consumed by the
// collectOptions pre-pass before directive walking begins.
func (c *checker) visitOption(*ast.Option) {}

// collectOptions pre-walks the ledger to parse option directives.
// Invalid values emit CodeInvalidOption; unknown keys are silently ignored.
func (c *checker) collectOptions() {
	values, parseErrs := options.Parse(c.ledger)
	c.options = values
	for _, pe := range parseErrs {
		c.emit(Error{
			Code:    CodeInvalidOption,
			Span:    pe.Span,
			Message: fmt.Sprintf("invalid option %q: %v", pe.Key, pe.Err),
		})
	}
}

// visitPlugin is a stub; plugins are not executed by the validator.
func (c *checker) visitPlugin(*ast.Plugin) {}

// visitInclude is a stub; include directives are resolved during loading.
func (c *checker) visitInclude(*ast.Include) {}
