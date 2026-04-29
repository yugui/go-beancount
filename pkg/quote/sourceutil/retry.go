package sourceutil

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// RetryPolicy configures RetryOnError. A zero value retries up to
// three times with a 1-second base delay capped at 30 seconds and 20%
// jitter, using the default IsRetriable predicate.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (including the
	// first). Values <= 0 are treated as 3.
	MaxAttempts int
	// BaseDelay is the delay before the second attempt; subsequent
	// attempts double up to MaxDelay. Values <= 0 are treated as 1s.
	BaseDelay time.Duration
	// MaxDelay caps the per-attempt backoff. Values <= 0 are treated
	// as 30s.
	MaxDelay time.Duration
	// Jitter is the fraction (0..1) of the computed delay added or
	// subtracted uniformly at random. A value of 0.2 means the actual
	// delay is uniformly distributed over [0.8d, 1.2d]. Values
	// outside [0, 1] are clamped.
	Jitter float64
	// IsRetriable decides whether an error returned by the wrapped
	// source should trigger another attempt. Nil means use the
	// package default, which retries on transient network errors and
	// HTTP-style transient errors recognised by IsHTTPRetriable.
	IsRetriable func(error) bool
}

func (p RetryPolicy) normalised() RetryPolicy {
	out := p
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = 3
	}
	if out.BaseDelay <= 0 {
		out.BaseDelay = time.Second
	}
	if out.MaxDelay <= 0 {
		out.MaxDelay = 30 * time.Second
	}
	if out.Jitter < 0 {
		out.Jitter = 0
	}
	if out.Jitter > 1 {
		out.Jitter = 1
	}
	if out.IsRetriable == nil {
		out.IsRetriable = defaultIsRetriable
	}
	return out
}

// HTTPError is the marker interface RetryOnError consults to recognise
// HTTP-style transient failures. Quoter authors that wrap their HTTP
// transport's errors should ensure those errors satisfy this interface
// (typically by embedding a small struct with a StatusCode method).
type HTTPError interface {
	error
	HTTPStatusCode() int
}

// IsHTTPRetriable reports whether err is an HTTP-style transient error
// that warrants a retry: 408 (request timeout), 425 (too early), 429
// (too many requests), or any 5xx. It uses errors.As so wrapped errors
// in the chain are inspected.
func IsHTTPRetriable(err error) bool {
	var he HTTPError
	if !errors.As(err, &he) {
		return false
	}
	c := he.HTTPStatusCode()
	return c == 408 || c == 425 || c == 429 || (c >= 500 && c <= 599)
}

// defaultIsRetriable is the fall-back predicate when RetryPolicy.
// IsRetriable is nil: retry transient network errors (per net.Error.
// Temporary or Timeout) and HTTP-style transient status codes. Source
// authors with source-specific transient errors typically override this.
func defaultIsRetriable(err error) bool {
	if err == nil {
		return false
	}
	if IsHTTPRetriable(err) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return true
		}
	}
	return false
}

// RetryOnError wraps s so that calls returning a retriable error are
// retried with exponential backoff per policy. The wrapper preserves
// whichever Capability sub-interfaces s implements.
//
// # Retry semantics
//
// Only the top-level error return value is consulted; per-query
// diagnostics flow through unchanged. If a retried call eventually
// succeeds, the prices/diagnostics from the successful attempt are
// returned and earlier attempts' partial results are discarded. If
// every attempt fails, the final error is returned.
//
// # Goroutine safety
//
// The returned source is safe for concurrent use provided s is also
// safe for concurrent use. Each call's retry loop is independent.
//
// # Stacking
//
// Place RetryOnError between RateLimit and the source so that retries
// are subject to the rate limiter; in particular, the 429-driven
// retry path doubles as a back-stop when RateLimit's calibration is
// loose.
func RetryOnError(s api.Source, policy RetryPolicy) api.Source {
	pol := policy.normalised()
	hook := &retryHook{policy: pol, rand: rand.New(rand.NewSource(time.Now().UnixNano()))}
	w := &retrySource{base: s, hook: hook}
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

type retryHook struct {
	policy RetryPolicy
	mu     sync.Mutex
	rand   *rand.Rand
}

func (h *retryHook) backoff(attempt int) time.Duration {
	d := float64(h.policy.BaseDelay) * math.Pow(2, float64(attempt-1))
	if d > float64(h.policy.MaxDelay) {
		d = float64(h.policy.MaxDelay)
	}
	if h.policy.Jitter > 0 {
		h.mu.Lock()
		// Uniform in [-Jitter, +Jitter].
		j := (h.rand.Float64()*2 - 1) * h.policy.Jitter
		h.mu.Unlock()
		d *= 1 + j
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// retrySource is parallel to wrappedSource but has its own call helper
// because each call must restart the loop on a retriable error rather
// than acquire a single semaphore slot.
type retrySource struct {
	base   api.Source
	hook   *retryHook
	latest api.LatestSource
	at     api.AtSource
	rng    api.RangeSource
}

func (w *retrySource) Name() string                   { return w.base.Name() }
func (w *retrySource) Capabilities() api.Capabilities { return w.base.Capabilities() }

func (w *retrySource) call(ctx context.Context, fn func(context.Context) ([]ast.Price, []ast.Diagnostic, error)) ([]ast.Price, []ast.Diagnostic, error) {
	var lastPrices []ast.Price
	var lastDiags []ast.Diagnostic
	var lastErr error
	for attempt := 1; attempt <= w.hook.policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		p, d, err := fn(ctx)
		if err == nil {
			return p, d, nil
		}
		lastPrices, lastDiags, lastErr = p, d, err
		if !w.hook.policy.IsRetriable(err) {
			return p, d, err
		}
		if attempt == w.hook.policy.MaxAttempts {
			break
		}
		wait := w.hook.backoff(attempt)
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return lastPrices, lastDiags, ctx.Err()
		}
	}
	return lastPrices, lastDiags, lastErr
}

func (w *retrySource) doLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return w.call(ctx, func(ctx context.Context) ([]ast.Price, []ast.Diagnostic, error) {
		return w.latest.QuoteLatest(ctx, q)
	})
}

func (w *retrySource) doAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return w.call(ctx, func(ctx context.Context) ([]ast.Price, []ast.Diagnostic, error) {
		return w.at.QuoteAt(ctx, q, at)
	})
}

func (w *retrySource) doRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return w.call(ctx, func(ctx context.Context) ([]ast.Price, []ast.Diagnostic, error) {
		return w.rng.QuoteRange(ctx, q, start, end)
	})
}

func (w *retrySource) asSource() api.Source {
	hasL := w.latest != nil
	hasA := w.at != nil
	hasR := w.rng != nil
	switch {
	case hasL && hasA && hasR:
		return retryLatestAtRange{w}
	case hasL && hasA:
		return retryLatestAt{w}
	case hasL && hasR:
		return retryLatestRange{w}
	case hasA && hasR:
		return retryAtRange{w}
	case hasL:
		return retryLatestOnly{w}
	case hasA:
		return retryAtOnly{w}
	case hasR:
		return retryRangeOnly{w}
	default:
		return retryBaseOnly{w}
	}
}

type retryBaseOnly struct{ *retrySource }

type retryLatestOnly struct{ *retrySource }

func (r retryLatestOnly) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doLatest(ctx, q)
}

type retryAtOnly struct{ *retrySource }

func (r retryAtOnly) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doAt(ctx, q, at)
}

type retryRangeOnly struct{ *retrySource }

func (r retryRangeOnly) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doRange(ctx, q, start, end)
}

type retryLatestAt struct{ *retrySource }

func (r retryLatestAt) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doLatest(ctx, q)
}
func (r retryLatestAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doAt(ctx, q, at)
}

type retryLatestRange struct{ *retrySource }

func (r retryLatestRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doLatest(ctx, q)
}
func (r retryLatestRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doRange(ctx, q, start, end)
}

type retryAtRange struct{ *retrySource }

func (r retryAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doAt(ctx, q, at)
}
func (r retryAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doRange(ctx, q, start, end)
}

type retryLatestAtRange struct{ *retrySource }

func (r retryLatestAtRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doLatest(ctx, q)
}
func (r retryLatestAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doAt(ctx, q, at)
}
func (r retryLatestAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return r.retrySource.doRange(ctx, q, start, end)
}
