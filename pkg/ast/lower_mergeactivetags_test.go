package ast

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// mergeActiveTagsDeterminismIters is the iteration count used by the
// determinism tests below. Go's map iteration order is randomized per
// call: any single call to `range` over a map with N>=2 keys produces
// one of N! possible orders chosen pseudo-randomly. A regression that
// removes the sort and emits keys in raw map-iteration order would
// therefore disagree with the first observed order on each subsequent
// call with probability roughly 1 - 1/N!, i.e. for N=4 about 23/24.
// 200 repeats drives the probability of a false-negative regression
// below (1/24)^199 — vastly below any realistic flake budget.
const mergeActiveTagsDeterminismIters = 200

// TestMergeActiveTagsSortedOrder pins the explicit contract that
// active tags merge in lexical order, regardless of the order in which
// they were pushed onto the active set. This guards the contract
// (documented on mergeActiveTags) that the output order is sorted —
// not merely stable against e.g. insertion order — so that golden
// tests and formatter round-trips can compare merged slices for
// equality across runs and across hosts.
func TestMergeActiveTagsSortedOrder(t *testing.T) {
	active := map[string]struct{}{
		"z": {},
		"a": {},
		"m": {},
		"b": {},
	}
	got := mergeActiveTags(nil, active)
	want := []string{"a", "b", "m", "z"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mergeActiveTags mismatch (-want +got):\n%s", diff)
	}
}

// TestMergeActiveTagsDeterministic exercises mergeActiveTags many
// times against a fresh active-tag map populated in a deterministic
// (but non-sorted) order, asserting the output slice is byte-equal
// across all iterations. Without the explicit sort inside
// mergeActiveTags the test would fail with overwhelming probability
// because Go randomizes map iteration order per call.
func TestMergeActiveTagsDeterministic(t *testing.T) {
	first := mergeActiveTags(nil, makeActiveTagsForDeterminismTest())
	for i := 0; i < mergeActiveTagsDeterminismIters; i++ {
		got := mergeActiveTags(nil, makeActiveTagsForDeterminismTest())
		if diff := cmp.Diff(first, got); diff != "" {
			t.Fatalf("iteration %d: mergeActiveTags output must be deterministic across calls (-want +got):\n%s", i, diff)
		}
	}
}

// TestMergeActiveTagsPreservesExplicitTagOrder verifies that explicit
// (inline) tags appear first in their given order, with active tags
// appended in lexical order after them and any duplicates skipped.
// This pins the full ordering contract in one place.
func TestMergeActiveTagsPreservesExplicitTagOrder(t *testing.T) {
	explicit := []string{"foo", "bar"}
	active := map[string]struct{}{
		"z":   {},
		"bar": {}, // duplicate of explicit; must be skipped
		"a":   {},
		"m":   {},
	}
	got := mergeActiveTags(explicit, active)
	want := []string{"foo", "bar", "a", "m", "z"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mergeActiveTags mismatch (-want +got):\n%s", diff)
	}
}

// makeActiveTagsForDeterminismTest builds an active-tag set with
// enough keys that any single map iteration almost certainly disagrees
// with sorted order, so a regression that drops the sort would surface
// as differing outputs across calls.
func makeActiveTagsForDeterminismTest() map[string]struct{} {
	return map[string]struct{}{
		"alpha":   {},
		"bravo":   {},
		"charlie": {},
		"delta":   {},
		"echo":    {},
	}
}
