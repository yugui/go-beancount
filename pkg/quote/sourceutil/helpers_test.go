package sourceutil

import (
	"context"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// fakeAt is a configurable api.AtSource for tests.
type fakeAt struct {
	name   string
	handle func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error)
}

func (f *fakeAt) Name() string { return f.name }
func (f *fakeAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handle(ctx, q, at)
}

// fakeLatest is a configurable api.LatestSource for tests.
type fakeLatest struct {
	name   string
	handle func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
}

func (f *fakeLatest) Name() string { return f.name }
func (f *fakeLatest) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handle(ctx, q)
}

// fakeRange is a configurable api.RangeSource for tests.
type fakeRange struct {
	name   string
	handle func(context.Context, []api.SourceQuery, time.Time, time.Time) ([]ast.Price, []ast.Diagnostic, error)
}

func (f *fakeRange) Name() string { return f.name }
func (f *fakeRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handle(ctx, q, start, end)
}

// fakeLatestAt is a fake implementing both Latest and At sub-interfaces.
type fakeLatestAt struct {
	name         string
	handleAt     func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error)
	handleLatest func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
}

func (f *fakeLatestAt) Name() string { return f.name }
func (f *fakeLatestAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handleAt(ctx, q, at)
}
func (f *fakeLatestAt) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return f.handleLatest(ctx, q)
}

// utcDate returns 0:00 UTC of the given calendar date.
func utcDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
