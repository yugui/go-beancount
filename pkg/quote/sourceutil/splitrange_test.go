package sourceutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

func TestSplitRange_BucketSize(t *testing.T) {
	type call struct{ start, end time.Time }
	var calls []call
	src := &fakeRange{
		name: "x",
		handle: func(_ context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			calls = append(calls, call{start, end})
			return nil, nil, nil
		},
	}
	wrapped := SplitRange(src, 2)
	d0 := utcDate(2024, time.January, 1)
	d5 := d0.AddDate(0, 0, 5)
	if _, _, err := wrapped.QuoteRange(context.Background(), nil, d0, d5); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(calls))
	}
	want := []call{
		{d0, d0.AddDate(0, 0, 2)},
		{d0.AddDate(0, 0, 2), d0.AddDate(0, 0, 4)},
		{d0.AddDate(0, 0, 4), d5},
	}
	for i, w := range want {
		if !calls[i].start.Equal(w.start) || !calls[i].end.Equal(w.end) {
			t.Errorf("call %d = [%v, %v), want [%v, %v)", i, calls[i].start, calls[i].end, w.start, w.end)
		}
	}
}

func TestSplitRange_PerCallZeroPassesThrough(t *testing.T) {
	type call struct{ start, end time.Time }
	var calls []call
	src := &fakeRange{
		name: "x",
		handle: func(_ context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			calls = append(calls, call{start, end})
			return nil, nil, nil
		},
	}
	wrapped := SplitRange(src, 0)
	d0 := utcDate(2024, time.January, 1)
	d10 := d0.AddDate(0, 0, 10)
	if _, _, err := wrapped.QuoteRange(context.Background(), nil, d0, d10); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if !calls[0].start.Equal(d0) || !calls[0].end.Equal(d10) {
		t.Errorf("call = [%v, %v), want [%v, %v)", calls[0].start, calls[0].end, d0, d10)
	}
}

func TestSplitRange_OneBucketError_DoesNotFailAll(t *testing.T) {
	type call struct{ start, end time.Time }
	var calls []call
	src := &fakeRange{
		name: "x",
		handle: func(_ context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			idx := len(calls)
			calls = append(calls, call{start, end})
			if idx == 1 {
				return nil, nil, errors.New("middle bucket failed")
			}
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				var d apd.Decimal
				_, _, _ = d.SetString("1")
				out = append(out, ast.Price{
					Date:      start,
					Commodity: qq.Pair.Commodity,
					Amount:    ast.Amount{Number: d, Currency: qq.Pair.QuoteCurrency},
				})
			}
			return out, nil, nil
		},
	}
	wrapped := SplitRange(src, 2)
	d0 := utcDate(2024, time.January, 1)
	d6 := d0.AddDate(0, 0, 6)
	queries := []api.SourceQuery{{Pair: api.Pair{Commodity: "A", QuoteCurrency: "USD"}}}
	prices, diags, err := wrapped.QuoteRange(context.Background(), queries, d0, d6)
	if err != nil {
		t.Fatalf("err = %v, want nil (per-bucket errors should turn into diags)", err)
	}
	if len(calls) != 3 {
		t.Errorf("got %d calls, want 3", len(calls))
	}
	// First and third buckets each contributed a Price.
	if len(prices) != 2 {
		t.Errorf("got %d prices, want 2", len(prices))
	}
	// At least one quote-fetch-error diag for the failing middle bucket.
	found := false
	for _, d := range diags {
		if d.Code == "quote-fetch-error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing quote-fetch-error diag, got: %+v", diags)
	}
}
