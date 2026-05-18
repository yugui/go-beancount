package classify_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/classify"
)

func TestConfigure_EmptyRules(t *testing.T) {
	h := &classify.Hook{}
	if err := h.Configure(permissiveDecode(``)); err != nil {
		t.Errorf("Configure(empty): %v", err)
	}
}

func TestConfigure_ValidRule(t *testing.T) {
	h := &classify.Hook{}
	err := h.Configure(permissiveDecode(`
[[rule]]
payee_regex = "ACME"
account     = "Expenses:Office"
`))
	if err != nil {
		t.Errorf("Configure(valid): %v", err)
	}
}

func TestConfigure_NilDecoder(t *testing.T) {
	h := &classify.Hook{}
	err := h.Configure(nil)
	if err == nil {
		t.Fatal("Configure(nil decoder) should return error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("Configure() error prefix wrong: %q", err.Error())
	}
}

func TestConfigure_SelectorlessRuleRejected(t *testing.T) {
	h := &classify.Hook{}
	err := h.Configure(permissiveDecode(`
[[rule]]
account = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("Configure(selector-less rule) should return error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestConfigure_MissingAccountRejected(t *testing.T) {
	h := &classify.Hook{}
	err := h.Configure(permissiveDecode(`
[[rule]]
payee_regex = "ACME"
`))
	if err == nil {
		t.Fatal("Configure(missing account) should return error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestConfigure_BadPayeeRegexRejected(t *testing.T) {
	h := &classify.Hook{}
	err := h.Configure(permissiveDecode(`
[[rule]]
payee_regex = "["
account     = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("Configure(bad payee_regex) should return error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestConfigure_BadNarrationRegexRejected(t *testing.T) {
	h := &classify.Hook{}
	err := h.Configure(permissiveDecode(`
[[rule]]
narration_regex = "["
account         = "Expenses:Misc"
`))
	if err == nil {
		t.Fatal("Configure(bad narration_regex) should return error")
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

func TestConfigure_DecoderErrorWrapped(t *testing.T) {
	sentinel := errors.New("decode failure")
	h := &classify.Hook{}
	err := h.Configure(func(dest any) error { return sentinel })
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error should wrap sentinel; got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "classify: configure:") {
		t.Errorf("error prefix wrong: %q", err.Error())
	}
}

// TestConfigure_PriorRulesUntouched verifies that a failed re-Configure leaves
// the previously installed rules intact.
func TestConfigure_PriorRulesUntouched(t *testing.T) {
	h := &classify.Hook{}
	if err := h.Configure(permissiveDecode(`
[[rule]]
payee_regex = "."
account     = "Expenses:First"
`)); err != nil {
		t.Fatalf("first Configure: %v", err)
	}

	// Second Configure fails: selector-less rule.
	if err := h.Configure(permissiveDecode(`
[[rule]]
account = "Expenses:Bad"
`)); err == nil {
		t.Fatal("second Configure should have failed")
	}

	// The hook must still use the original rules: single-leg txn should be
	// classified, not produce DiagNoRule.
	tx := singleLegTxn("Anyone", "anything", "1.00", "USD")
	res, err := h.Apply(context.Background(), hook.HookInput{Directives: []ast.Directive{tx}})
	if err != nil {
		t.Fatalf("Apply after failed re-Configure: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Apply() DiagNoRule emitted after failed re-Configure; original rules should still apply")
	}
	result, ok := res.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("Directives[0]: got %T, want *ast.Transaction", res.Directives[0])
	}
	if len(result.Postings) != 2 {
		t.Errorf("len(Postings) = %d; want 2 (original rule should have fired)", len(result.Postings))
	}
}
