package query

import (
	"context"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/exec"
	"github.com/yugui/go-beancount/pkg/query/parser"
)

// Compiled is an immutable, executable BQL query bound to one ledger. It is
// produced by [Compile] and carries no per-execution state, so a single
// *Compiled may be run concurrently from many goroutines over one shared,
// immutable [ast.Ledger] with no locking; each [Compiled.Run] allocates its
// own buffers and never mutates the plan or the ledger.
type Compiled struct {
	plan *exec.Compiled
}

// Compile parses query and compiles it against ledger, returning an
// executable plan whose output schema is available immediately via
// [Compiled.Columns]. It returns a positioned error (never a panic) on a
// parse failure or any compile failure: unknown column or table, operator
// type mismatch, missing or ambiguous function overload, a misplaced or
// nested aggregate, or a non-boolean predicate. ledger is held by reference
// and never mutated.
func Compile(query string, ledger *ast.Ledger) (*Compiled, error) {
	sel, err := parser.Parse(query)
	if err != nil {
		return nil, err
	}
	plan, err := exec.Compile(sel, ledger)
	if err != nil {
		return nil, err
	}
	return &Compiled{plan: plan}, nil
}

// Columns returns the output schema fixed at compile time. The returned
// slice must not be mutated.
func (c *Compiled) Columns() []Column {
	return toColumns(c.plan.Columns())
}

// Run executes the query and returns its rows. It honors ctx, returning
// ctx.Err() if the context is cancelled during execution. Run is safe to
// call concurrently on one *Compiled; it allocates all execution state
// locally and never mutates the plan or the ledger.
func (c *Compiled) Run(ctx context.Context) (Result, error) {
	rows, err := c.plan.Run(ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{Columns: c.Columns(), Rows: rows}, nil
}

// Query compiles query against ledger and runs it under ctx in one call. It
// is the convenience form of [Compile] followed by [Compiled.Run].
func Query(ctx context.Context, query string, ledger *ast.Ledger) (Result, error) {
	c, err := Compile(query, ledger)
	if err != nil {
		return Result{}, err
	}
	return c.Run(ctx)
}

func toColumns(cs []exec.OutColumn) []Column {
	out := make([]Column, len(cs))
	for i, c := range cs {
		out[i] = Column{Name: c.Name, Type: c.Type}
	}
	return out
}
