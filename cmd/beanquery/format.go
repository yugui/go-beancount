package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/yugui/go-beancount/pkg/query"
)

// Formatter renders a query.Result to an io.Writer. Format writes a complete
// header row followed by one row per result row and returns the first write
// error encountered, or nil on success.
type Formatter interface {
	Format(w io.Writer, result query.Result) error
}

// formatterFor returns the Formatter named by format, or an error naming the
// unknown format. Recognized names: "text".
func formatterFor(format string) (Formatter, error) {
	switch format {
	case "text":
		return textFormatter{}, nil
	default:
		return nil, fmt.Errorf("unknown output format %q", format)
	}
}

type textFormatter struct{}

func (textFormatter) Format(w io.Writer, result query.Result) error {
	n := len(result.Columns)
	if n == 0 {
		return nil
	}

	cells := make([][]string, 0, len(result.Rows)+1)
	header := make([]string, n)
	for j, c := range result.Columns {
		header[j] = c.Name
	}
	cells = append(cells, header)
	for _, row := range result.Rows {
		line := make([]string, n)
		for j, v := range row {
			line[j] = v.Format()
		}
		cells = append(cells, line)
	}

	widths := make([]int, n)
	for _, line := range cells {
		for j, s := range line {
			if wdt := runewidth.StringWidth(s); wdt > widths[j] {
				widths[j] = wdt
			}
		}
	}

	right := make([]bool, n)
	for j, c := range result.Columns {
		right[j] = isNumeric(c.Type)
	}

	var b strings.Builder
	for _, line := range cells {
		b.Reset()
		for j, s := range line {
			if j > 0 {
				b.WriteString("  ")
			}
			b.WriteString(pad(s, widths[j], right[j]))
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(b.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}
