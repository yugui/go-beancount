package importer

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegisterFactory_RoundTrip(t *testing.T) {
	withCleanKindRegistry(t)

	fa := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		return &fakeImporter{name: name}, nil
	})
	fb := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		return &fakeImporter{name: name}, nil
	})
	RegisterFactory("alpha", fa)
	RegisterFactory("beta", fb)

	t.Run("LookupAlpha", func(t *testing.T) {
		got, ok := LookupFactory("alpha")
		if !ok {
			t.Fatal("LookupFactory(\"alpha\") ok=false")
		}
		if got == nil {
			t.Error("LookupFactory returned nil factory")
		}
	})

	t.Run("LookupBeta", func(t *testing.T) {
		got, ok := LookupFactory("beta")
		if !ok {
			t.Fatal("LookupFactory(\"beta\") ok=false")
		}
		if got == nil {
			t.Error("LookupFactory returned nil factory")
		}
	})

	t.Run("KindNames", func(t *testing.T) {
		want := []string{"alpha", "beta"}
		if diff := cmp.Diff(want, KindNames()); diff != "" {
			t.Errorf("KindNames() mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestRegisterFactory_DuplicatePanics(t *testing.T) {
	withCleanKindRegistry(t)

	f := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		return &fakeImporter{name: name}, nil
	})
	RegisterFactory("csv", f)

	defer func() {
		if recover() == nil {
			t.Fatal("RegisterFactory did not panic on duplicate kind")
		}
	}()
	RegisterFactory("csv", f)
}

func TestLookupFactory_Missing(t *testing.T) {
	withCleanKindRegistry(t)

	got, ok := LookupFactory("nonexistent")
	if ok {
		t.Errorf("LookupFactory(\"nonexistent\") returned ok=true with %v", got)
	}
	if got != nil {
		t.Errorf("LookupFactory(\"nonexistent\") = %v, want nil", got)
	}
}

func TestKindNames_SortedOrder(t *testing.T) {
	withCleanKindRegistry(t)

	f := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		return &fakeImporter{name: name}, nil
	})
	RegisterFactory("zebra", f)
	RegisterFactory("alpha", f)
	RegisterFactory("mango", f)

	want := []string{"alpha", "mango", "zebra"}
	if diff := cmp.Diff(want, KindNames()); diff != "" {
		t.Errorf("KindNames() mismatch (-want +got):\n%s", diff)
	}
}
