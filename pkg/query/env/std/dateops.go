package std

import (
	"fmt"
	"regexp"
	"strconv"
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

	registerStrict("interval", []types.Type{types.String}, types.Interval, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsString(args[0])
		if y, mo, d, ok := intervalFromString(s); ok {
			return types.NewInterval(y, mo, d), nil
		}
		return types.Null(types.Interval), nil
	})
	registerStrict("date_bin", []types.Type{types.Interval, types.Date, types.Date}, types.Date, func(args []types.Value) (types.Value, error) {
		y, mo, d, _ := types.AsInterval(args[0])
		source, _ := types.AsDate(args[1])
		origin, _ := types.AsDate(args[2])
		return dateBin(y, mo, d, source, origin), nil
	})
	registerStrict("date_bin", []types.Type{types.String, types.Date, types.Date}, types.Date, func(args []types.Value) (types.Value, error) {
		s, _ := types.AsString(args[0])
		source, _ := types.AsDate(args[1])
		origin, _ := types.AsDate(args[2])
		y, mo, d, ok := intervalFromString(s)
		if !ok {
			return types.Null(types.Date), nil
		}
		return dateBin(y, mo, d, source, origin), nil
	})
}

var intervalRE = regexp.MustCompile(`^([-+]?[0-9]+)\s+(day|month|year)s?$`)

// intervalFromString parses upstream beanquery's interval() grammar:
// "<int> <unit>" where unit is day|month|year (optional trailing s),
// matched against the whole string. ok is false when x does not match.
func intervalFromString(x string) (years, months, days int, ok bool) {
	m := intervalRE.FindStringSubmatch(x)
	if m == nil {
		return 0, 0, 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, 0, false
	}
	switch m[2] {
	case "day":
		return 0, 0, n, true
	case "month":
		return 0, n, 0, true
	default: // year
		return n, 0, 0, true
	}
}

// addInterval applies a dateutil relativedelta-style offset to t: it adds
// years and months with end-of-month CLAMPING (e.g. Jan 31 + 1 month =
// Feb 28), then adds days as a plain day delta. This matches Python
// dateutil's relativedelta addition, which date_bin's month/year stride
// relies on.
func addInterval(t time.Time, years, months, days int) time.Time {
	y := t.Year() + years
	mi := int(t.Month()) - 1 + months
	y += floorDiv(mi, 12)
	m := floorMod(mi, 12) + 1
	day := t.Day()
	if max := daysInMonth(y, m); day > max {
		day = max
	}
	base := time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC)
	return base.AddDate(0, 0, days)
}

func daysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, b int) int {
	return a - floorDiv(a, b)*b
}

// dateBin buckets source into the half-open bin aligned to origin by the
// (years, months, days) stride, matching upstream beanquery's date_bin.
// A non-positive stride (a stride that does not advance origin) yields a
// NULL Date. Month/year strides iterate addInterval (so end-of-month
// clamping accumulates across steps, matching dateutil); pure-day strides
// align by floor division.
func dateBin(years, months, days int, source, origin time.Time) types.Value {
	if years != 0 || months != 0 {
		if next := addInterval(origin, years, months, days); !next.After(origin) {
			return types.Null(types.Date)
		}
		if !source.Before(origin) {
			prev := origin
			n := origin
			for {
				n = addInterval(n, years, months, days)
				if !n.Before(source) {
					return types.NewDate(prev)
				}
				prev = n
			}
		}
		n := origin
		for {
			n = addInterval(n, -years, -months, -days)
			if !n.After(source) {
				return types.NewDate(n)
			}
		}
	}
	if days <= 0 {
		return types.Null(types.Date)
	}
	diffDays := int(source.Sub(origin) / (24 * time.Hour))
	aligned := floorDiv(diffDays, days) * days
	return types.NewDate(origin.AddDate(0, 0, aligned))
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
