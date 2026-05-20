package classify_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/hook"
)

func TestFactory_EmptyRules(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(``))
	if err != nil {
		t.Errorf("New(empty): %v", err)
	}
	if h == nil {
		t.Error("New(empty): nil Hook")
	}
}

func TestFactory_ValidRule(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(`
[[rule]]
payee_regex = "ACME"
account     = "Expenses:Office"
`))
	if err != nil {
		t.Errorf("New(valid): %v", err)
	}
	if h == nil {
		t.Error("New(valid): nil Hook")
	}
}

func TestFactory_SelectorlessRuleRejected(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(`
[[rule]]
account = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("New(selector-less rule) should return error")
	}
	if h != nil {
		t.Error("New(selector-less rule): non-nil Hook on error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestFactory_MissingAccountRejected(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(`
[[rule]]
payee_regex = "ACME"
`))
	if err == nil {
		t.Fatal("New(missing account) should return error")
	}
	if h != nil {
		t.Error("New(missing account): non-nil Hook on error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestFactory_BadPayeeRegexRejected(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(`
[[rule]]
payee_regex = "["
account     = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("New(bad payee_regex) should return error")
	}
	if h != nil {
		t.Error("New(bad payee_regex): non-nil Hook on error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestFactory_BadNarrationRegexRejected(t *testing.T) {
	h, err := hook.New("classify", "test", permissiveDecode(`
[[rule]]
narration_regex = "["
account         = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("New(bad narration_regex) should return error")
	}
	if h != nil {
		t.Error("New(bad narration_regex): non-nil Hook on error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestFactory_DecoderErrorWrapped(t *testing.T) {
	sentinel := errors.New("decode failure")
	h, err := hook.New("classify", "test", func(dest any) error { return sentinel })
	if err == nil {
		t.Fatal("expected error")
	}
	if h != nil {
		t.Error("New: non-nil Hook on decode error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error should wrap sentinel; got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}
