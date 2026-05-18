// Tests in this file reach the unexported rowHash function directly.
// rowHash's serialisation contract (separator bytes, field trimming, shape-name
// prefix) is subtle and central to cross-run deduplication; verifying it
// through the exported Extract surface would require parsing emitted metadata
// and cannot distinguish boundary-collision bugs from field-value bugs.
package csvimp

import "testing"

func TestRowHash_Deterministic(t *testing.T) {
	fields := []string{"2024-01-15", "Coffee", "-4.50"}
	a := rowHash("simple", fields)
	b := rowHash("simple", fields)
	if a != b {
		t.Errorf("rowHash not deterministic: %q vs %q", a, b)
	}
}

func TestRowHash_LengthIs16Hex(t *testing.T) {
	h := rowHash("s", []string{"a", "b"})
	if len(h) != 16 {
		t.Errorf("len = %d, want 16", len(h))
	}
	for _, r := range h {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("non-lowercase-hex char %q in %q", r, h)
		}
	}
}

func TestRowHash_ShapeNameMatters(t *testing.T) {
	fields := []string{"2024-01-15", "Coffee", "-4.50"}
	a := rowHash("bank-a", fields)
	b := rowHash("bank-b", fields)
	if a == b {
		t.Errorf("same hash %q under different shape names", a)
	}
}

func TestRowHash_TrimmedFields(t *testing.T) {
	a := rowHash("s", []string{"2024-01-15", "Coffee", "-4.50"})
	b := rowHash("s", []string{" 2024-01-15 ", "\tCoffee\t", "-4.50  "})
	if a != b {
		t.Errorf("trimming did not normalise: %q vs %q", a, b)
	}
}

func TestRowHash_DifferentFields(t *testing.T) {
	a := rowHash("s", []string{"a", "b", "c"})
	b := rowHash("s", []string{"a", "b", "d"})
	if a == b {
		t.Errorf("collision: %q", a)
	}
}

// Distinct field boundaries: "ab||cd" must not equal "a||bcd".
func TestRowHash_UnitSeparatorPreventsBoundaryCollision(t *testing.T) {
	a := rowHash("s", []string{"ab", "cd"})
	b := rowHash("s", []string{"a", "bcd"})
	if a == b {
		t.Errorf("field-boundary collision: %q", a)
	}
}
