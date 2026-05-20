package importer

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func emptyDecode(dest any) error { return nil }

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

	t.Run("NewAlpha", func(t *testing.T) {
		imp, err := New("alpha", "test_instance", emptyDecode)
		if err != nil {
			t.Fatalf("New(\"alpha\") error: %v", err)
		}
		if imp == nil {
			t.Fatal("New(\"alpha\") returned nil Importer")
		}
		if got := imp.Name(); got != "test_instance" {
			t.Errorf("imp.Name() = %q, want %q", got, "test_instance")
		}
	})

	t.Run("NewBeta", func(t *testing.T) {
		imp, err := New("beta", "test_instance", emptyDecode)
		if err != nil {
			t.Fatalf("New(\"beta\") error: %v", err)
		}
		if imp == nil {
			t.Fatal("New(\"beta\") returned nil Importer")
		}
		if got := imp.Name(); got != "test_instance" {
			t.Errorf("imp.Name() = %q, want %q", got, "test_instance")
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

func TestNew_UnknownKindReturnsError(t *testing.T) {
	withCleanKindRegistry(t)

	imp, err := New("nonexistent", "test", emptyDecode)
	if err == nil {
		t.Error("New(\"nonexistent\") should return an error")
	}
	if imp != nil {
		t.Errorf("New(\"nonexistent\") = %v, want nil", imp)
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
