package csvbase

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// These are white-box unit tests for MappingState. Its fields are unexported and
// it is constructed only by Pipeline.Map, so exercising the accessors through
// the public API would require building a full Builder/Emit/Map pipeline per
// case. Constructing the value directly keeps each test focused on a single
// accessor contract (At, Info, Row, Value) in isolation; the pipeline-level
// plumbing is covered separately in pipeline_test.go.

func TestMappingState_At(t *testing.T) {
	ms := &MappingState{
		raw:   []string{"alpha"},
		index: map[string]int{"A": 0, "B": 1}, // B's position is past the short row
	}
	if got := ms.At("A"); got != "alpha" {
		t.Errorf("At(A) = %q, want %q", got, "alpha")
	}
	if got := ms.At("Z"); got != "" {
		t.Errorf("At(Z) = %q, want %q (absent column)", got, "")
	}
	if got := ms.At("B"); got != "" {
		t.Errorf("At(B) = %q, want %q (short row)", got, "")
	}
}

func TestMappingState_Info(t *testing.T) {
	want := RowInfo{Path: "/bank/statement.csv", Line: 7, Hints: map[string]string{"account": "Expenses:Food"}}
	ms := &MappingState{info: want}
	if diff := cmp.Diff(want, ms.Info()); diff != "" {
		t.Errorf("Info() mismatch (-want +got):\n%s", diff)
	}
}

func TestMappingState_Row(t *testing.T) {
	ms := &MappingState{
		raw:   []string{"a", "b"},
		index: map[string]int{"X": 0, "Y": 1, "Z": 2}, // Z's position is past the short row
	}
	want := map[string]string{"X": "a", "Y": "b", "Z": ""}
	got := ms.Row()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Row() mismatch (-want +got):\n%s", diff)
	}
	// Row returns a fresh map each call; mutating the result must not leak back.
	got["X"] = "mutated"
	if again := ms.Row(); again["X"] != "a" {
		t.Errorf("Row() not fresh: after mutation got X=%q, want %q", again["X"], "a")
	}
}

func TestValue(t *testing.T) {
	diag := ErrorDiag("boom", "/f.csv", 3, "bad")
	ms := &MappingState{
		results: map[string]result{
			"s":   {value: "hi"},
			"n":   {value: 7},
			"bad": {diag: &diag},
		},
	}
	if got, d := Value(ms, Key[string]{name: "s"}); got != "hi" || d != nil {
		t.Errorf("Value(s) = (%q, %v), want (%q, nil)", got, d, "hi")
	}
	if got, d := Value(ms, Key[int]{name: "n"}); got != 7 || d != nil {
		t.Errorf("Value(n) = (%d, %v), want (7, nil)", got, d)
	}
	// A soft-failed step yields the zero value and its diagnostic.
	if got, d := Value(ms, Key[string]{name: "bad"}); got != "" || d == nil || d.Code != "boom" {
		t.Errorf("Value(bad) = (%q, %v), want (%q, boom diag)", got, d, "")
	}
	// A key not produced by this pipeline yields (zero, nil).
	if got, d := Value(ms, Key[string]{name: "missing"}); got != "" || d != nil {
		t.Errorf("Value(missing) = (%q, %v), want (%q, nil)", got, d, "")
	}
}
