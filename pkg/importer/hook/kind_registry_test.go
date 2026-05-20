package hook

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func emptyDecode(dest any) error { return nil }

func TestRegisterFactory_RoundTrip(t *testing.T) {
	withCleanKindRegistry(t)

	fa := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{name: name}, nil
	})
	fb := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{name: name}, nil
	})
	RegisterFactory("alpha", fa)
	RegisterFactory("beta", fb)

	t.Run("NewAlpha", func(t *testing.T) {
		h, err := New("alpha", "test_instance", emptyDecode)
		if err != nil {
			t.Fatalf("New(\"alpha\") error: %v", err)
		}
		if h == nil {
			t.Fatal("New(\"alpha\") returned nil Hook")
		}
		if got := h.Name(); got != "test_instance" {
			t.Errorf("h.Name() = %q, want %q", got, "test_instance")
		}
	})

	t.Run("NewBeta", func(t *testing.T) {
		h, err := New("beta", "test_instance", emptyDecode)
		if err != nil {
			t.Fatalf("New(\"beta\") error: %v", err)
		}
		if h == nil {
			t.Fatal("New(\"beta\") returned nil Hook")
		}
		if got := h.Name(); got != "test_instance" {
			t.Errorf("h.Name() = %q, want %q", got, "test_instance")
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

	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{name: name}, nil
	})
	RegisterFactory("classify", f)

	defer func() {
		if recover() == nil {
			t.Fatal("RegisterFactory did not panic on duplicate kind")
		}
	}()
	RegisterFactory("classify", f)
}

func TestNew_UnknownKindReturnsError(t *testing.T) {
	withCleanKindRegistry(t)

	h, err := New("nonexistent", "test", emptyDecode)
	if err == nil {
		t.Error("New(\"nonexistent\") should return an error")
	}
	if h != nil {
		t.Errorf("New(\"nonexistent\") = %v, want nil", h)
	}
}

func TestKindNames_SortedOrder(t *testing.T) {
	withCleanKindRegistry(t)

	f := FactoryFunc(func(name string, decode func(dest any) error) (Hook, error) {
		return &fakeHook{name: name}, nil
	})
	RegisterFactory("zebra", f)
	RegisterFactory("alpha", f)
	RegisterFactory("mango", f)

	want := []string{"alpha", "mango", "zebra"}
	if diff := cmp.Diff(want, KindNames()); diff != "" {
		t.Errorf("KindNames() mismatch (-want +got):\n%s", diff)
	}
}
