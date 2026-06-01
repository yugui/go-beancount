package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// csvFormatter renders a query.Result as RFC 4180 CSV. The first row is a
// header of column names; each subsequent row encodes one result row. Composite
// values (Position, Inventory, Set, Dict) are encoded as beancount-flavored
// strings that may themselves contain embedded CSV records.
type csvFormatter struct{}

// Format writes result as CSV: a header row followed by one row per result
// row. It uses encoding/csv for outer RFC 4180 framing and Flush; any write
// error or cell-encoding error is returned and the output is left partially
// written.
func (csvFormatter) Format(w io.Writer, result query.Result) error {
	cw := csv.NewWriter(w)

	header := make([]string, len(result.Columns))
	for j, c := range result.Columns {
		header[j] = c.Name
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, row := range result.Rows {
		record := make([]string, len(row))
		for j, v := range row {
			s, err := csvCell(v)
			if err != nil {
				return err
			}
			record[j] = s
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}

// csvCell encodes a single BQL value as a CSV cell string. The returned string
// is the raw field content; the caller's csv.Writer handles outer RFC 4180
// quoting. NULL → "". Unsupported kinds (Any, Invalid) return an error.
//
// Composite encodings:
//   - Position: "<units>[ {<cost components>}]" with full cost detail.
//   - Inventory: nested CSV record of position strings, insertion order.
//   - Set: nested CSV record of elements, ascending order.
//   - Dict: "key:value,..." ascending by key; values are CSV-quoted when they
//     contain a comma, colon, double-quote, CR, or LF.
//   - Entry: the directive's JSON object (same as Format).
func csvCell(v types.Value) (string, error) {
	if v.IsNull() {
		return "", nil
	}

	// Each As* accessor below cannot fail: v.Type() has already matched.
	switch v.Type() {
	case types.Bool, types.Int, types.Decimal, types.String, types.Date, types.Amount, types.Interval, types.Entry:
		return v.Format(), nil

	case types.Position:
		p, _ := types.AsPosition(v)
		return csvPosition(p), nil

	case types.Inventory:
		inv, _ := types.AsInventory(v)
		if inv.Len() == 0 {
			return "", nil
		}
		var fields []string
		for p := range inv.All() { // insertion order
			fields = append(fields, csvPosition(p))
		}
		return nestedCSVRecord(fields), nil

	case types.SetType:
		s, _ := types.AsSet(v)
		elems := s.Elements()
		if len(elems) == 0 {
			return "", nil
		}
		return nestedCSVRecord(elems), nil

	case types.DictType:
		d, _ := types.AsDict(v)
		keys := d.Keys()
		if len(keys) == 0 {
			return "", nil
		}
		var b strings.Builder
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			val, _ := d.Get(k)
			vs, err := csvCell(val)
			if err != nil {
				return "", err
			}
			// keys are metadata identifiers, free of delimiters, so unquoted
			b.WriteString(k)
			b.WriteByte(':')
			b.WriteString(csvDictValueQuote(vs))
		}
		return b.String(), nil

	default:
		return "", fmt.Errorf("csv: unsupported value type %s", v.Type())
	}
}

// csvPosition renders p as "<units>[ {<cost>}]"; the cost is omitted when nil.
func csvPosition(p inventory.Position) string {
	s := p.Units.Number.Text('f') + " " + p.Units.Currency
	if p.Cost != nil {
		s += " " + csvCostLiteral(p.Cost)
	}
	return s
}

// csvCostLiteral builds "{number currency[, date][, label]}" from a Lot.
func csvCostLiteral(lot *inventory.Lot) string {
	parts := []string{lot.Number.Text('f') + " " + lot.Currency}
	if !lot.Date.IsZero() {
		parts = append(parts, lot.Date.Format("2006-01-02"))
	}
	if lot.Label != "" {
		parts = append(parts, beancountQuoteLocal(lot.Label))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// nestedCSVRecord encodes fields as a single CSV record (no trailing newline).
func nestedCSVRecord(fields []string) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(fields) // bytes.Buffer.Write never fails
	w.Flush()
	s := buf.String()
	return strings.TrimSuffix(s, "\n")
}

// csvDictValueQuote wraps s in double quotes (with internal " doubled) when s
// contains a comma, colon, double-quote, CR, or LF; otherwise returns s unchanged.
func csvDictValueQuote(s string) string {
	if !strings.ContainsAny(s, ",:\"\r\n") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// beancountQuoteLocal wraps s in double quotes, escaping only \ and ".
// This mirrors pkg/printer.beancountQuote, which is unexported.
func beancountQuoteLocal(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}
