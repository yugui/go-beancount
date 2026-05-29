package types_test

import (
	"slices"
	"testing"

	"github.com/yugui/go-beancount/pkg/query/types"
)

func TestSetDedupAndSort(t *testing.T) {
	s := types.NewSet("c", "a", "b", "a", "c")
	if got, want := s.Elements(), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("Elements() = %v, want %v", got, want)
	}
	if s.Len() != 3 {
		t.Errorf("Len() = %d, want 3", s.Len())
	}
}

func TestSetOrderIndependentEquality(t *testing.T) {
	a := types.NewSet("x", "y", "z")
	b := types.NewSet("z", "y", "x", "y")
	if a.Compare(b) != 0 {
		t.Errorf("sets with same elements in different input order compare %d, want 0", a.Compare(b))
	}
}

func TestSetContains(t *testing.T) {
	s := types.NewSet("tag1", "tag2")
	if !s.Contains("tag1") {
		t.Error("Contains(tag1) = false")
	}
	if s.Contains("missing") {
		t.Error("Contains(missing) = true")
	}
}

func TestSetCompareDeterministic(t *testing.T) {
	empty := types.NewSet()
	a := types.NewSet("a")
	ab := types.NewSet("a", "b")
	b := types.NewSet("b")
	// empty < {a} < {a,b} < {b}: prefix sorts before extension; "a" < "b".
	ordered := []types.Value{empty, a, ab, b}
	for i := 0; i+1 < len(ordered); i++ {
		if ordered[i].Compare(ordered[i+1]) != -1 {
			t.Errorf("expected %s < %s", ordered[i].Format(), ordered[i+1].Format())
		}
	}
}

func TestSetElementsCopyIsolation(t *testing.T) {
	s := types.NewSet("a", "b")
	got := s.Elements()
	got[0] = "mutated"
	if s.Contains("mutated") {
		t.Error("mutating Elements() result affected the Set")
	}
}

func TestSetInputCopyIsolation(t *testing.T) {
	in := []string{"a", "b"}
	s := types.NewSet(in...)
	in[0] = "mutated"
	if s.Contains("mutated") || !s.Contains("a") {
		t.Error("Set retained an alias to the input slice")
	}
}
