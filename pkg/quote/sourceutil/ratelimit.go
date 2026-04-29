package sourceutil

import (
	"context"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/quote/api"
)

// RateLimit imposes a token-bucket rate limit on every call to s. rps
// is the long-run rate in tokens per second and may be fractional (e.g.
// 0.5 means one call every two seconds); burst is the bucket capacity
// and the maximum number of tokens that may accumulate during idle
// periods. Each call to QuoteLatest / QuoteAt / QuoteRange consumes one
// token; if no token is available the call blocks until one is, or
// until ctx is cancelled.
//
// A non-positive rps disables waiting (effectively unlimited), and a
// non-positive burst is normalised to 1 so the bucket can hold at
// least one token.
//
// The wrapper preserves whichever Capability sub-interfaces s
// implements; recover the desired sub-interface with a type assertion.
//
// # Goroutine safety
//
// The returned source is safe for concurrent use. Tokens are dispensed
// fairly under a single mutex; multiple waiters proceed in arrival
// order.
//
// # Wall-clock semantics
//
// Calls observe wall-clock time via time.Now / time.Sleep. The bucket
// refills continuously rather than in discrete ticks, so the average
// rate over any window strictly larger than 1/rps converges to rps
// regardless of burstiness in arrival times.
//
// # Stacking
//
// RateLimit is the canonical way to honour a documented per-source
// quota; place it outside RetryOnError so retries are themselves rate-
// limited. When 429 errors are still possible despite the limiter
// (calibration drift, server-side throttling), RetryOnError acts as a
// back-stop.
func RateLimit(s api.Source, rps float64, burst int) api.Source {
	if burst <= 0 {
		burst = 1
	}
	tb := &tokenBucket{
		rps:    rps,
		burst:  burst,
		tokens: float64(burst),
		last:   time.Now(),
	}
	return wrapSource(s, &rateLimitHook{bucket: tb})
}

type tokenBucket struct {
	mu     sync.Mutex
	rps    float64
	burst  int
	tokens float64
	last   time.Time
}

// take blocks until one token is available, returning ctx.Err() if ctx
// is cancelled before that. When rps is non-positive the bucket is
// treated as unlimited and take returns immediately.
func (b *tokenBucket) take(ctx context.Context) error {
	if b.rps <= 0 {
		return ctx.Err()
	}
	for {
		b.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(b.last).Seconds()
		b.tokens += elapsed * b.rps
		if b.tokens > float64(b.burst) {
			b.tokens = float64(b.burst)
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		// Compute wait until we have a full token.
		need := 1 - b.tokens
		wait := time.Duration(need / b.rps * float64(time.Second))
		b.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-t.C:
			// loop again
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

type rateLimitHook struct {
	bucket *tokenBucket
}

func (h *rateLimitHook) before(ctx context.Context) (release func(), err error) {
	if err := h.bucket.take(ctx); err != nil {
		return nil, err
	}
	return func() {}, nil
}
