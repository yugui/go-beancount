package classify_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/hook"
	_ "github.com/yugui/go-beancount/pkg/importer/hook/std/classify"
)

// TestRegister_DualRegistration verifies both the short name and Go-path alias
// resolve to the same *Hook pointer.
func TestRegister_DualRegistration(t *testing.T) {
	const shortName = "classify"
	const goPath = "github.com/yugui/go-beancount/pkg/importer/hook/std/classify"

	byShort, okShort := hook.Lookup(shortName)
	if !okShort {
		t.Fatalf("Lookup(%q) not found", shortName)
	}

	byPath, okPath := hook.Lookup(goPath)
	if !okPath {
		t.Fatalf("Lookup(%q) not found", goPath)
	}

	if byShort != byPath {
		t.Errorf("Lookup(%q) and Lookup(%q) returned different instances", shortName, goPath)
	}
}

// TestRegister_NameReturnsShort verifies Name() returns the short name, not the
// Go-path.
func TestRegister_NameReturnsShort(t *testing.T) {
	h, ok := hook.Lookup("classify")
	if !ok {
		t.Fatal("Lookup(\"classify\") not found")
	}
	if h.Name() != "classify" {
		t.Errorf("Name() = %q, want %q", h.Name(), "classify")
	}
}
