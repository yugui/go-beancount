package hook

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegister_RoundTrip(t *testing.T) {
	withCleanRegistry(t)

	a := &fakeHook{name: "alpha"}
	b := &fakeHook{name: "beta"}
	Register("alpha", a)
	Register("beta", b)

	t.Run("Lookup", func(t *testing.T) {
		cases := []struct {
			name string
			want Hook
		}{
			{"alpha", a},
			{"beta", b},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := Lookup(tc.name)
				if !ok {
					t.Fatalf("Lookup(%q) ok=false", tc.name)
				}
				if got != tc.want {
					t.Errorf("Lookup(%q) = %v, want %v", tc.name, got, tc.want)
				}
			})
		}
	})

	t.Run("Names", func(t *testing.T) {
		want := []string{"alpha", "beta"}
		if diff := cmp.Diff(want, Names()); diff != "" {
			t.Errorf("Names() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestRegister_DuplicatePanics(t *testing.T) {
	withCleanRegistry(t)
	Register("classify", &fakeHook{name: "classify"})

	defer func() {
		if recover() == nil {
			t.Fatal("Register did not panic on duplicate name")
		}
	}()
	Register("classify", &fakeHook{name: "classify-2"})
}

func TestLookup_Missing(t *testing.T) {
	withCleanRegistry(t)

	got, ok := Lookup("nonexistent")
	if ok {
		t.Errorf("Lookup(\"nonexistent\") returned ok=true with %v", got)
	}
	if got != nil {
		t.Errorf("Lookup(\"nonexistent\") = %v, want nil", got)
	}
}

func TestGlobalRegistry(t *testing.T) {
	withCleanRegistry(t)

	h := &fakeHook{name: "classify"}
	Register("classify", h)

	gr := GlobalRegistry()

	t.Run("Lookup", func(t *testing.T) {
		got, ok := gr.Lookup("classify")
		if !ok {
			t.Fatal("GlobalRegistry().Lookup(\"classify\") ok=false")
		}
		if got != h {
			t.Errorf("GlobalRegistry().Lookup = %v, want %v", got, h)
		}
	})

	t.Run("Names", func(t *testing.T) {
		want := []string{"classify"}
		if diff := cmp.Diff(want, gr.Names()); diff != "" {
			t.Errorf("GlobalRegistry().Names() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("MissingName", func(t *testing.T) {
		_, ok := gr.Lookup("nonexistent")
		if ok {
			t.Error("GlobalRegistry().Lookup(\"nonexistent\") returned ok=true")
		}
	})
}

func TestNames_SortedOrder(t *testing.T) {
	withCleanRegistry(t)

	Register("zebra", &fakeHook{name: "zebra"})
	Register("alpha", &fakeHook{name: "alpha"})
	Register("mango", &fakeHook{name: "mango"})

	want := []string{"alpha", "mango", "zebra"}
	if diff := cmp.Diff(want, Names()); diff != "" {
		t.Errorf("Names() mismatch (-want +got):\n%s", diff)
	}
}
