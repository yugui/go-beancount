package exec

import (
	"github.com/yugui/go-beancount/pkg/query/api"
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

	orderBy  []orderKey
	distinct bool
	limit    *int64
}

// Columns returns the output schema fixed at compile time, available before
// Run. The returned slice must not be mutated.
func (c *Compiled) Columns() []OutColumn { return c.cols }
