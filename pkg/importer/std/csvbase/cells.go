package csvbase

import "github.com/yugui/go-beancount/pkg/ast"

// Key is an opaque, typed handle to one build step's output, returned by
// AddStep and read with Value. A Key is valid only for Cells produced by the
// same pipeline that created it.
type Key[T any] struct {
	name string
}

// result holds a step's outcome: either a value (as any) or a soft-fail
// diagnostic.
type result struct {
	value any
	diag  *ast.Diagnostic
}

// Cells holds one row's raw fields and the values computed by the pipeline's
// steps so far. Step eval functions and the emit callback read it; it is
// created fresh per row and is not safe for concurrent use.
type Cells struct {
	fields  []string
	index   map[string]int
	info    RowInfo
	results map[string]result
}

// Value returns the value stored for k. It returns (value, nil) when the step
// succeeded, (zero, diag) when the step soft-failed, and (zero, nil) when k
// was not produced by this pipeline.
func Value[T any](c *Cells, k Key[T]) (T, *ast.Diagnostic) {
	r, ok := c.results[k.name]
	if !ok {
		var zero T
		return zero, nil
	}
	if r.diag != nil {
		var zero T
		return zero, r.diag
	}
	v, _ := r.value.(T)
	return v, nil
}

// Field returns the raw cell named col, or "" when the column is absent or the
// row is too short. It does not trim; callers trim as needed.
func (c *Cells) Field(col string) string {
	return fieldAt(c.fields, c.index, col)
}

// Info returns the row's Path, Line, and Hints.
func (c *Cells) Info() RowInfo {
	return c.info
}
