package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
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
	want := "[\n  {\n    \"label\": \"foo\",\n    \"count\": 7,\n    \"amount\": \"1.50\"\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("scalars output mismatch:\ngot:  %q\nwant: %q", got, want)
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
	want := "[\n  {\n    \"x\": null\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("null cell output = %q, want %q", got, want)
	}
}

func TestJSONFormatter_ColumnOrder(t *testing.T) {
	// Column names in non-alphabetical order: "zebra" before "apple".
	// A plain map[string]any would emit "apple" first; column order must be preserved.
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
	want := "[\n  {\n    \"zebra\": \"z\",\n    \"apple\": 1\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("column order output = %q, want %q", got, want)
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
	// Amount keys are sorted alphabetically by encoding/json: "currency" before "number".
	// Set is sorted ascending: ["a","b"].
	// Dict has one key "k".
	// Inventory contains one position: {"cost":null,"units":{"currency":"USD","number":"10"}}.
	want := "[\n" +
		"  {\n" +
		"    \"amt\": {\n" +
		"      \"currency\": \"USD\",\n" +
		"      \"number\": \"12.50\"\n" +
		"    },\n" +
		"    \"tags\": [\n" +
		"      \"a\",\n" +
		"      \"b\"\n" +
		"    ],\n" +
		"    \"meta\": {\n" +
		"      \"k\": \"v\"\n" +
		"    },\n" +
		"    \"inv\": [\n" +
		"      {\n" +
		"        \"cost\": null,\n" +
		"        \"units\": {\n" +
		"          \"currency\": \"USD\",\n" +
		"          \"number\": \"10\"\n" +
		"        }\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"]\n"
	if got := buf.String(); got != want {
		t.Errorf("composite output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
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
	// encoding/json escapes " as \" and passes non-ASCII runes through as raw
	// UTF-8 (it escapes only <, >, &, and control characters), so é stays é.
	want := "[\n  {\n    \"s\": \"say \\\"héllo\\\"\"\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("string escaping output = %q, want %q", got, want)
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
	want := "[\n  {\n    \"n\": \"a\",\n    \"v\": 1\n  },\n  {\n    \"n\": \"b\",\n    \"v\": 2\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("jsonFormatter.Format() multi-row output = %q, want %q", got, want)
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
	want := "[\n  {\n    \"p\": {\n      \"cost\": {\n        \"currency\": \"USD\",\n        \"date\": \"2023-06-01\",\n        \"label\": \"lot-a\",\n        \"number\": \"100.00\"\n      },\n      \"units\": {\n        \"currency\": \"AAPL\",\n        \"number\": \"10\"\n      }\n    }\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("jsonFormatter.Format() position-with-cost output = %q, want %q", got, want)
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
	want := "[\n  {\n    \"a\": null\n  }\n]\n"
	if got := buf.String(); got != want {
		t.Errorf("jsonFormatter.Format() null-composite output = %q, want %q", got, want)
	}
}
