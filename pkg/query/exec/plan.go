package exec

import (
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/price"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// OutColumn is one column of a compiled query's output schema: a name and
// the static result type of its projection expression.
type OutColumn struct {
	Name string
	Type types.Type
}

// aggSlot is one distinct aggregate invocation in a query. Each slot owns a
// fresh accumulator per group (built via newAcc) and the compiled scalar
// argument expressions whose values are fed to Accumulator.Add.
type aggSlot struct {
	newAcc api.NewAccumulator
	args   []cexpr
}

// orderKey is one compiled ORDER BY item: the expression whose value sorts
// the output, and the sort direction.
type orderKey struct {
	expr cexpr
	desc bool
}

// Compiled is an immutable, executable query plan produced by [Compile]. It
// holds no per-execution state: [Compiled.Run] allocates every buffer,
// group map, and accumulator locally and never mutates the Compiled or the
// ledger behind its table. Consequently one *Compiled may be run
// concurrently from many goroutines over one shared immutable ledger with
// no locking (Decision 6).
type Compiled struct {
	tbl     *table.Table
	cols    []OutColumn
	targets []cexpr

	predicate cexpr // nil when no FROM-filter and no WHERE

	aggregate bool
	groupBy   []cexpr
	slots     []aggSlot

	// having is the compiled HAVING predicate, evaluated per group after
	// aggregation; nil when absent. A group is emitted only when it evaluates
	// to TRUE (NULL/FALSE are excluded, like WHERE).
	having cexpr

	orderBy  []orderKey
	distinct bool
	limit    *int64

	// usesBalance is set when a target or ORDER BY reads the running-balance
	// column; balancePos is then the table's position accessor, whose value is
	// folded into the running inventory for each selected row.
	usesBalance bool
	balancePos  func(table.Row) types.Value

	// qctx is the query-wide, immutable context built once from the ledger at
	// Compile and shared read-only by every Run (Decision 6).
	qctx *price.QueryContext
}

// Columns returns the output schema fixed at compile time, available before
// Run. The returned slice must not be mutated.
func (c *Compiled) Columns() []OutColumn { return c.cols }
