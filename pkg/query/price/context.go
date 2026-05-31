package price

import "github.com/yugui/go-beancount/pkg/ast"

// QueryContext is the query-wide, init-time, immutable context injected into
// BQL scalar function evaluation. It is built once per compiled query from the
// loaded ledger and shared read-only across concurrent Runs (Decision 6).
//
// Today it carries the price [Map]; the struct is the seam for future
// init-time directive maps (open/close, commodity metadata) and options. A
// context-free function receives it and ignores it (see api.Pure).
type QueryContext struct {
	Prices *Map
}

// NewQueryContext builds the context for a query over ledger. The price map is
// constructed lazily on first use, so this call is cheap regardless of ledger
// size.
func NewQueryContext(ledger *ast.Ledger) *QueryContext {
	return &QueryContext{Prices: NewMap(ledger)}
}
