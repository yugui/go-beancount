package sourceutil

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

func TestConcurrencyCapsInFlight(t *testing.T) {
	const limit = 2
	const callers = 6
	var inFlight int64
	var maxInFlight int64
	release := make(chan struct{})
	entered := make(chan struct{}, callers)

	at := &fakeAt{
		name: "x",
		handle: func(_ context.Context, _ []api.SourceQuery, _ time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			cur := atomic.AddInt64(&inFlight, 1)
			for {
				m := atomic.LoadInt64(&maxInFlight)
				if cur <= m || atomic.CompareAndSwapInt64(&maxInFlight, m, cur) {
					break
				}
			}
			entered <- struct{}{}
			<-release
			atomic.AddInt64(&inFlight, -1)
			return nil, nil, nil
		},
	}
	src := Concurrency(at, limit)
	asAt, ok := src.(api.AtSource)
	if !ok {
		t.Fatalf("Concurrency(AtSource) result is not an AtSource")
	}

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = asAt.QuoteAt(context.Background(), nil, utcDate(2024, time.January, 5))
		}()
	}

	// Wait until exactly `limit` goroutines have entered the wrapped
	// source. The semaphore must block the rest, so no further entered
	// signals should arrive until release is closed.
	deadline := time.After(2 * time.Second)
	for i := 0; i < limit; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatalf("only %d/%d goroutines entered before deadline", i, limit)
		}
	}
	// Confirm no extra goroutines have entered: any additional entered
	// signal within a short window indicates the semaphore is leaky.
	select {
	case <-entered:
		t.Errorf("more than %d goroutines entered concurrently", limit)
	case <-time.After(50 * time.Millisecond):
	}
	if got := atomic.LoadInt64(&inFlight); got > limit {
		t.Errorf("inFlight=%d during steady state, want <= %d", got, limit)
	}

	// Release everyone.
	close(release)
	wg.Wait()

	if maxInFlight > limit {
		t.Errorf("maxInFlight=%d, want <= %d", maxInFlight, limit)
	}
	if maxInFlight < 1 {
		t.Errorf("maxInFlight=%d, want >= 1", maxInFlight)
	}
}

func TestConcurrencyPreservesSubInterfaces(t *testing.T) {
	src := &fakeLatestAt{
		name: "x",
		handleAt: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
		handleLatest: func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	wrapped := Concurrency(src, 2)
	if _, ok := wrapped.(api.LatestSource); !ok {
		t.Errorf("Concurrency lost LatestSource sub-interface")
	}
	if _, ok := wrapped.(api.AtSource); !ok {
		t.Errorf("Concurrency lost AtSource sub-interface")
	}
	if _, ok := wrapped.(api.RangeSource); ok {
		t.Errorf("Concurrency unexpectedly added RangeSource sub-interface")
	}
}

func TestConcurrencyHonoursContextCancellation(t *testing.T) {
	at := &fakeAt{
		name: "x",
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	src := Concurrency(at, 1).(api.AtSource)

	// Saturate the semaphore. The saturating goroutine signals that it
	// is inside the wrapped source (and therefore holds the only slot)
	// by closing `inside`; the test waits on that channel rather than
	// sleeping.
	hold := make(chan struct{})
	inside := make(chan struct{})
	at.handle = func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
		close(inside)
		<-hold
		return nil, nil, nil
	}
	go func() { _, _, _ = src.QuoteAt(context.Background(), nil, time.Time{}) }()
	select {
	case <-inside:
	case <-time.After(2 * time.Second):
		t.Fatalf("saturating goroutine did not enter wrapped source before deadline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := src.QuoteAt(ctx, nil, time.Time{})
	if err == nil {
		t.Errorf("expected ctx.Err(), got nil")
	}
	close(hold)
}
