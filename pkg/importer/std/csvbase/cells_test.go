package csvbase_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
)

// point is a local struct type used to verify Value round-trips over struct types.
type point struct{ X, Y int }

// TestValue_RoundTrips verifies that AddStep + Value correctly round-trips
// several distinct types (string, int, time.Time, struct).
func TestValue_RoundTrips(t *testing.T) {
	wantStr := "hello"
	wantInt := 42
	wantTime := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	wantPoint := point{3, 7}

	b := csvbase.NewBuilder()
	kStr := csvbase.AddStep(b, func(*csvbase.Cells) (string, *ast.Diagnostic, error) {
		return wantStr, nil, nil
	})
	kInt := csvbase.AddStep(b, func(*csvbase.Cells) (int, *ast.Diagnostic, error) {
		return wantInt, nil, nil
	})
	kTime := csvbase.AddStep(b, func(*csvbase.Cells) (time.Time, *ast.Diagnostic, error) {
		return wantTime, nil, nil
	})
	kPoint := csvbase.AddStep(b, func(*csvbase.Cells) (point, *ast.Diagnostic, error) {
		return wantPoint, nil, nil
	})

	var gotStr string
	var gotInt int
	var gotTime time.Time
	var gotPoint point
	p := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		gotStr, _ = csvbase.Value(c, kStr)
		gotInt, _ = csvbase.Value(c, kInt)
		gotTime, _ = csvbase.Value(c, kTime)
		gotPoint, _ = csvbase.Value(c, kPoint)
		return nil, nil, nil
	})

	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotStr != wantStr {
		t.Errorf("string value = %q, want %q", gotStr, wantStr)
	}
	if gotInt != wantInt {
		t.Errorf("int value = %d, want %d", gotInt, wantInt)
	}
	if !gotTime.Equal(wantTime) {
		t.Errorf("time value = %v, want %v", gotTime, wantTime)
	}
	if gotPoint != wantPoint {
		t.Errorf("point value = %v, want %v", gotPoint, wantPoint)
	}
}

// TestValue_AbsentKey verifies that Value returns (zero, nil) for a key not
// produced by the pipeline (e.g., created from a different builder).
func TestValue_AbsentKey(t *testing.T) {
	otherBuilder := csvbase.NewBuilder()
	orphan := csvbase.AddStep(otherBuilder, func(*csvbase.Cells) (string, *ast.Diagnostic, error) {
		return "orphan", nil, nil
	})

	b := csvbase.NewBuilder()
	p := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		v, diag := csvbase.Value(c, orphan)
		if v != "" || diag != nil {
			t.Errorf("absent key: got (%q, %v), want (\"\", nil)", v, diag)
		}
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
}

// TestCells_Field verifies Field returns raw cell values and "" for absent or
// out-of-range columns.
func TestCells_Field(t *testing.T) {
	b := csvbase.NewBuilder()
	var gotA, gotMissing, gotShort string
	p := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		gotA = c.Field("A")
		gotMissing = c.Field("Z")
		gotShort = c.Field("B")
		return nil, nil, nil
	})

	// Row has 2 columns in header but only 1 field value; "B" index=1 is out of range.
	rec := csvbase.RowContext{
		Fields: []string{"alpha"},
		Index:  map[string]int{"A": 0, "B": 1},
	}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotA != "alpha" {
		t.Errorf("Field(A) = %q, want %q", gotA, "alpha")
	}
	if gotMissing != "" {
		t.Errorf("Field(Z) = %q, want %q", gotMissing, "")
	}
	if gotShort != "" {
		t.Errorf("Field(B) short row = %q, want %q", gotShort, "")
	}
}

// TestCells_Info verifies Info fields match the RowContext.
func TestCells_Info(t *testing.T) {
	hints := map[string]string{"account": "Expenses:Food"}
	b := csvbase.NewBuilder()
	var got csvbase.RowInfo
	p := b.Emit(func(_ context.Context, c *csvbase.Cells) ([]ast.Directive, []ast.Diagnostic, error) {
		got = c.Info()
		return nil, nil, nil
	})

	rec := csvbase.RowContext{
		Path:   "/bank/statement.csv",
		Line:   7,
		Fields: []string{},
		Index:  map[string]int{},
		Hints:  hints,
	}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	want := csvbase.RowInfo{Path: rec.Path, Line: rec.Line, Hints: hints}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Info() mismatch (-want +got):\n%s", diff)
	}
}
