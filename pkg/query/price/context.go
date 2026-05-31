package price

import (
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/directives"
)

// QueryContext is the query-wide, init-time, immutable context injected into
// BQL scalar function evaluation. It is built once per compiled query from the
// loaded ledger and shared read-only across concurrent Runs (Decision 6).
//
// It bundles the init-time directive maps a scalar function may consult: the
// price [Map] for rate lookups and the directives [directives.Index] for
// account/currency directive context (open/close dates, directive metadata,
// account-type sign/sort-key). The struct is the seam for future additions. A
// context-free function receives it and ignores it (see api.Pure).
type QueryContext struct {
	Prices *Map
	Dirs   *directives.Index
}

// NewQueryContext builds the context for a query over ledger. Both the price
// map and the directive index are constructed lazily on first use, so this
// call is cheap regardless of ledger size. A nil ledger (or nil
// ledger.Options) is safe.
func NewQueryContext(ledger *ast.Ledger) *QueryContext {
	var opts *ast.OptionValues
	if ledger != nil {
		opts = ledger.Options
	}
	return &QueryContext{
		Prices: NewMap(ledger),
		Dirs:   directives.NewIndex(ledger, opts),
	}
}
