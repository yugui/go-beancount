package postproc

import (
	"context"
	"testing"

	"github.com/yugui/go-beancount/pkg/postproc/api"
)

// stubPlugin is a minimal api.Plugin for registry tests.
type stubPlugin struct{}

func (s *stubPlugin) Apply(_ context.Context, _ api.Input) (api.Result, error) {
	return api.Result{}, nil
}

func withCleanRegistry(t *testing.T) {
	t.Helper()
	old := registry
	registry = map[string]api.Plugin{}
	t.Cleanup(func() { registry = old })
}

func TestRegister_LookupRoundTrip(t *testing.T) {
	withCleanRegistry(t)
	p := &stubPlugin{}
	Register("example.com/test/roundtrip", p)

	got, ok := lookup("example.com/test/roundtrip")
	if !ok {
		t.Fatal("lookup returned false for registered plugin")
	}
	if got != p {
		t.Errorf("lookup(%q) = %v, want %v", "example.com/test/roundtrip", got, p)
	}
}

func TestRegister_LookupMissing(t *testing.T) {
	withCleanRegistry(t)

	_, ok := lookup("nonexistent")
	if ok {
		t.Error("lookup returned true for unregistered plugin")
	}
}

func TestRegister_DuplicatePanics(t *testing.T) {
	withCleanRegistry(t)
	Register("example.com/test/dup", &stubPlugin{})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register did not panic on duplicate name")
		}
	}()
	Register("example.com/test/dup", &stubPlugin{})
}
