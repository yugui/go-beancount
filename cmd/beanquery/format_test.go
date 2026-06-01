package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

func mustFormatterFor(t *testing.T, name string) Formatter {
	t.Helper()
	f, err := formatterFor(name)
	if err != nil {
		t.Fatalf("formatterFor(%q): %v", name, err)
	}
	return f
}

func TestFormatterFor_Text(t *testing.T) {
	f, err := formatterFor("text")
	if err != nil {
		t.Fatalf("formatterFor(%q) error: %v", "text", err)
	}
	if f == nil {
		t.Fatal("formatterFor(\"text\") returned nil Formatter")
	}
}

func TestFormatterFor_Unknown(t *testing.T) {
	_, err := formatterFor("bogus")
	if err == nil {
		t.Fatal("formatterFor(\"bogus\") returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %q, want it to name the bad format", err.Error())
	}
}

func TestTextFormatter_Format_WithRows(t *testing.T) {
	// Two columns: a string (left-aligned) and an int (right-aligned).
	result := query.Result{
		Columns: []query.Column{
			{Name: "name", Type: types.String},
			{Name: "count", Type: types.Int},
		},
		Rows: [][]types.Value{
			{types.NewString("foo"), types.NewInt(1)},
			{types.NewString("longname"), types.NewInt(42)},
		},
	}

	f := mustFormatterFor(t, "text")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}

	got := buf.String()
	// Header row must contain both column names.
	if !strings.Contains(got, "name") || !strings.Contains(got, "count") {
		t.Errorf("output missing column names:\n%s", got)
	}
	// Data rows must be present.
	if !strings.Contains(got, "foo") || !strings.Contains(got, "42") {
		t.Errorf("output missing row data:\n%s", got)
	}

	// Column alignment: the int column is right-aligned so "1" should be
	// padded to the width of "count" (5 chars) and "42" to 5 chars, left-padded.
	// Verify by checking that each line has a consistent structure.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (header + 2 rows), got %d:\n%s", len(lines), got)
	}

	// All lines must have the same length (last col is trimmed trailing space,
	// but numeric right-align means no trailing space on the count column).
	headerLen := len(lines[0])
	for i, line := range lines[1:] {
		if len(line) != headerLen {
			t.Errorf("line %d len=%d, want %d (alignment broken):\n%s", i+1, len(line), headerLen, got)
		}
	}
}

func TestTextFormatter_Format_ZeroRows(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "account", Type: types.String},
			{Name: "total", Type: types.Decimal},
		},
		Rows: nil,
	}

	f := mustFormatterFor(t, "text")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("zero-row result: want 1 line (header only), got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "account") || !strings.Contains(lines[0], "total") {
		t.Errorf("header = %q, want it to name both columns", lines[0])
	}
}

func TestTextFormatter_Format_ZeroColumns(t *testing.T) {
	result := query.Result{}

	f := mustFormatterFor(t, "text")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("zero-column result: want empty output, got %q", buf.String())
	}
}

// failWriter returns errWriteFailed on the first write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

var errWriteFailed = errors.New("write failed")

func TestTextFormatter_Format_WriteError(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{{Name: "x", Type: types.String}},
		Rows:    [][]types.Value{{types.NewString("hello")}},
	}

	f := mustFormatterFor(t, "text")
	err := f.Format(failWriter{}, result)
	if !errors.Is(err, errWriteFailed) {
		t.Fatalf("Format with failing writer: err = %v, want %v", err, errWriteFailed)
	}
}

// TestTextFormatter_Format_Golden pins the exact byte output, including the
// two-space column separator, per-column widths, numeric right-alignment, and
// trailing-space trim. It is the byte-for-byte guard for the table rendering.
func TestTextFormatter_Format_Golden(t *testing.T) {
	result := query.Result{
		Columns: []query.Column{
			{Name: "name", Type: types.String},
			{Name: "count", Type: types.Int},
		},
		Rows: [][]types.Value{
			{types.NewString("foo"), types.NewInt(1)},
			{types.NewString("longname"), types.NewInt(42)},
		},
	}

	f := mustFormatterFor(t, "text")
	var buf bytes.Buffer
	if err := f.Format(&buf, result); err != nil {
		t.Fatalf("Format error: %v", err)
	}

	want := "name      count\n" +
		"foo           1\n" +
		"longname     42\n"
	if got := buf.String(); got != want {
		t.Errorf("Format output mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
}
