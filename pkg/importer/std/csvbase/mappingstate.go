package csvbase

import "github.com/yugui/go-beancount/pkg/ast"

// Key is an opaque, typed handle to one build step's output, returned by
// AddStep and read with Value. A Key is valid only for MappingState produced
// by the same pipeline that created it.
type Key[T any] struct {
	name string
}

// result holds a step's outcome: either a value (as any) or a soft-fail
// diagnostic.
type result struct {
	value any
	diag  *ast.Diagnostic
}

// MappingState holds one row's raw fields and the values computed by the
// pipeline's steps so far. Step eval functions and the emit callback read it;
// it is created fresh per row and is not safe for concurrent use.
type MappingState struct {
	raw      []string
	index    map[string]int
	info     RowInfo
	results  map[string]result
	warnings []ast.Diagnostic
}

// Warn records d as a non-fatal diagnostic, surfaced alongside the row's
// emitted directives. Unlike a step soft-fail (which drops the value), Warn
// keeps the row; use it when a step succeeds with a caveat, e.g. a counter
// account that could not be resolved. Recorded warnings precede the emit
// callback's own diagnostics in the row's reported result.
func (c *MappingState) Warn(d ast.Diagnostic) {
	c.warnings = append(c.warnings, d)
}

// Value returns the value stored for k. It returns (value, nil) when the step
// succeeded, (zero, diag) when the step soft-failed, and (zero, nil) when k
// was not produced by this pipeline.
func Value[T any](c *MappingState, k Key[T]) (T, *ast.Diagnostic) {
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

// At returns the raw cell named col, or "" when the column is absent or the
// row is too short. It does not trim; callers trim as needed.
//
// Per the leaf-only invariant, only Column (and leaf wrappers around it) calls
// At directly; all standard resolver steps read prior outputs via Value instead.
func (c *MappingState) At(col string) string {
	return fieldAt(c.raw, c.index, col)
}

// Info returns the row's Path, Line, and Hints.
func (c *MappingState) Info() RowInfo {
	return c.info
}

// Row returns a fresh map of every indexed column name to its raw cell value
// ("" for columns past a short row).
//
// Per the leaf-only invariant, only the Row leaf step calls this directly (to
// supply the full data map to resolver steps such as Template); all other
// standard steps read prior outputs via Value instead.
func (c *MappingState) Row() map[string]string {
	m := make(map[string]string, len(c.index))
	for name, i := range c.index {
		if i < len(c.raw) {
			m[name] = c.raw[i]
		} else {
			m[name] = ""
		}
	}
	return m
}
