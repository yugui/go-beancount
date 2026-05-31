package std

import (
	"fmt"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/query/api"
	"github.com/yugui/go-beancount/pkg/query/env"
	"github.com/yugui/go-beancount/pkg/query/price"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// isoLayouts are tried in order by the single-argument parse_date. The Go
// port does not replicate dateutil's full flexible parsing; it accepts the
// common unambiguous layouts and errors otherwise.
var isoLayouts = []string{"2006-01-02", "2006/01/02", "2006-01-02T15:04:05Z07:00"}

func init() {
	env.Register(api.Function{
		Name:   "today",
		In:     nil,
		Out:    types.Date,
		Flavor: api.ScalarFlavor,
		Scalar: func(ctx *price.QueryContext, _ []types.Value) (types.Value, error) {
			return types.NewDate(dateOnly(ctx.Now)), nil
		},
	})

	registerStrict("date_add", []types.Type{types.Date, types.Int}, types.Date, func(args []types.Value) (types.Value, error) {
		d, _ := types.AsDate(args[0])
		n, _ := types.AsInt(args[1])
		return types.NewDate(d.AddDate(0, 0, int(n))), nil
	})

	registerStrict("date_diff", []types.Type{types.Date, types.Date}, types.Int, func(args []types.Value) (types.Value, error) {
		x, _ := types.AsDate(args[0])
		y, _ := types.AsDate(args[1])
		return types.NewInt(int64(x.Sub(y) / (24 * time.Hour))), nil
	})

	registerStrict("parse_date", []types.Type{types.String}, types.Date, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsString(args[0])
		for _, layout := range isoLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return types.NewDate(dateOnly(t)), nil
			}
		}
		return nil, fmt.Errorf("std: parse_date: cannot parse %q", s)
	})
	registerStrict("parse_date", []types.Type{types.String, types.String}, types.Date, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsString(args[0])
		strptimeFmt, _ := types.AsString(args[1])
		t, err := time.Parse(strptimeToLayout(strptimeFmt), s)
		if err != nil {
			return nil, fmt.Errorf("std: parse_date: cannot parse %q with format %q: %w", s, strptimeFmt, err)
		}
		return types.NewDate(dateOnly(t)), nil
	})

	registerStrict("date_trunc", []types.Type{types.String, types.Date}, types.Date, func(args []types.Value) (types.Value, error) {
		field, _ := types.AsString(args[0])
		x, _ := types.AsDate(args[1])
		return dateTrunc(field, x), nil
	})
	registerStrict("date_part", []types.Type{types.String, types.Date}, types.Int, func(args []types.Value) (types.Value, error) {
		field, _ := types.AsString(args[0])
		x, _ := types.AsDate(args[1])
		return datePart(field, x), nil
	})
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func ymd(y int, m time.Month, d int) types.Value {
	return types.NewDate(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

// dateTrunc truncates x to the start of the named period, matching upstream
// beanquery's date_trunc. An unrecognized field yields NULL, as upstream
// returns None.
func dateTrunc(field string, x time.Time) types.Value {
	y := x.Year()
	switch field {
	case "week":
		return types.NewDate(x.AddDate(0, 0, -weekdayMon0(x)))
	case "month":
		return ymd(y, x.Month(), 1)
	case "quarter":
		return ymd(y, time.Month(int(x.Month())-(int(x.Month())-1)%3), 1)
	case "year":
		return ymd(y, time.January, 1)
	case "decade":
		return ymd(y-y%10, time.January, 1)
	case "century":
		return ymd(y-(y-1)%100, time.January, 1)
	case "millennium":
		return ymd(y-(y-1)%1000, time.January, 1)
	default:
		return types.Null(types.Date)
	}
}

// datePart extracts the named integer field from x, matching upstream
// beanquery's date_part. An unrecognized field yields NULL, as upstream
// returns None.
func datePart(field string, x time.Time) types.Value {
	y := x.Year()
	switch field {
	case "weekday", "dow":
		return types.NewInt(int64(weekdayMon0(x)))
	case "isoweekday", "isodow":
		return types.NewInt(int64(weekdayMon0(x) + 1))
	case "week":
		_, w := x.ISOWeek()
		return types.NewInt(int64(w))
	case "month":
		return types.NewInt(int64(x.Month()))
	case "quarter":
		return types.NewInt(int64((int(x.Month())-1)/3 + 1))
	case "year":
		return types.NewInt(int64(y))
	case "isoyear":
		iy, _ := x.ISOWeek()
		return types.NewInt(int64(iy))
	case "decade":
		return types.NewInt(int64(y / 10))
	case "century":
		return types.NewInt(int64((y-1)/100 + 1))
	case "millennium":
		return types.NewInt(int64((y-1)/1000 + 1))
	case "epoch":
		return types.NewInt(dateOnly(x).Unix())
	default:
		return types.Null(types.Int)
	}
}

// weekdayMon0 returns x's weekday with Monday as 0, matching Python's
// date.weekday().
func weekdayMon0(x time.Time) int {
	return (int(x.Weekday()) + 6) % 7
}

// strptimeReplacer maps the common C strptime directives to Go reference-time
// layout fragments, so parse_date's format argument keeps upstream's strptime
// semantics rather than Go's layout syntax.
var strptimeReplacer = strings.NewReplacer(
	"%Y", "2006",
	"%y", "06",
	"%m", "01",
	"%d", "02",
	"%H", "15",
	"%M", "04",
	"%S", "05",
	"%B", "January",
	"%b", "Jan",
	"%A", "Monday",
	"%a", "Mon",
	"%p", "PM",
	"%%", "%",
)

func strptimeToLayout(f string) string {
	return strptimeReplacer.Replace(f)
}
