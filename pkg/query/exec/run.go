package exec

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/table"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// Run executes the plan and returns the result rows. Every buffer, group
// map, and accumulator is allocated locally, so Run neither mutates the
// [Compiled] nor the ledger and may run concurrently with other Run calls on
// the same plan (Decision 6). It checks ctx once per input row and returns
// ctx.Err() on cancellation.
//
// The pipeline is: predicate filter → (group+aggregate → HAVING filter | row
// projection) → DISTINCT → ORDER BY → LIMIT.
//
// PARALLEL-EXECUTOR SEAM (not built): the input-row scan below is the single
// point to partition. A future parallel executor would split table.Rows into
// shards, run the filter+projection (or per-group accumulators) per shard on
// its own goroutine, then merge: aggregate partials via Accumulator.Merge
// (the law Add-then-Merge ≡ Add-all makes this exact), and scalar outputs by
// a stable concat. DISTINCT, ORDER BY, and LIMIT then run on the merged rows
// exactly as below. Nothing downstream of this scan needs to change. The one
// caveat is the running balance: it is scan-order-dependent (each selected row
// folds into a single inventory), so a sharded executor must preserve input
// order when reconstructing the balance values.
func (c *Compiled) Run(ctx context.Context) ([][]types.Value, error) {
	var (
		rows [][]types.Value
		keys [][]types.Value
		err  error
	)
	if c.aggregate {
		rows, keys, err = c.runAggregate(ctx)
	} else {
		rows, keys, err = c.runScalar(ctx)
	}
	if err != nil {
		return nil, err
	}

	rows, keys = c.applyDistinct(rows, keys)
	c.applyOrderBy(rows, keys)
	rows = c.applyLimit(rows)
	return rows, nil
}

// runScalar projects each passing row. It returns the output rows and their
// parallel ORDER BY sort keys.
func (c *Compiled) runScalar(ctx context.Context) (rows, keys [][]types.Value, err error) {
	ectx := c.newEvalCtx()
	for row := range c.tbl.Rows() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		ectx.row = row
		pass, err := c.passes(ectx)
		if err != nil {
			return nil, nil, err
		}
		if !pass {
			continue
		}
		if err := c.accumulateBalance(ectx); err != nil {
			return nil, nil, err
		}
		out, err := evalRow(c.targets, ectx)
		if err != nil {
			return nil, nil, err
		}
		key, err := evalRow(orderExprs(c.orderBy), ectx)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, out)
		keys = append(keys, key)
	}
	return rows, keys, nil
}

// group holds the per-group accumulators (one per aggregate slot) and a
// representative row used to evaluate group-key columns at projection time.
type group struct {
	accs []api.Accumulator
	rep  table.Row
}

// runAggregate folds passing rows into groups keyed by the GROUP BY values,
// then projects one output row per group in first-seen order, dropping groups
// rejected by HAVING. With aggregate targets (or a bare HAVING) but no GROUP BY
// it produces exactly one group (one output row even over zero rows, subject to
// HAVING), matching SQL "SELECT count(*) FROM empty".
func (c *Compiled) runAggregate(ctx context.Context) (rows, keys [][]types.Value, err error) {
	groups := map[string]*group{}
	var order []string

	ectx := c.newEvalCtx()
	for row := range c.tbl.Rows() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		ectx.row = row
		pass, err := c.passes(ectx)
		if err != nil {
			return nil, nil, err
		}
		if !pass {
			continue
		}
		if err := c.accumulateBalance(ectx); err != nil {
			return nil, nil, err
		}

		keyVals, err := evalRow(c.groupBy, ectx)
		if err != nil {
			return nil, nil, err
		}
		k := groupKey(keyVals)
		g := groups[k]
		if g == nil {
			g = &group{accs: c.newAccumulators(), rep: row}
			groups[k] = g
			order = append(order, k)
		}
		if err := c.addToSlots(g.accs, ectx); err != nil {
			return nil, nil, err
		}
	}

	if len(order) == 0 && len(c.groupBy) == 0 {
		order = append(order, "")
		groups[""] = &group{accs: c.newAccumulators()}
	}

	for _, k := range order {
		out, key, keep, err := c.projectGroup(groups[k], ectx)
		if err != nil {
			return nil, nil, err
		}
		if !keep {
			continue
		}
		rows = append(rows, out)
		keys = append(keys, key)
	}
	return rows, keys, nil
}

// projectGroup finalizes one group's accumulators and projects its output row
// and ORDER BY keys. keep is false when the HAVING predicate does not evaluate
// to TRUE (NULL/FALSE excluded), in which case out and key are nil and the
// group is dropped. HAVING is evaluated before projection so a filtered group
// costs no target/ORDER BY work.
func (c *Compiled) projectGroup(g *group, ectx *evalCtx) (out, key []types.Value, keep bool, err error) {
	results := make([]types.Value, len(c.slots))
	for i, acc := range g.accs {
		v, err := acc.Result()
		if err != nil {
			return nil, nil, false, err
		}
		results[i] = v
	}
	ectx.row = g.rep
	ectx.aggResults = results
	defer func() { ectx.aggResults = nil }()

	pass, err := c.groupPasses(ectx)
	if err != nil {
		return nil, nil, false, err
	}
	if !pass {
		return nil, nil, false, nil
	}

	out, err = evalRow(c.targets, ectx)
	if err != nil {
		return nil, nil, false, err
	}
	key, err = evalRow(orderExprs(c.orderBy), ectx)
	if err != nil {
		return nil, nil, false, err
	}
	return out, key, true, nil
}

// groupPasses reports whether the current group satisfies the HAVING
// predicate. Like WHERE, a group passes ONLY when HAVING evaluates to TRUE;
// NULL and FALSE are excluded. A nil HAVING passes every group. It must be
// called with ectx.aggResults set to the group's finalized results.
func (c *Compiled) groupPasses(ectx *evalCtx) (bool, error) {
	if c.having == nil {
		return true, nil
	}
	v, err := c.having.eval(ectx)
	if err != nil {
		return false, err
	}
	b, ok := types.AsBool(v)
	return ok && b, nil
}

func (c *Compiled) newAccumulators() []api.Accumulator {
	accs := make([]api.Accumulator, len(c.slots))
	for i, s := range c.slots {
		accs[i] = s.newAcc()
	}
	return accs
}

func (c *Compiled) addToSlots(accs []api.Accumulator, ectx *evalCtx) error {
	for i, s := range c.slots {
		args, err := evalRow(s.args, ectx)
		if err != nil {
			return err
		}
		if err := accs[i].Add(args); err != nil {
			return err
		}
	}
	return nil
}

// newEvalCtx allocates a fresh per-Run evaluation context, including the
// running-balance inventory when the query reads the balance column. All state
// is local to one Run, so concurrent Runs over one plan never share it
// (Decision 6).
func (c *Compiled) newEvalCtx() *evalCtx {
	ectx := &evalCtx{qctx: c.qctx}
	if c.usesBalance {
		ectx.balance = inventory.NewInventory()
	}
	return ectx
}

// accumulateBalance folds the current (already predicate-passing) row's
// position into the running balance, so a balanceExpr evaluated for this row
// sees the cumulative inventory of the selected rows up to and including it.
// It is a no-op unless the query reads the balance column, and returns a
// non-nil error only on an apd arithmetic failure (unreachable from booked
// input, but surfaced rather than silently corrupting the running total).
func (c *Compiled) accumulateBalance(ectx *evalCtx) error {
	if !c.usesBalance || c.balancePos == nil {
		return nil
	}
	if pos, ok := types.AsPosition(c.balancePos(ectx.row)); ok {
		return ectx.balance.Add(pos)
	}
	return nil
}

// passes reports whether the current row satisfies the predicate. A row
// passes ONLY when the predicate evaluates to TRUE; NULL and FALSE are
// excluded (standard SQL WHERE). A nil predicate passes every row.
func (c *Compiled) passes(ectx *evalCtx) (bool, error) {
	if c.predicate == nil {
		return true, nil
	}
	v, err := c.predicate.eval(ectx)
	if err != nil {
		return false, err
	}
	b, ok := types.AsBool(v)
	return ok && b, nil
}

func evalRow(exprs []cexpr, ectx *evalCtx) ([]types.Value, error) {
	out := make([]types.Value, len(exprs))
	for i, e := range exprs {
		v, err := e.eval(ectx)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func orderExprs(keys []orderKey) []cexpr {
	if len(keys) == 0 {
		return nil
	}
	out := make([]cexpr, len(keys))
	for i, k := range keys {
		out[i] = k.expr
	}
	return out
}

// groupKey builds a stable, collision-free string key from the group's key
// values via each value's Format, length-prefixed so distinct tuples never
// alias.
func groupKey(vals []types.Value) string {
	var b strings.Builder
	for _, v := range vals {
		s := v.Format()
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte('|')
	}
	return b.String()
}

// applyDistinct removes duplicate output rows by true value equality (every
// column compares 0 via types.Compare, with NULL == NULL). It preserves
// first-seen order and keeps each row's parallel sort key. DISTINCT runs
// before the final ORDER BY.
func (c *Compiled) applyDistinct(rows, keys [][]types.Value) (outRows, outKeys [][]types.Value) {
	if !c.distinct || len(rows) == 0 {
		return rows, keys
	}
	outRows = rows[:0:0]
	outKeys = keys[:0:0]
	for i, r := range rows {
		dup := false
		for _, kept := range outRows {
			if rowsEqual(r, kept) {
				dup = true
				break
			}
		}
		if !dup {
			outRows = append(outRows, r)
			outKeys = append(outKeys, keys[i])
		}
	}
	return outRows, outKeys
}

func rowsEqual(a, b []types.Value) bool {
	for i := range a {
		if a[i].Compare(b[i]) != 0 {
			return false
		}
	}
	return true
}

// applyOrderBy sorts rows in place by their parallel sort keys, stably, using
// types.Compare (NULL last in ASC) with each item's direction negating the
// comparison. Without ORDER BY the order is the deterministic first-seen
// order from the scan (documented in doc.go).
func (c *Compiled) applyOrderBy(rows, keys [][]types.Value) {
	if len(c.orderBy) == 0 {
		return
	}
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		for j, ok := range c.orderBy {
			c := keys[a][j].Compare(keys[b][j])
			if ok.desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	})
	applyPermutation(rows, idx)
	applyPermutation(keys, idx)
}

func applyPermutation(rows [][]types.Value, idx []int) {
	reordered := make([][]types.Value, len(rows))
	for i, j := range idx {
		reordered[i] = rows[j]
	}
	copy(rows, reordered)
}

func (c *Compiled) applyLimit(rows [][]types.Value) [][]types.Value {
	if c.limit == nil {
		return rows
	}
	n := *c.limit
	if n < 0 {
		n = 0
	}
	if int64(len(rows)) <= n {
		return rows
	}
	return rows[:n]
}
