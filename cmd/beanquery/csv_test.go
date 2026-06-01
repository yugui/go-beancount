package main

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// parseCSV reads all records from s using csv.NewReader.
func parseCSV(t *testing.T, s string) [][]string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(s)).ReadAll()
	if err != nil {
		t.Fatalf("parseCSV: %v", err)
	}
	return records
}

func TestFormatterFor_CSV(t *testing.T) {
	f, err := formatterFor("csv")
	if err != nil {
		t.Fatalf("formatterFor(\"csv\") error: %v", err)
	}
	if f == nil {
		t.Fatal("formatterFor(\"csv\") returned nil")
	}
}

func TestCSVFormatter_ZeroRows(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "account", Type: types.String},
			{Name: "total", Type: types.Decimal},
		},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	want := [][]string{{"account", "total"}}
	if d := cmp.Diff(want, records); d != "" {
		t.Errorf("zero-row CSV mismatch (-want +got):\n%s", d)
	}
}

func TestCSVFormatter_Scalars(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "flag", Type: types.Bool},
			{Name: "count", Type: types.Int},
			{Name: "price", Type: types.Decimal},
			{Name: "name", Type: types.String},
			{Name: "day", Type: types.Date},
		},
		Rows: [][]types.Value{
			{
				types.NewBool(true),
				types.NewInt(42),
				types.NewDecimal(mustDecimal(t, "1.50")),
				types.NewString("hello"),
				types.NewDate(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)),
			},
		},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	want := [][]string{
		{"flag", "count", "price", "name", "day"},
		{"true", "42", "1.50", "hello", "2024-03-15"},
	}
	if d := cmp.Diff(want, records); d != "" {
		t.Errorf("scalars CSV mismatch (-want +got):\n%s", d)
	}
}

func TestCSVFormatter_NullCell(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "x", Type: types.String},
			{Name: "y", Type: types.Amount},
		},
		Rows: [][]types.Value{
			{types.Null(types.String), types.Null(types.Amount)},
		},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	// NULL → empty field
	want := [][]string{
		{"x", "y"},
		{"", ""},
	}
	if d := cmp.Diff(want, records); d != "" {
		t.Errorf("null cell mismatch (-want +got):\n%s", d)
	}
}

func TestCSVFormatter_Amount(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{{Name: "amt", Type: types.Amount}},
		Rows: [][]types.Value{
			{types.NewAmount(ast.Amount{Number: mustDecimal(t, "1000"), Currency: "USD"})},
		},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	if got := records[1][0]; got != "1000 USD" {
		t.Errorf("amount cell = %q, want %q", got, "1000 USD")
	}
}

func TestCSVFormatter_PositionNoCost(t *testing.T) {
	pos := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "AAPL"},
	}
	result := query.Result{
		Columns: []query.Column{{Name: "p", Type: types.Position}},
		Rows:    [][]types.Value{{types.NewPosition(pos)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	if got := records[1][0]; got != "10 AAPL" {
		t.Errorf("position-no-cost cell = %q, want %q", got, "10 AAPL")
	}
}

func TestCSVFormatter_PositionFullCost(t *testing.T) {
	pos := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "AAPL"},
		Cost: &inventory.Lot{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Date:     time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-a",
		},
	}
	result := query.Result{
		Columns: []query.Column{{Name: "p", Type: types.Position}},
		Rows:    [][]types.Value{{types.NewPosition(pos)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	want := `10 AAPL {100 USD, 2023-06-01, "lot-a"}`
	if got := records[1][0]; got != want {
		t.Errorf("position-full-cost cell = %q, want %q", got, want)
	}
}

func TestCSVFormatter_PositionCostLabelNoDate(t *testing.T) {
	pos := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "AAPL"},
		Cost: &inventory.Lot{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Label:    "foo",
		},
	}
	result := query.Result{
		Columns: []query.Column{{Name: "p", Type: types.Position}},
		Rows:    [][]types.Value{{types.NewPosition(pos)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	want := `10 AAPL {100 USD, "foo"}`
	if got := records[1][0]; got != want {
		t.Errorf("position-label-no-date cell = %q, want %q", got, want)
	}
}

func TestCSVFormatter_PositionCostNeitherDateNorLabel(t *testing.T) {
	pos := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "AAPL"},
		Cost: &inventory.Lot{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
		},
	}
	result := query.Result{
		Columns: []query.Column{{Name: "p", Type: types.Position}},
		Rows:    [][]types.Value{{types.NewPosition(pos)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	want := "10 AAPL {100 USD}"
	if got := records[1][0]; got != want {
		t.Errorf("position-no-date-no-label cell = %q, want %q", got, want)
	}
}

func TestCSVFormatter_InventoryMultiplePositions(t *testing.T) {
	inv := inventory.NewInventory()
	p1 := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "10"), Currency: "AAPL"},
		Cost: &inventory.Lot{
			Number:   mustDecimal(t, "100"),
			Currency: "USD",
			Date:     time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-a",
		},
	}
	p2 := inventory.Position{
		Units: ast.Amount{Number: mustDecimal(t, "5"), Currency: "GOOG"},
	}
	if err := inv.Add(p1); err != nil {
		t.Fatalf("inv.Add p1: %v", err)
	}
	if err := inv.Add(p2); err != nil {
		t.Fatalf("inv.Add p2: %v", err)
	}

	result := query.Result{
		Columns: []query.Column{{Name: "inv", Type: types.Inventory}},
		Rows:    [][]types.Value{{types.NewInventory(inv)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	// The outer csv field contains a nested CSV record. Parse the cell itself.
	outerCell := records[1][0]
	inner := parseCSV(t, outerCell+"\n")
	wantInner := [][]string{{`10 AAPL {100 USD, 2023-06-01, "lot-a"}`, "5 GOOG"}}
	if d := cmp.Diff(wantInner, inner); d != "" {
		t.Errorf("inventory nested CSV mismatch (-want +got):\n%s", d)
	}
}

func TestCSVFormatter_EmptyInventory(t *testing.T) {
	inv := inventory.NewInventory()
	result := query.Result{
		Columns: []query.Column{{Name: "inv", Type: types.Inventory}},
		Rows:    [][]types.Value{{types.NewInventory(inv)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// A single empty field renders as a blank line, which csv.Reader skips,
	// so assert the raw bytes: header line followed by an empty row.
	if got, want := buf.String(), "inv\n\n"; got != want {
		t.Errorf("empty inventory output = %q, want %q", got, want)
	}
}

func TestCSVFormatter_SetWithComma(t *testing.T) {
	// An element containing a comma forces nested CSV quoting.
	s := types.NewSet("a,b", "c")
	result := query.Result{
		Columns: []query.Column{{Name: "tags", Type: types.SetType}},
		Rows:    [][]types.Value{{s}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	// The nested record must occupy exactly one outer field; a regression in
	// outer quoting would split it into several.
	if got := len(records[1]); got != 1 {
		t.Fatalf("outer record has %d fields, want 1: %q", got, records[1])
	}
	// The outer field is the nested CSV record; parse it back.
	outerCell := records[1][0]
	inner := parseCSV(t, outerCell+"\n")
	// Elements are in ascending order: "a,b" < "c".
	wantInner := [][]string{{"a,b", "c"}}
	if d := cmp.Diff(wantInner, inner); d != "" {
		t.Errorf("set-with-comma nested CSV mismatch (-want +got):\n%s", d)
	}
}

func TestCSVFormatter_EmptySet(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{{Name: "tags", Type: types.SetType}},
		Rows:    [][]types.Value{{types.NewSet()}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// A single empty field renders as a blank line, which csv.Reader skips,
	// so assert the raw bytes: header line followed by an empty row.
	if got, want := buf.String(), "tags\n\n"; got != want {
		t.Errorf("empty set output = %q, want %q", got, want)
	}
}

func TestCSVFormatter_DictValueWithCommaAndColon(t *testing.T) {
	d := types.NewDict(map[string]types.Value{
		"k": types.NewString("a, b"),
		"n": types.NewInt(1),
		"m": types.NewString("x:y"),
	})
	result := query.Result{
		Columns: []query.Column{{Name: "meta", Type: types.DictType}},
		Rows:    [][]types.Value{{d}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	got := records[1][0]
	// Keys in ascending order: k, m, n.
	// "a, b" contains a comma → value is CSV-quoted → k:"a, b"
	// "x:y" contains a colon → value is CSV-quoted → m:"x:y"
	// 1 has no special chars → n:1
	want := `k:"a, b",m:"x:y",n:1`
	if got != want {
		t.Errorf("dict cell = %q, want %q", got, want)
	}
}

func TestCSVFormatter_DictValueQuotingTriggers(t *testing.T) {
	// A real newline must trigger value quoting; a plain alphabetic value
	// (no comma/colon/quote/CR/LF) must NOT be quoted, even if it contains
	// letters like 'r' or 'n'.
	d := types.NewDict(map[string]types.Value{
		"nl":    types.NewString("a\nb"),
		"plain": types.NewString("rain"),
	})
	result := query.Result{
		Columns: []query.Column{{Name: "meta", Type: types.DictType}},
		Rows:    [][]types.Value{{d}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	// Keys ascending: nl, plain.
	want := "nl:\"a\nb\",plain:rain"
	if got := records[1][0]; got != want {
		t.Errorf("dict cell = %q, want %q", got, want)
	}
}

func TestCSVFormatter_DictCompositeAndQuoteValue(t *testing.T) {
	// A composite value (Amount) renders bare; a string value containing a
	// double-quote forces CSV-style "" doubling within the dict value.
	d := types.NewDict(map[string]types.Value{
		"amt": types.NewAmount(ast.Amount{Number: mustDecimal(t, "1000"), Currency: "USD"}),
		"q":   types.NewString(`a"b`),
	})
	result := query.Result{
		Columns: []query.Column{{Name: "meta", Type: types.DictType}},
		Rows:    [][]types.Value{{d}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	records := parseCSV(t, buf.String())
	// Keys ascending: amt, q. "1000 USD" has no trigger char → bare.
	// `a"b` contains a quote → value quoted, inner quote doubled → q:"a""b".
	want := `amt:1000 USD,q:"a""b"`
	if got := records[1][0]; got != want {
		t.Errorf("dict cell = %q, want %q", got, want)
	}
}

func TestCSVFormatter_EmptyDict(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{{Name: "meta", Type: types.DictType}},
		Rows:    [][]types.Value{{types.NewDict(nil)}},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// A single empty field renders as a blank line, which csv.Reader skips,
	// so assert the raw bytes: header line followed by an empty row.
	if got, want := buf.String(), "meta\n\n"; got != want {
		t.Errorf("empty dict output = %q, want %q", got, want)
	}
}

// TestCSVFormatter_ScalarGolden pins the exact byte output for a simple
// all-scalar result: header + one data row, encoding/csv uses \n line endings.
func TestCSVFormatter_ScalarGolden(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "name", Type: types.String},
			{Name: "count", Type: types.Int},
		},
		Rows: [][]types.Value{
			{types.NewString("foo"), types.NewInt(7)},
		},
	}
	f := mustFormatterFor(t, "csv")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := "name,count\nfoo,7\n"
	if got := buf.String(); got != want {
		t.Errorf("scalar golden mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
