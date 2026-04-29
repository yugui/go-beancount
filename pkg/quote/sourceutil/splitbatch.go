package sourceutil

import (
	"context"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// SplitBatch caps per-call query count to relieve a Source author
// of the obligation to handle arbitrarily large batches natively.
//
// SplitBatch wraps s so that any inbound Quote* call carrying more
// than n queries is split into batches of at most n and dispatched
// in parallel goroutines. Use this when your source cannot natively
// handle arbitrarily large batches (the Phase 7 obligation on every
// Source implementer; see pkg/quote/api for details).
//
// The returned source preserves whichever sub-interfaces the input
// s declares (LatestSource / AtSource / RangeSource): SplitBatch
// wraps each sub-interface that s implements. The combined
// Capabilities() returns the same Supports* flags as s.
//
// # Goroutine model
//
// One goroutine is spawned per batch; results from completed batches
// are merged on return. To bound parallelism within a single Quote*
// call, stack with Concurrency. A non-positive n is treated as 1
// (effectively serial). If the input query slice is shorter than n,
// only one goroutine runs.
//
// # Error aggregation
//
// A non-nil error from one batch does not fail the whole call: the
// failing batch's diagnostics and prices are still merged in, and
// only context cancellation propagates as a top-level error from
// the Quote* method. Per-query failures the wrapped source already
// encodes as diagnostics flow through unchanged. This matches the
// orchestrator's "diagnostic per failed unit, error only when
// nothing succeeded" contract.
//
// # Context cancellation timing
//
// In-flight batches are not individually cancellable from outside
// the wrapped source. When ctx is cancelled, the Quote* method
// waits for every already-spawned per-batch goroutine to return
// before reporting ctx.Err(); each downstream call receives the
// cancelled ctx and is expected to return promptly, so the wait is
// typically short, but it is bounded by the wrapped source's
// responsiveness rather than by the wrapper itself. Partial results
// from goroutines that completed before cancellation are returned
// alongside ctx.Err(), matching SplitRange and the orchestrator's
// level loop.
//
// # Time-axis arguments
//
// SplitBatch only splits the query axis. The date argument on
// QuoteAt and the (start, end) interval on QuoteRange are passed
// through to every sub-batch unchanged; use SplitRange to cap
// per-call day count for RangeSource.
func SplitBatch(s api.Source, n int) api.Source {
	if n <= 0 {
		n = 1
	}
	w := &splitBatchSource{base: s, n: n}
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

// splitBatchSource holds whichever sub-interfaces the wrapped source
// satisfies. Its asSource method returns a value that re-satisfies
// the same set, so callers can recover the original interface set
// with a type assertion.
type splitBatchSource struct {
	base   api.Source
	n      int
	latest api.LatestSource
	at     api.AtSource
	rng    api.RangeSource
}

func (s *splitBatchSource) Name() string                   { return s.base.Name() }
func (s *splitBatchSource) Capabilities() api.Capabilities { return s.base.Capabilities() }

// chunkRanges returns the list of [lo, hi) index pairs that partition
// [0, len) into chunks of at most s.n.
func (s *splitBatchSource) chunkRanges(length int) [][2]int {
	if length == 0 {
		return nil
	}
	num := (length + s.n - 1) / s.n
	out := make([][2]int, num)
	for i := 0; i < num; i++ {
		lo := i * s.n
		hi := lo + s.n
		if hi > length {
			hi = length
		}
		out[i] = [2]int{lo, hi}
	}
	return out
}

// runChunks fans the chunks out into parallel goroutines, calling fn
// per chunk and merging results. A non-nil err from a chunk is
// converted into a "quote-fetch-error" diagnostic and folded into
// that chunk's diags slice. ctx cancellation propagates as a
// top-level error after all spawned goroutines have returned.
func (s *splitBatchSource) runChunks(ctx context.Context, length int, fn func(lo, hi int) ([]ast.Price, []ast.Diagnostic, error)) ([]ast.Price, []ast.Diagnostic, error) {
	chunks := s.chunkRanges(length)
	if len(chunks) == 0 {
		return nil, nil, nil
	}
	type result struct {
		prices []ast.Price
		diags  []ast.Diagnostic
	}
	results := make([]result, len(chunks))
	var wg sync.WaitGroup
	for i, c := range chunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps, ds, err := fn(c[0], c[1])
			results[i] = result{prices: ps, diags: ds}
			if err != nil {
				results[i].diags = append(results[i].diags, ast.Diagnostic{
					Code:    "quote-fetch-error",
					Message: err.Error(),
				})
			}
		}()
	}
	wg.Wait()
	var prices []ast.Price
	var diags []ast.Diagnostic
	for _, r := range results {
		prices = append(prices, r.prices...)
		diags = append(diags, r.diags...)
	}
	if err := ctx.Err(); err != nil {
		return prices, diags, err
	}
	return prices, diags, nil
}

func (s *splitBatchSource) doLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.runChunks(ctx, len(q), func(lo, hi int) ([]ast.Price, []ast.Diagnostic, error) {
		return s.latest.QuoteLatest(ctx, q[lo:hi])
	})
}

func (s *splitBatchSource) doAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.runChunks(ctx, len(q), func(lo, hi int) ([]ast.Price, []ast.Diagnostic, error) {
		return s.at.QuoteAt(ctx, q[lo:hi], at)
	})
}

func (s *splitBatchSource) doRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.runChunks(ctx, len(q), func(lo, hi int) ([]ast.Price, []ast.Diagnostic, error) {
		return s.rng.QuoteRange(ctx, q[lo:hi], start, end)
	})
}

// asSource returns s typed against whichever sub-interface
// combination the wrapped source satisfies.
func (s *splitBatchSource) asSource() api.Source {
	hasL := s.latest != nil
	hasA := s.at != nil
	hasR := s.rng != nil
	switch {
	case hasL && hasA && hasR:
		return splitBatchLatestAtRange{s}
	case hasL && hasA:
		return splitBatchLatestAt{s}
	case hasL && hasR:
		return splitBatchLatestRange{s}
	case hasA && hasR:
		return splitBatchAtRange{s}
	case hasL:
		return splitBatchLatestOnly{s}
	case hasA:
		return splitBatchAtOnly{s}
	case hasR:
		return splitBatchRangeOnly{s}
	default:
		return splitBatchBaseOnly{s}
	}
}

// The eight permutations below exist so the SplitBatch return value
// satisfies exactly the sub-interface set of the wrapped source.

type splitBatchBaseOnly struct{ *splitBatchSource }

type splitBatchLatestOnly struct{ *splitBatchSource }

func (s splitBatchLatestOnly) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doLatest(ctx, q)
}

type splitBatchAtOnly struct{ *splitBatchSource }

func (s splitBatchAtOnly) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doAt(ctx, q, at)
}

type splitBatchRangeOnly struct{ *splitBatchSource }

func (s splitBatchRangeOnly) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doRange(ctx, q, start, end)
}

type splitBatchLatestAt struct{ *splitBatchSource }

func (s splitBatchLatestAt) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doLatest(ctx, q)
}
func (s splitBatchLatestAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doAt(ctx, q, at)
}

type splitBatchLatestRange struct{ *splitBatchSource }

func (s splitBatchLatestRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doLatest(ctx, q)
}
func (s splitBatchLatestRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doRange(ctx, q, start, end)
}

type splitBatchAtRange struct{ *splitBatchSource }

func (s splitBatchAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doAt(ctx, q, at)
}
func (s splitBatchAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doRange(ctx, q, start, end)
}

type splitBatchLatestAtRange struct{ *splitBatchSource }

func (s splitBatchLatestAtRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doLatest(ctx, q)
}
func (s splitBatchLatestAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doAt(ctx, q, at)
}
func (s splitBatchLatestAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.splitBatchSource.doRange(ctx, q, start, end)
}
