package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func mustDecimal(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("mustDecimal(%q): %v", s, err)
	}
	return d
}

// decodeJSON parses s into an order-independent Go value so tests compare JSON
// by structure and content rather than by byte-exact indentation. JSON objects
// become map[string]any, dropping key order; when key order is itself the
// contract under test, assert it separately (see TestJSONFormatter_ColumnOrder).
func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding JSON %q: %v", s, err)
	}
	return v
}

func TestFormatterFor_JSON(t *testing.T) {
	f, err := formatterFor("json")
	if err != nil {
		t.Fatalf("formatterFor(\"json\") error: %v", err)
	}
	if f == nil {
		t.Fatal("formatterFor(\"json\") returned nil")
	}
}

func TestJSONFormatter_ZeroRows(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "account", Type: types.String},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if got := buf.String(); got != "[]\n" {
		t.Errorf("zero-row output = %q, want %q", got, "[]\n")
	}
}

func TestJSONFormatter_Scalars(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "label", Type: types.String},
			{Name: "count", Type: types.Int},
			{Name: "amount", Type: types.Decimal},
		},
		Rows: [][]types.Value{
			{
				types.NewString("foo"),
				types.NewInt(7),
				types.NewDecimal(mustDecimal(t, "1.50")),
			},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	got := buf.String()
	// Output is pretty-printed (indented), not compact. A single robust check
	// guards against a regression to json.Marshal without pinning the layout.
	if !strings.Contains(got, "\n  ") {
		t.Errorf("output is not indented:\n%s", got)
	}
	// Int is a JSON number; Decimal is an exact JSON string.
	want := `[
  {
    "label": "foo",
    "count": 7,
    "amount": "1.50"
  }
]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, got)); d != "" {
		t.Errorf("scalars JSON mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_NullCell(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "x", Type: types.String},
		},
		Rows: [][]types.Value{
			{types.Null(types.String)},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := `[{"x": null}]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, buf.String())); d != "" {
		t.Errorf("null cell JSON mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_ColumnOrder(t *testing.T) {
	// Column names in non-alphabetical order: "zebra" before "apple".
	// A plain map[string]any would emit "apple" first; column order must be
	// preserved, which decoding into a map cannot verify — so assert the key
	// positions directly on the serialized output.
	result := query.Result{
		Columns: []query.Column{
			{Name: "zebra", Type: types.String},
			{Name: "apple", Type: types.Int},
		},
		Rows: [][]types.Value{
			{types.NewString("z"), types.NewInt(1)},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	out := buf.String()
	zi, ai := strings.Index(out, `"zebra"`), strings.Index(out, `"apple"`)
	if zi < 0 || ai < 0 {
		t.Fatalf("output missing expected keys:\n%s", out)
	}
	if zi >= ai {
		t.Errorf("column order not preserved: zebra@%d should precede apple@%d:\n%s", zi, ai, out)
	}
	// Content is correct regardless of key order.
	want := `[{"zebra": "z", "apple": 1}]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, out)); d != "" {
		t.Errorf("column-order JSON content mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_Composite(t *testing.T) {
	inv := inventory.NewInventory()
	pos := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "USD"},
	}
	if err := inv.Add(pos); err != nil {
		t.Fatalf("inv.Add: %v", err)
	}

	result := query.Result{
		Columns: []query.Column{
			{Name: "amt", Type: types.Amount},
			{Name: "tags", Type: types.SetType},
			{Name: "meta", Type: types.DictType},
			{Name: "inv", Type: types.Inventory},
		},
		Rows: [][]types.Value{
			{
				types.NewAmount(ast.Amount{Number: mustDecimal(t, "12.50"), Currency: "USD"}),
				types.NewSet("b", "a"),
				types.NewDict(map[string]types.Value{"k": types.NewString("v")}),
				types.NewInventory(inv),
			},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// Amount/Position are objects; Set is sorted ascending; a cash position has
	// a null cost; Decimal-valued fields are exact strings.
	want := `[
  {
    "amt": {"currency": "USD", "number": "12.50"},
    "tags": ["a", "b"],
    "meta": {"k": "v"},
    "inv": [
      {"cost": null, "units": {"currency": "USD", "number": "10"}}
    ]
  }
]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, buf.String())); d != "" {
		t.Errorf("composite JSON mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_StringEscaping(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "s", Type: types.String},
		},
		Rows: [][]types.Value{
			{types.NewString(`say "héllo"`)},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	out := buf.String()
	// Serialization concern (lost on decode): quotes are escaped, but non-ASCII
	// runes pass through as raw UTF-8 rather than \u escapes.
	if !strings.Contains(out, `say \"héllo\"`) {
		t.Errorf("want escaped quotes and raw é in:\n%s", out)
	}
	if strings.Contains(out, "\\u00e9") {
		t.Errorf("é should be raw UTF-8, not \\u-escaped, in:\n%s", out)
	}
	// And it round-trips back to the original string.
	want := `[{"s": "say \"héllo\""}]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, out)); d != "" {
		t.Errorf("string-escaping JSON content mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_MultipleRows(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "n", Type: types.String},
			{Name: "v", Type: types.Int},
		},
		Rows: [][]types.Value{
			{types.NewString("a"), types.NewInt(1)},
			{types.NewString("b"), types.NewInt(2)},
		},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// Row order is significant; decoding into a slice preserves it, so cmp.Diff
	// catches a reordering regression.
	want := `[
  {"n": "a", "v": 1},
  {"n": "b", "v": 2}
]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, buf.String())); d != "" {
		t.Errorf("multi-row JSON mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_PositionWithCost(t *testing.T) {
	pos := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "AAPL"},
		Cost: &inventory.Lot{
			Number:   mustDecimal(t, "100.00"),
			Currency: "USD",
			Date:     time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-a",
		},
	}
	result := query.Result{
		Columns: []query.Column{{Name: "p", Type: types.Position}},
		Rows:    [][]types.Value{{types.NewPosition(pos)}},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := `[
  {
    "p": {
      "cost": {"currency": "USD", "date": "2023-06-01", "label": "lot-a", "number": "100.00"},
      "units": {"currency": "AAPL", "number": "10"}
    }
  }
]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, buf.String())); d != "" {
		t.Errorf("position-with-cost JSON mismatch (-want +got):\n%s", d)
	}
}

func TestJSONFormatter_NullComposite(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{{Name: "a", Type: types.Amount}},
		Rows:    [][]types.Value{{types.Null(types.Amount)}},
	}
	f := mustFormatterFor(t, "json")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := `[{"a": null}]`
	if d := cmp.Diff(decodeJSON(t, want), decodeJSON(t, buf.String())); d != "" {
		t.Errorf("null-composite JSON mismatch (-want +got):\n%s", d)
	}
}
