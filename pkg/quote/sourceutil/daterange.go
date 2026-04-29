package sourceutil

import (
	"context"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// Calendar reports which dates a source emits prices for. The default
// implementations skip Saturday and Sunday (WeekdaysOnly) or include
// every date (AllDays). Implementations are stateless and safe for
// concurrent use.
type Calendar interface {
	// Includes reports whether the given calendar date should be
	// emitted. Implementations should ignore the time-of-day portion
	// of date and consider only the calendar day in date.UTC().
	Includes(date time.Time) bool
}

type allDays struct{}

func (allDays) Includes(time.Time) bool { return true }

type weekdaysOnly struct{}

func (weekdaysOnly) Includes(date time.Time) bool {
	w := date.UTC().Weekday()
	return w != time.Saturday && w != time.Sunday
}

// AllDays is a Calendar that includes every date in [start, end). Use
// this for sources whose underlying market trades 24/7 (cryptocurrency
// venues, for example).
var AllDays Calendar = allDays{}

// WeekdaysOnly is a Calendar that excludes Saturday and Sunday. It is
// the appropriate default for FX reference data and listed-equity
// sources whose underlying market is closed on weekends.
var WeekdaysOnly Calendar = weekdaysOnly{}

// DateRangeIter lifts an api.AtSource into an api.RangeSource by
// iterating dates within the half-open interval [start, end) and
// invoking the wrapped source's QuoteAt for each date the Calendar
// includes. Output prices are returned in date-ascending order;
// per-date diagnostics are concatenated in the same order.
//
// The returned source reports Capabilities with SupportsRange=true
// and inherits the wrapped source's other Capabilities flags
// (SupportsLatest, SupportsAt, BatchPairs, RangePerCall). Stack with
// Concurrency to bound the number of parallel per-date calls; by
// itself this decorator iterates dates serially, which is the safe
// default for sources with strict rate limits.
func DateRangeIter(s api.AtSource, cal Calendar) api.RangeSource {
	if cal == nil {
		cal = AllDays
	}
	return &dateRangeIter{at: s, cal: cal}
}

type dateRangeIter struct {
	at  api.AtSource
	cal Calendar
}

func (d *dateRangeIter) Name() string { return d.at.Name() }

func (d *dateRangeIter) Capabilities() api.Capabilities {
	c := d.at.Capabilities()
	c.SupportsRange = true
	return c
}

func (d *dateRangeIter) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return d.at.QuoteAt(ctx, q, at)
}

func (d *dateRangeIter) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	var prices []ast.Price
	var diags []ast.Diagnostic
	// Normalise to calendar-day stepping at 0:00 UTC of start.
	cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	for cur.Before(endDay) {
		if err := ctx.Err(); err != nil {
			return prices, diags, err
		}
		if d.cal.Includes(cur) {
			ps, ds, err := d.at.QuoteAt(ctx, q, cur)
			prices = append(prices, ps...)
			diags = append(diags, ds...)
			if err != nil {
				return prices, diags, err
			}
		}
		cur = cur.AddDate(0, 0, 1)
	}
	return prices, diags, nil
}
