package classify_test

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/hook"
)

func TestFactory_NameReturnsInstanceName(t *testing.T) {
	h, err := factory(t).New("my-instance", permissiveDecode(""))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := h.Name(); got != "my-instance" {
		t.Errorf("Name() = %q, want %q", got, "my-instance")
	}
}

func TestFactory_RegisteredUnderKindClassify(t *testing.T) {
	_, ok := hook.LookupFactory("classify")
	if !ok {
		t.Error("LookupFactory(\"classify\"): not registered")
	}
}

// TestFactory_NoGoPathAlias pins the deletion of the dual-name registration.
func TestFactory_NoGoPathAlias(t *testing.T) {
	const goPath = "github.com/yugui/go-beancount/pkg/importer/hook/std/classify"
	_, ok := hook.LookupFactory(goPath)
	if ok {
		t.Errorf("LookupFactory(%q): Go-path alias should not exist", goPath)
	}
}

func TestFactory_ValidationError(t *testing.T) {
	h, err := factory(t).New("test", permissiveDecode(`
[[rule]]
account = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("New: expected validation error")
	}
	if h != nil {
		t.Error("New: non-nil Hook on validation error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestFactory_NilDecoder(t *testing.T) {
	h, err := factory(t).New("test", nil)
	if err == nil {
		t.Fatal("New(nil decoder): expected error")
	}
	if h != nil {
		t.Error("New(nil decoder): non-nil Hook on error")
	}
	const want = "classify: configure: nil decoder"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
