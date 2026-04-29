package sourceutil

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

type httpErr struct {
	code int
	msg  string
}

func (h *httpErr) Error() string       { return fmt.Sprintf("http %d: %s", h.code, h.msg) }
func (h *httpErr) HTTPStatusCode() int { return h.code }

func TestIsHTTPRetriable(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{200, false},
		{400, false},
		{408, true},
		{425, true},
		{429, true},
		{500, true},
		{502, true},
		{599, true},
		{600, false},
	}
	for _, c := range cases {
		got := IsHTTPRetriable(&httpErr{code: c.code})
		if got != c.want {
			t.Errorf("IsHTTPRetriable(%d) = %v, want %v", c.code, got, c.want)
		}
	}
	if IsHTTPRetriable(errors.New("plain")) {
		t.Errorf("IsHTTPRetriable(plain error) = true, want false")
	}
}

func TestRetryOnErrorRetriesUntilSuccess(t *testing.T) {
	var attempts int64
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			n := atomic.AddInt64(&attempts, 1)
			if n < 3 {
				return nil, nil, &httpErr{code: 503, msg: "down"}
			}
			return nil, nil, nil
		},
	}
	pol := RetryPolicy{
		MaxAttempts: 4,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		Jitter:      0,
	}
	src := RetryOnError(at, pol).(api.AtSource)
	_, _, err := src.QuoteAt(context.Background(), nil, time.Time{})
	if err != nil {
		t.Errorf("err=%v, want nil", err)
	}
	if attempts != 3 {
		t.Errorf("attempts=%d, want 3", attempts)
	}
}

func TestRetryOnErrorExhausts(t *testing.T) {
	var attempts int64
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&attempts, 1)
			return nil, nil, &httpErr{code: 500, msg: "always"}
		},
	}
	pol := RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	}
	src := RetryOnError(at, pol).(api.AtSource)
	_, _, err := src.QuoteAt(context.Background(), nil, time.Time{})
	if err == nil {
		t.Errorf("err=nil, want error")
	}
	if attempts != 3 {
		t.Errorf("attempts=%d, want 3", attempts)
	}
}

func TestRetryOnErrorNonRetriableFailsFast(t *testing.T) {
	var attempts int64
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			atomic.AddInt64(&attempts, 1)
			return nil, nil, errors.New("not retriable")
		},
	}
	pol := RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
	}
	src := RetryOnError(at, pol).(api.AtSource)
	_, _, err := src.QuoteAt(context.Background(), nil, time.Time{})
	if err == nil {
		t.Errorf("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts=%d, want 1 (non-retriable)", attempts)
	}
}

func TestRetryOnErrorCustomIsRetriable(t *testing.T) {
	var attempts int64
	custom := errors.New("custom-transient")
	at := &fakeAt{
		name: "x",
		caps: api.Capabilities{SupportsAt: true},
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			n := atomic.AddInt64(&attempts, 1)
			if n < 2 {
				return nil, nil, custom
			}
			return nil, nil, nil
		},
	}
	pol := RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		IsRetriable: func(err error) bool { return errors.Is(err, custom) },
	}
	src := RetryOnError(at, pol).(api.AtSource)
	_, _, err := src.QuoteAt(context.Background(), nil, time.Time{})
	if err != nil {
		t.Errorf("err=%v, want nil", err)
	}
	if attempts != 2 {
		t.Errorf("attempts=%d, want 2", attempts)
	}
}

func TestRetryOnErrorPreservesSubInterfaces(t *testing.T) {
	src := &fakeLatestAt{
		name: "x",
		caps: api.Capabilities{SupportsLatest: true, SupportsAt: true},
		handleAt: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			return nil, nil, nil
		},
		handleLatest: func(context.Context, []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) { return nil, nil, nil },
	}
	wrapped := RetryOnError(src, RetryPolicy{})
	if _, ok := wrapped.(api.LatestSource); !ok {
		t.Errorf("RetryOnError lost LatestSource sub-interface")
	}
	if _, ok := wrapped.(api.AtSource); !ok {
		t.Errorf("RetryOnError lost AtSource sub-interface")
	}
	if _, ok := wrapped.(api.RangeSource); ok {
		t.Errorf("RetryOnError unexpectedly added RangeSource sub-interface")
	}
}
