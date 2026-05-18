package importer

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegister_RoundTrip(t *testing.T) {
	withCleanRegistry(t)

	a := &fakeImporter{name: "alpha"}
	b := &fakeImporter{name: "beta"}
	Register("alpha", a)
	Register("beta", b)

	t.Run("Lookup", func(t *testing.T) {
		cases := []struct {
			name string
			want Importer
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
	Register("csv", &fakeImporter{name: "csv"})

	defer func() {
		if recover() == nil {
			t.Fatal("Register did not panic on duplicate name")
		}
	}()
	Register("csv", &fakeImporter{name: "csv-2"})
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

	imp := &fakeImporter{name: "csv"}
	Register("csv", imp)

	gr := GlobalRegistry()

	t.Run("Lookup", func(t *testing.T) {
		got, ok := gr.Lookup("csv")
		if !ok {
			t.Fatal("GlobalRegistry().Lookup(\"csv\") ok=false")
		}
		direct, _ := Lookup("csv")
		if got != direct {
			t.Errorf("GlobalRegistry().Lookup = %v, package Lookup = %v", got, direct)
		}
	})

	t.Run("Names", func(t *testing.T) {
		want := []string{"csv"}
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

	Register("zebra", &fakeImporter{name: "zebra"})
	Register("alpha", &fakeImporter{name: "alpha"})
	Register("mango", &fakeImporter{name: "mango"})

	want := []string{"alpha", "mango", "zebra"}
	if diff := cmp.Diff(want, Names()); diff != "" {
		t.Errorf("Names() mismatch (-want +got):\n%s", diff)
	}
}
