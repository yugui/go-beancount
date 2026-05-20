package classify_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/hook"
)

func TestFactory_NameReturnsInstanceName(t *testing.T) {
	h, err := hook.New("classify", "my-instance", permissiveDecode(""))
	if err != nil {
		t.Fatalf("hook.New: %v", err)
	}
	if got := h.Name(); got != "my-instance" {
		t.Errorf("Name() = %q, want %q", got, "my-instance")
	}
}

func TestFactory_RegisteredUnderKindClassify(t *testing.T) {
	if !slices.Contains(hook.KindNames(), "classify") {
		t.Error("KindNames() does not contain \"classify\"")
	}
}

// TestFactory_NoGoPathAlias pins the deletion of the dual-name registration.
func TestFactory_NoGoPathAlias(t *testing.T) {
	const goPath = "github.com/yugui/go-beancount/pkg/importer/hook/std/classify"
	if slices.Contains(hook.KindNames(), goPath) {
		t.Errorf("KindNames() contains Go-path alias %q; should not exist", goPath)
	}
}

func TestFactory_ValidationError(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(`
[[rule]]
account = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("hook.New: expected validation error")
	}
	if h != nil {
		t.Error("hook.New: non-nil Hook on validation error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestFactory_NilDecoder(t *testing.T) {
	h, err := hook.New("classify", "test", nil)
	if err == nil {
		t.Fatal("hook.New(nil decoder): expected error")
	}
	if h != nil {
		t.Error("hook.New(nil decoder): non-nil Hook on error")
	}
	const want = "classify: configure: nil decoder"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
