package query

import "github.com/yugui/go-beancount/pkg/query/types"

// Column is one column of a query's output schema: a display name and the
// static type of its values.
type Column struct {
	Name string
	Type types.Type
}

// Result is the materialized output of a query. Columns is the output
// schema in projection order; each Rows[i] is one result row whose values
// align positionally with Columns. Every value in column j has type
// Columns[j].Type (a NULL is a typed null of that type).
type Result struct {
	Columns []Column
	Rows    [][]types.Value
}
