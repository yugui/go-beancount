package sourceutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

func TestRateLimitEnforcesRate(t *testing.T) {
	at := &fakeAt{
		name: "x",
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	// 10 rps, burst 1: first call free, then 100ms between calls.
	src := RateLimit(at, 10, 1).(api.AtSource)

	start := time.Now()
	for i := 0; i < 3; i++ {
		_, _, err := src.QuoteAt(context.Background(), nil, time.Time{})
		if err != nil {
			t.Fatalf("call %d err: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	// 2 inter-call delays of 100ms each = 200ms minimum.
	if elapsed < 180*time.Millisecond {
		t.Errorf("elapsed=%v, want >= ~200ms", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed=%v, suspiciously long", elapsed)
	}
}

func TestRateLimitHonoursCancellation(t *testing.T) {
	at := &fakeAt{
		name: "x",
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	// 0.5 rps, burst 1: first call free, then 2s wait.
	src := RateLimit(at, 0.5, 1).(api.AtSource)

	// Consume the first token.
	_, _, err := src.QuoteAt(context.Background(), nil, time.Time{})
	if err != nil {
		t.Fatalf("first call err: %v", err)
	}

	// Cancel the second mid-wait.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err = src.QuoteAt(ctx, nil, time.Time{})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("err=%v, want context cancellation", err)
	}
}

func TestRateLimitPreservesSubInterfaces(t *testing.T) {
	src := &fakeLatestAt{
		name: "x",
		handleAt: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
		handleLatest: func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) { return nil, nil, nil },
	}
	wrapped := RateLimit(src, 100, 10)
	if _, ok := wrapped.(api.LatestSource); !ok {
		t.Errorf("RateLimit lost LatestSource sub-interface")
	}
	if _, ok := wrapped.(api.AtSource); !ok {
		t.Errorf("RateLimit lost AtSource sub-interface")
	}
}

func TestRateLimitFractionalRPS(t *testing.T) {
	// 2 rps -> 500ms between calls after burst.
	at := &fakeAt{
		name: "x",
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
	}
	src := RateLimit(at, 2, 1).(api.AtSource)
	start := time.Now()
	_, _, _ = src.QuoteAt(context.Background(), nil, time.Time{})
	_, _, _ = src.QuoteAt(context.Background(), nil, time.Time{})
	elapsed := time.Since(start)
	if elapsed < 400*time.Millisecond {
		t.Errorf("elapsed=%v, want >= ~500ms for 2 calls at 2rps with burst 1", elapsed)
	}
}
