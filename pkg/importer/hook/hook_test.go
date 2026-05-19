package hook

import (
	"testing"
)

func TestOptionalInterface_ConfigurableAssertion(t *testing.T) {
	// asserts Configurable is type-assertable from a Hook value obtained via
	// the registry — the path plugin authors will use.
	withCleanRegistry(t)

	h := &fakeConfigurableHook{fakeHook: fakeHook{name: "cfg"}}
	Register("cfg", h)

	got, ok := Lookup("cfg")
	if !ok {
		t.Fatal("Lookup returned ok=false")
	}
	_, ok = got.(Configurable)
	if !ok {
		t.Error("registered fakeConfigurableHook does not satisfy Configurable via type assertion")
	}
}
