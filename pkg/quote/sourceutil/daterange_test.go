package sourceutil

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

func TestWeekdaysOnlyExcludesWeekend(t *testing.T) {
	// 2024-01-06 is Saturday, 2024-01-07 is Sunday.
	if WeekdaysOnly.Includes(utcDate(2024, time.January, 6)) {
		t.Errorf("WeekdaysOnly.Includes(Saturday) = true, want false")
	}
	if WeekdaysOnly.Includes(utcDate(2024, time.January, 7)) {
		t.Errorf("WeekdaysOnly.Includes(Sunday) = true, want false")
	}
	if !WeekdaysOnly.Includes(utcDate(2024, time.January, 8)) {
		t.Errorf("WeekdaysOnly.Includes(Monday) = false, want true")
	}
}

func TestAllDaysIncludesEverything(t *testing.T) {
	for d := 1; d <= 7; d++ {
		if !AllDays.Includes(utcDate(2024, time.January, d)) {
			t.Errorf("AllDays.Includes(2024-01-%02d) = false, want true", d)
		}
	}
}

func TestDateRangeIterCallsAtPerWeekday(t *testing.T) {
	var seenDates []time.Time
	at := &fakeAt{
		name: "fx",
		handle: func(_ context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			seenDates = append(seenDates, at)
			out := make([]ast.Price, 0, len(q))
			for _, qq := range q {
				var d apd.Decimal
				_, _, _ = d.SetString("1")
				out = append(out, ast.Price{
					Date:      at,
					Commodity: qq.Pair.Commodity,
					Amount:    ast.Amount{Number: d, Currency: qq.Pair.QuoteCurrency},
				})
			}
			return out, nil, nil
		},
	}
	rs := DateRangeIter(at, WeekdaysOnly)

	if _, ok := any(rs).(api.RangeSource); !ok {
		t.Errorf("DateRangeIter return value does not satisfy api.RangeSource")
	}
	if _, ok := any(rs).(api.AtSource); !ok {
		t.Errorf("DateRangeIter return value does not satisfy api.AtSource (inherited)")
	}

	// 2024-01-05 (Fri) through 2024-01-10 (Wed): Fri, Mon, Tue (5/6/7 -> Fri Sat Sun, then 8/9/10 = Mon Tue Wed; end exclusive at Wed 10).
	start := utcDate(2024, time.January, 5)
	end := utcDate(2024, time.January, 10) // exclusive
	queries := []api.SourceQuery{{Pair: api.Pair{Commodity: "EUR", QuoteCurrency: "USD"}, Symbol: "EUR"}}
	prices, _, err := rs.QuoteRange(context.Background(), queries, start, end)
	if err != nil {
		t.Fatalf("QuoteRange returned error: %v", err)
	}

	wantDates := []time.Time{
		utcDate(2024, time.January, 5), // Fri
		utcDate(2024, time.January, 8), // Mon
		utcDate(2024, time.January, 9), // Tue
	}
	if len(seenDates) != len(wantDates) {
		t.Fatalf("seenDates=%v, want=%v", seenDates, wantDates)
	}
	for i, d := range seenDates {
		if !d.Equal(wantDates[i]) {
			t.Errorf("seenDates[%d]=%v, want %v", i, d, wantDates[i])
		}
	}
	if len(prices) != len(wantDates) {
		t.Errorf("got %d prices, want %d", len(prices), len(wantDates))
	}
	// Verify ordering of returned prices.
	for i, p := range prices {
		if !p.Date.Equal(wantDates[i]) {
			t.Errorf("prices[%d].Date = %v, want %v", i, p.Date, wantDates[i])
		}
	}
}

func TestDateRangeIterHalfOpen(t *testing.T) {
	var seen []time.Time
	at := &fakeAt{
		name: "all",
		handle: func(_ context.Context, _ []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			seen = append(seen, at)
			return nil, nil, nil
		},
	}
	rs := DateRangeIter(at, AllDays)
	start := utcDate(2024, time.January, 1)
	end := utcDate(2024, time.January, 4)
	_, _, err := rs.QuoteRange(context.Background(), nil, start, end)
	if err != nil {
		t.Fatalf("QuoteRange err: %v", err)
	}
	if len(seen) != 3 {
		t.Errorf("got %d calls, want 3 (1, 2, 3 only)", len(seen))
	}
	for i, want := range []time.Time{utcDate(2024, time.January, 1), utcDate(2024, time.January, 2), utcDate(2024, time.January, 3)} {
		if !seen[i].Equal(want) {
			t.Errorf("seen[%d]=%v, want %v", i, seen[i], want)
		}
	}
}

func TestDateRangeIterDelegatesAt(t *testing.T) {
	calledAt := false
	at := &fakeAt{
		name: "x",
		handle: func(context.Context, []api.SourceQuery, time.Time) ([]ast.Price, []ast.Diagnostic, error) {
			calledAt = true
			return nil, nil, nil
		},
	}
	rs := DateRangeIter(at, AllDays)
	asAt, ok := rs.(api.AtSource)
	if !ok {
		t.Fatalf("DateRangeIter return value is not an AtSource")
	}
	_, _, err := asAt.QuoteAt(context.Background(), nil, utcDate(2024, time.January, 1))
	if err != nil {
		t.Fatalf("QuoteAt err: %v", err)
	}
	if !calledAt {
		t.Errorf("wrapped AtSource.QuoteAt was not called")
	}
}
