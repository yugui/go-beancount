package csvbase

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
)

// RowInfo is the row metadata exposed to step eval functions and the emit
// callback: the display path, 1-based source line, and caller Hints (may be nil).
type RowInfo struct {
	Path  string
	Line  int
	Hints map[string]string
}

// EmitFunc turns a row's resolved cells into directives plus diagnostics. Its
// return encodes every per-row disposition, identical to RowMapper.Map:
// ([d],nil,nil) emit; (nil,nil,nil) skip; (nil,[err],nil) drop-with-diag;
// ([d],[warn],nil) emit+warn; (_,_,err) fatal. Row metadata is available via
// c.Info(). It must be safe for concurrent use (called once per row, possibly
// from concurrent Extract calls).
type EmitFunc func(ctx context.Context, c *MappingState) ([]ast.Directive, []ast.Diagnostic, error)

// step is an internal build step: an eval function over any-typed results.
type step struct {
	name string
	eval func(c *MappingState) (any, *ast.Diagnostic, error)
}

// Builder accumulates build steps and required columns for one Pipeline. It is
// used at construction time only and is NOT safe for concurrent use. Emit
// freezes it into a Pipeline.
type Builder struct {
	steps    []step
	required []string
	seen     map[string]struct{} // dedup for required
	counter  int
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{seen: make(map[string]struct{})}
}

// Require records header columns that must be present for the pipeline to run.
// Duplicates are deduplicated; insertion order is preserved.
func (b *Builder) Require(cols ...string) {
	for _, col := range cols {
		if _, ok := b.seen[col]; ok {
			continue
		}
		b.seen[col] = struct{}{}
		b.required = append(b.required, col)
	}
}

// AddStep registers a build step and returns a typed Key for its output. eval
// is run once per row, in the order steps were added; it reads earlier steps
// via Value and raw cells via the MappingState accessors. eval returns (value,
// nil, nil) on success, (zero, diag, nil) to soft-fail (attach diag to this
// key), or (_, _, err) to fail the whole row mapping (fatal). eval must be
// safe for concurrent use. This is the generic extension point on which
// standard and third-party steps are built.
func AddStep[T any](b *Builder, eval func(c *MappingState) (T, *ast.Diagnostic, error)) Key[T] {
	b.counter++
	name := fmt.Sprintf("step-%d", b.counter)
	k := Key[T]{name: name}
	b.steps = append(b.steps, step{
		name: name,
		eval: func(c *MappingState) (any, *ast.Diagnostic, error) {
			v, diag, err := eval(c)
			if err != nil {
				return nil, nil, err
			}
			return v, diag, nil
		},
	})
	return k
}

// Emit freezes the Builder's steps and required columns and returns a Pipeline
// that calls emit after evaluating all steps. The returned Pipeline is
// immutable; later mutation of the Builder does not affect it. emit must be
// non-nil; Emit panics if emit is nil.
func (b *Builder) Emit(emit EmitFunc) *Pipeline {
	if emit == nil {
		panic("csvbase: Builder.Emit called with nil emit")
	}
	steps := make([]step, len(b.steps))
	copy(steps, b.steps)
	required := make([]string, len(b.required))
	copy(required, b.required)
	return &Pipeline{steps: steps, required: required, emit: emit}
}

// Pipeline is a RowMapper that evaluates a fixed sequence of build steps into
// a MappingState, then calls its emit callback. It is immutable and safe for
// concurrent use (each Map call uses its own MappingState).
type Pipeline struct {
	steps    []step
	required []string
	emit     EmitFunc
}

var _ RowMapper = (*Pipeline)(nil)

// Required returns the header columns the pipeline needs. The returned slice
// is a fresh copy the caller may modify.
func (p *Pipeline) Required() []string {
	out := make([]string, len(p.required))
	copy(out, p.required)
	return out
}

// Map evaluates the steps for rec and returns the emit callback's result. A
// step returning a hard error stops evaluation and returns that error (emit is
// not called); soft-failed steps store their diagnostic for the emit callback
// to act on.
func (p *Pipeline) Map(ctx context.Context, rec RowContext) ([]ast.Directive, []ast.Diagnostic, error) {
	c := &MappingState{
		raw:     rec.Fields,
		index:   rec.Index,
		info:    RowInfo{Path: rec.Path, Line: rec.Line, Hints: rec.Hints},
		results: make(map[string]result, len(p.steps)),
	}
	for _, s := range p.steps {
		v, diag, err := s.eval(c)
		if err != nil {
			return nil, nil, err
		}
		c.results[s.name] = result{value: v, diag: diag}
	}
	return p.emit(ctx, c)
}
