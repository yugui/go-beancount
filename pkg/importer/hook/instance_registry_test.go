package hook

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMapRegistry_HappyPath(t *testing.T) {
	// Use names where lex order (aaa < bbb < zzz) differs from declaration
	// order (zzz, bbb, aaa) to pin that Names() returns declaration order.
	a := &fakeHook{name: "zzz"}
	b := &fakeHook{name: "bbb"}
	c := &fakeHook{name: "aaa"}

	reg, err := NewRegistry([]Hook{a, b, c})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("NamesDeclarationOrder", func(t *testing.T) {
		want := []string{"zzz", "bbb", "aaa"}
		if diff := cmp.Diff(want, reg.Names()); diff != "" {
			t.Errorf("Names() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("NamesReturnsCopy", func(t *testing.T) {
		n1 := reg.Names()
		n1[0] = "mutated"
		n2 := reg.Names()
		if n2[0] == "mutated" {
			t.Error("Names() returned internal slice; mutation affected registry state")
		}
	})

	t.Run("LookupPresent", func(t *testing.T) {
		for _, h := range []Hook{a, b, c} {
			got, ok := reg.Lookup(h.Name())
			if !ok {
				t.Errorf("Lookup(%q) ok=false", h.Name())
				continue
			}
			if got != h {
				t.Errorf("Lookup(%q) = %v, want %v", h.Name(), got, h)
			}
		}
	})

	t.Run("LookupMissing", func(t *testing.T) {
		got, ok := reg.Lookup("nonexistent")
		if ok {
			t.Errorf("Lookup(\"nonexistent\") ok=true, got %v", got)
		}
		if got != nil {
			t.Errorf("Lookup(\"nonexistent\") = %v, want nil", got)
		}
	})
}

func TestNewRegistry_ErrorCases(t *testing.T) {
	valid := &fakeHook{name: "valid"}

	t.Run("NilHook", func(t *testing.T) {
		_, err := NewRegistry([]Hook{valid, nil})
		if err == nil {
			t.Error("NewRegistry did not return error for nil Hook")
		}
	})

	t.Run("DuplicateName", func(t *testing.T) {
		_, err := NewRegistry([]Hook{
			&fakeHook{name: "dup"},
			&fakeHook{name: "dup"},
		})
		if err == nil {
			t.Error("NewRegistry did not return error for duplicate Name()")
		}
	})

	t.Run("EmptyName", func(t *testing.T) {
		_, err := NewRegistry([]Hook{&fakeHook{name: ""}})
		if err == nil {
			t.Error("NewRegistry did not return error for empty Name()")
		}
	})
}
