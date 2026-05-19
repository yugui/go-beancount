package importer

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFactory_ParallelCallsProduceIndependentImporters(t *testing.T) {
	const n = 20

	f := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		return &fakeImporter{name: name}, nil
	})

	imps := make([]Importer, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			imp, err := f.New(fmt.Sprintf("instance-%02d", i), func(dest any) error { return nil })
			if err != nil {
				t.Errorf("FactoryFunc.New: instance-%02d: %v", i, err)
				return
			}
			imps[i] = imp
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, imp := range imps {
		if imp == nil {
			t.Errorf("FactoryFunc.New: imps[%d] is nil", i)
			continue
		}
		name := imp.Name()
		if seen[name] {
			t.Errorf("FactoryFunc.New: duplicate instance name %q", name)
		}
		seen[name] = true
		want := fmt.Sprintf("instance-%02d", i)
		if name != want {
			t.Errorf("FactoryFunc.New: imps[%d].Name() = %q, want %q", i, name, want)
		}
	}
}

func TestFactory_DecodeErrorPropagates(t *testing.T) {
	decodeErr := errors.New("decode error")

	f := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		var cfg struct{ Val string }
		if err := decode(&cfg); err != nil {
			return nil, err
		}
		return &fakeImporter{name: name}, nil
	})

	imp, err := f.New("x", func(dest any) error { return decodeErr })
	if !errors.Is(err, decodeErr) {
		t.Errorf("factory.New error = %v, want %v", err, decodeErr)
	}
	if imp != nil {
		t.Errorf("factory.New returned non-nil Importer on decode error")
	}
}

func TestFactory_ValidationErrorPropagates(t *testing.T) {
	validationErr := errors.New("validation error")

	f := FactoryFunc(func(name string, decode func(dest any) error) (Importer, error) {
		if name == "" {
			return nil, validationErr
		}
		return &fakeImporter{name: name}, nil
	})

	imp, err := f.New("", func(dest any) error { return nil })
	if !errors.Is(err, validationErr) {
		t.Errorf("factory.New error = %v, want %v", err, validationErr)
	}
	if imp != nil {
		t.Errorf("factory.New returned non-nil Importer on validation error")
	}
}

func TestMapRegistry_HappyPath(t *testing.T) {
	// Use names where lex order (aaa < bbb < zzz) differs from declaration
	// order (zzz, bbb, aaa) so that a regression that re-sorts Names() fails.
	a := &fakeImporter{name: "zzz"}
	b := &fakeImporter{name: "bbb"}
	c := &fakeImporter{name: "aaa"}

	reg, err := NewRegistry([]Importer{a, b, c})
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
		for _, imp := range []Importer{a, b, c} {
			got, ok := reg.Lookup(imp.Name())
			if !ok {
				t.Errorf("Lookup(%q) ok=false", imp.Name())
				continue
			}
			if got != imp {
				t.Errorf("Lookup(%q) = %v, want %v", imp.Name(), got, imp)
			}
		}
	})

	t.Run("LookupMissing", func(t *testing.T) {
		got, ok := reg.Lookup("nonexistent")
		if ok {
			t.Errorf("Lookup(\"nonexistent\") ok=true, got %v", got)
		}
	})
}

func TestNewRegistry_ErrorCases(t *testing.T) {
	valid := &fakeImporter{name: "valid"}

	t.Run("NilImporter", func(t *testing.T) {
		_, err := NewRegistry([]Importer{valid, nil})
		if err == nil {
			t.Error("NewRegistry did not return error for nil Importer")
		}
	})

	t.Run("DuplicateName", func(t *testing.T) {
		_, err := NewRegistry([]Importer{
			&fakeImporter{name: "dup"},
			&fakeImporter{name: "dup"},
		})
		if err == nil {
			t.Error("NewRegistry did not return error for duplicate Name()")
		}
	})

	t.Run("EmptyName", func(t *testing.T) {
		_, err := NewRegistry([]Importer{&fakeImporter{name: ""}})
		if err == nil {
			t.Error("NewRegistry did not return error for empty Name()")
		}
	})
}
