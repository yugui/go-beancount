package main

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// jsonFormatter renders a query.Result as a pretty-printed JSON array of
// row objects.
type jsonFormatter struct{}

// Format writes result as a pretty-printed JSON array of row objects, one
// object per row. Keys appear in column order (not alphabetical). Output is
// terminated by a single newline. An empty result writes "[]" followed by a
// newline.
func (jsonFormatter) Format(w io.Writer, result query.Result) error {
	rows := make([]jsonRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		rows = append(rows, jsonRow{cols: result.Columns, vals: row})
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// jsonRow encodes a single result row as a JSON object whose keys follow
// column order rather than alphabetical order.
type jsonRow struct {
	cols []query.Column
	vals []types.Value
}

// MarshalJSON encodes the row as a JSON object with keys in column order,
// which a map[string]any could not preserve. cols and vals must have equal
// length (the positional row/column alignment guaranteed by query.Result).
func (r jsonRow) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, col := range r.cols {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(col.Name)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(types.MarshalTree(r.vals[i]))
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
