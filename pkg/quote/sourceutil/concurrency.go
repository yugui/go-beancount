package sourceutil

import (
	"context"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// Concurrency caps the number of in-flight calls to s at n. Every
// invocation of QuoteLatest / QuoteAt / QuoteRange acquires one slot of
// a semaphore of size n before delegating to the wrapped source and
// releases it on return. A non-positive n is treated as 1 (effectively
// serial).
//
// The wrapper preserves whichever Capability sub-interfaces s
// implements: if s is both a LatestSource and an AtSource, the returned
// value satisfies both. Use a type assertion to recover the desired
// sub-interface from the returned api.Source.
//
// # Goroutine safety
//
// The returned source is safe for concurrent use by multiple goroutines
// provided s is itself safe for concurrent use. Concurrency intentionally
// permits up to n concurrent calls to s.
//
// # Stacking
//
// Concurrency composes freely with the other decorators in this
// package; a typical stack is Cache(RateLimit(RetryOnError(Concurrency
// (source)))). Place Concurrency innermost when the underlying source
// has a hard parallel-connection limit, so retries and rate-limited
// waits do not themselves consume connection slots.
func Concurrency(s api.Source, n int) api.Source {
	if n <= 0 {
		n = 1
	}
	sem := make(chan struct{}, n)
	return wrapSource(s, &concurrencyHook{sem: sem})
}

// concurrencyHook implements callHook by acquiring a semaphore slot
// before each call and releasing it after.
type concurrencyHook struct {
	sem chan struct{}
}

func (h *concurrencyHook) before(ctx context.Context) (release func(), err error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// callHook is the abstraction that Concurrency, RateLimit, and
// RetryOnError use to plug their behaviour into the generic wrapped-
// source machinery.
type callHook interface {
	// before is called immediately before the wrapped source's method.
	// If err is non-nil the wrapped method is not invoked. Otherwise
	// release is called when the method returns.
	before(ctx context.Context) (release func(), err error)
}

// wrapSource builds a wrapper around s that re-satisfies whichever
// sub-interfaces s implements, routing each call through hook.before
// and the corresponding release. A nil hook is a no-op.
func wrapSource(s api.Source, hook callHook) api.Source {
	w := &wrappedSource{base: s, hook: hook}
	if l, ok := s.(api.LatestSource); ok {
		w.latest = l
	}
	if a, ok := s.(api.AtSource); ok {
		w.at = a
	}
	if r, ok := s.(api.RangeSource); ok {
		w.rng = r
	}
	return w.asSource()
}

// wrappedSource holds whichever sub-interfaces the wrapped source
// satisfies. Its asSource method returns a value that re-satisfies the
// same set, so callers can recover the original interface set with a
// type assertion.
type wrappedSource struct {
	base   api.Source
	hook   callHook
	latest api.LatestSource
	at     api.AtSource
	rng    api.RangeSource
}

func (w *wrappedSource) Name() string                   { return w.base.Name() }
func (w *wrappedSource) Capabilities() api.Capabilities { return w.base.Capabilities() }

func (w *wrappedSource) call(ctx context.Context, fn func(context.Context) ([]ast.Price, []ast.Diagnostic, error)) ([]ast.Price, []ast.Diagnostic, error) {
	if w.hook == nil {
		return fn(ctx)
	}
	release, err := w.hook.before(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	return fn(ctx)
}

func (w *wrappedSource) doLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return w.call(ctx, func(ctx context.Context) ([]ast.Price, []ast.Diagnostic, error) {
		return w.latest.QuoteLatest(ctx, q)
	})
}

func (w *wrappedSource) doAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return w.call(ctx, func(ctx context.Context) ([]ast.Price, []ast.Diagnostic, error) {
		return w.at.QuoteAt(ctx, q, at)
	})
}

func (w *wrappedSource) doRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return w.call(ctx, func(ctx context.Context) ([]ast.Price, []ast.Diagnostic, error) {
		return w.rng.QuoteRange(ctx, q, start, end)
	})
}

// asSource returns w typed against whichever sub-interface combination
// the wrapped source satisfies. The orchestrator (and other callers)
// recovers the appropriate sub-interface with a type assertion on the
// returned api.Source.
func (w *wrappedSource) asSource() api.Source {
	hasL := w.latest != nil
	hasA := w.at != nil
	hasR := w.rng != nil
	switch {
	case hasL && hasA && hasR:
		return latestAtRange{w}
	case hasL && hasA:
		return latestAt{w}
	case hasL && hasR:
		return latestRange{w}
	case hasA && hasR:
		return atRange{w}
	case hasL:
		return latestOnly{w}
	case hasA:
		return atOnly{w}
	case hasR:
		return rangeOnly{w}
	default:
		return baseOnly{w}
	}
}

// The eight permutations below exist so that wrapSource's return value
// satisfies exactly the sub-interface set of the wrapped source.

type baseOnly struct{ *wrappedSource }

type latestOnly struct{ *wrappedSource }

func (l latestOnly) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return l.wrappedSource.doLatest(ctx, q)
}

type atOnly struct{ *wrappedSource }

func (a atOnly) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return a.wrappedSource.doAt(ctx, q, at)
}

type rangeOnly struct{ *wrappedSource }

func (r rangeOnly) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.wrappedSource.doRange(ctx, q, start, end)
}

type latestAt struct{ *wrappedSource }

func (la latestAt) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return la.wrappedSource.doLatest(ctx, q)
}
func (la latestAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return la.wrappedSource.doAt(ctx, q, at)
}

type latestRange struct{ *wrappedSource }

func (lr latestRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return lr.wrappedSource.doLatest(ctx, q)
}
func (lr latestRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return lr.wrappedSource.doRange(ctx, q, start, end)
}

type atRange struct{ *wrappedSource }

func (ar atRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return ar.wrappedSource.doAt(ctx, q, at)
}
func (ar atRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return ar.wrappedSource.doRange(ctx, q, start, end)
}

type latestAtRange struct{ *wrappedSource }

func (lar latestAtRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return lar.wrappedSource.doLatest(ctx, q)
}
func (lar latestAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return lar.wrappedSource.doAt(ctx, q, at)
}
func (lar latestAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return lar.wrappedSource.doRange(ctx, q, start, end)
}
