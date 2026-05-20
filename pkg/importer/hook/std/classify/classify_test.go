package classify_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/classify"
)

// cmpOpts is read-only after package init.
var cmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

func mustDecimal(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return *d
}

func permissiveDecode(src string) func(dest any) error {
	return func(dest any) error {
		_, err := toml.NewDecoder(bytes.NewBufferString(src)).Decode(dest)
		return err
	}
}

func newHook(t *testing.T, tomlSrc string) *classify.Hook {
	t.Helper()
	h, err := hook.New("classify", "test", permissiveDecode(tomlSrc))
	if err != nil {
		t.Fatalf("hook.New: %v", err)
	}
	ch, ok := h.(*classify.Hook)
	if !ok {
		t.Fatalf("hook.New returned %T, want *classify.Hook", h)
	}
	return ch
}

// emptyHook returns a factory-built Hook with no rules.
func emptyHook(t *testing.T) *classify.Hook {
	t.Helper()
	return newHook(t, "")
}

func singleLegTxn(payee, narration, amount, currency string) *ast.Transaction {
	return &ast.Transaction{
		Date:      time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Payee:     payee,
		Narration: narration,
		Postings: []ast.Posting{
			{
				Account: ast.Account("Assets:Bank"),
				Amount:  &ast.Amount{Number: mustDecimal(amount), Currency: currency},
			},
		},
	}
}

func balancedTxn() *ast.Transaction {
	t := singleLegTxn("", "balanced", "100.00", "USD")
	t.Postings = append(t.Postings, ast.Posting{
		Account: ast.Account("Expenses:Food"),
		Amount:  &ast.Amount{Number: mustDecimal("-100.00"), Currency: "USD"},
	})
	return t
}

func applyOne(t *testing.T, h *classify.Hook, d ast.Directive) (ast.Directive, []ast.Diagnostic) {
	t.Helper()
	res, err := h.Apply(context.Background(), hook.HookInput{Directives: []ast.Directive{d}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res.Directives[0], res.Diagnostics
}

// TestApply_PayeeOnly verifies a payee-only rule matches on payee and ignores
// narration.
func TestApply_PayeeOnly(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "(?i)acme"
account     = "Expenses:Office"
`)
	tx := singleLegTxn("ACME Corp", "purchase", "50.00", "USD")
	got, diags := applyOne(t, h, tx)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("got %T, want *ast.Transaction", got)
	}
	if len(result.Postings) != 2 {
		t.Fatalf("len(Postings) = %d, want 2", len(result.Postings))
	}
	if result.Postings[1].Account != "Expenses:Office" {
		t.Errorf("counterpart Account = %q, want %q", result.Postings[1].Account, "Expenses:Office")
	}
}

// TestApply_NarrationOnly verifies a narration-only rule matches on narration.
func TestApply_NarrationOnly(t *testing.T) {
	h := newHook(t, `
[[rule]]
narration_regex = "(?i)salary"
account         = "Income:Salary"
`)
	tx := singleLegTxn("Employer", "Monthly Salary", "3000.00", "USD")
	got, diags := applyOne(t, h, tx)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("got %T, want *ast.Transaction", got)
	}
	if result.Postings[1].Account != "Income:Salary" {
		t.Errorf("counterpart Account = %q, want %q", result.Postings[1].Account, "Income:Salary")
	}
}

// TestApply_BothSelectors verifies that when both payee_regex and
// narration_regex are set, both must match.
func TestApply_BothSelectors(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex     = "^Bank$"
narration_regex = "transfer"
account         = "Assets:Savings"
`)
	t.Run("both match", func(t *testing.T) {
		tx := singleLegTxn("Bank", "transfer funds", "200.00", "USD")
		got, diags := applyOne(t, h, tx)
		if len(diags) != 0 {
			t.Errorf("unexpected diagnostics: %v", diags)
		}
		result, ok := got.(*ast.Transaction)
		if !ok {
			t.Fatalf("got %T, want *ast.Transaction", got)
		}
		if result.Postings[1].Account != "Assets:Savings" {
			t.Errorf("Postings[1].Account = %q, want Assets:Savings", result.Postings[1].Account)
		}
	})
	t.Run("payee mismatch", func(t *testing.T) {
		tx := singleLegTxn("Other", "transfer funds", "200.00", "USD")
		_, diags := applyOne(t, h, tx)
		if len(diags) != 1 || diags[0].Code != classify.DiagNoRule {
			t.Errorf("want DiagNoRule, got %v", diags)
		}
	})
	t.Run("narration mismatch", func(t *testing.T) {
		tx := singleLegTxn("Bank", "shopping", "200.00", "USD")
		_, diags := applyOne(t, h, tx)
		if len(diags) != 1 || diags[0].Code != classify.DiagNoRule {
			t.Errorf("want DiagNoRule, got %v", diags)
		}
	})
}

// TestApply_CurrencyOverride verifies the rule's currency is forwarded to
// BalanceWith when non-empty.
func TestApply_CurrencyOverride(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "."
currency    = "EUR"
account     = "Expenses:Travel"
`)
	tx := singleLegTxn("Airline", "ticket", "300.00", "USD")
	got, diags := applyOne(t, h, tx)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("got %T, want *ast.Transaction", got)
	}
	if result.Postings[1].Amount.Currency != "EUR" {
		t.Errorf("counterpart Currency = %q, want EUR", result.Postings[1].Amount.Currency)
	}
}

// TestApply_CurrencyInferred verifies empty currency uses the source posting's
// currency.
func TestApply_CurrencyInferred(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "."
account     = "Expenses:Misc"
`)
	tx := singleLegTxn("Shop", "purchase", "100.00", "JPY")
	got, diags := applyOne(t, h, tx)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("got %T, want *ast.Transaction", got)
	}
	if result.Postings[1].Amount.Currency != "JPY" {
		t.Errorf("counterpart Currency = %q, want JPY", result.Postings[1].Amount.Currency)
	}
}

// TestApply_DeclarationOrderPrecedence verifies first-match semantics: the
// first matching rule wins, not the last.
func TestApply_DeclarationOrderPrecedence(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "."
account     = "Expenses:First"

[[rule]]
payee_regex = "."
account     = "Expenses:Second"
`)
	tx := singleLegTxn("Anyone", "anything", "10.00", "USD")
	got, diags := applyOne(t, h, tx)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("got %T, want *ast.Transaction", got)
	}
	if result.Postings[1].Account != "Expenses:First" {
		t.Errorf("Postings[1].Account = %q, want Expenses:First", result.Postings[1].Account)
	}
}

// TestApply_NoRule verifies the DiagNoRule warning is emitted when no rule
// matches a single-leg transaction.
func TestApply_NoRule(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "^ACME$"
account     = "Expenses:Office"
`)
	tx := singleLegTxn("Unknown Payee", "something", "25.00", "USD")
	got, diags := applyOne(t, h, tx)

	if got != ast.Directive(tx) {
		t.Error("Apply() directive should alias input on no-rule")
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1", len(diags))
	}
	d := diags[0]
	if d.Code != classify.DiagNoRule {
		t.Errorf("Code = %q, want %q", d.Code, classify.DiagNoRule)
	}
	if d.Severity != ast.Warning {
		t.Errorf("Severity = %v, want Warning", d.Severity)
	}
	if !strings.Contains(d.Message, tx.Payee) {
		t.Errorf("Message %q missing payee %q", d.Message, tx.Payee)
	}
	if !strings.Contains(d.Message, tx.Narration) {
		t.Errorf("Message %q missing narration %q", d.Message, tx.Narration)
	}
}

// TestApply_NoRuleDiagSpan verifies DiagNoRule span is copied from the
// transaction's Span.
func TestApply_NoRuleDiagSpan(t *testing.T) {
	h := emptyHook(t)
	tx := &ast.Transaction{
		Date:      time.Now(),
		Narration: "test",
		Span:      ast.Span{Start: ast.Position{Filename: "test.bean", Line: 42}},
		Postings: []ast.Posting{
			{Account: "Assets:Bank", Amount: &ast.Amount{Number: mustDecimal("1"), Currency: "USD"}},
		},
	}
	_, diags := applyOne(t, h, tx)
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1", len(diags))
	}
	if diags[0].Span != tx.Span {
		t.Errorf("Span = %v, want %v", diags[0].Span, tx.Span)
	}
}

// TestApply_EmptyRules verifies Apply is well-defined when the hook was built
// with no rules: every single-leg txn produces DiagNoRule.
func TestApply_EmptyRules(t *testing.T) {
	h := emptyHook(t)
	tx := singleLegTxn("Payee", "narration", "100.00", "USD")
	_, diags := applyOne(t, h, tx)
	if len(diags) != 1 || diags[0].Code != classify.DiagNoRule {
		t.Errorf("want one DiagNoRule diagnostic, got %v", diags)
	}
}

// TestApply_NonTransactionPassThrough verifies non-Transaction directives
// pass through unchanged (aliased).
func TestApply_NonTransactionPassThrough(t *testing.T) {
	h := emptyHook(t)
	note := &ast.Note{Date: time.Now(), Account: "Assets:Cash", Comment: "hi"}
	res, err := h.Apply(context.Background(), hook.HookInput{Directives: []ast.Directive{note}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Directives[0] != ast.Directive(note) {
		t.Error("Apply() non-Transaction should alias input")
	}
}

// TestApply_BalancedTxnPassThrough verifies transactions with != 1 posting pass
// through unchanged.
func TestApply_BalancedTxnPassThrough(t *testing.T) {
	h := emptyHook(t)

	t.Run("two postings", func(t *testing.T) {
		tx := balancedTxn()
		got, diags := applyOne(t, h, tx)
		if len(diags) != 0 {
			t.Errorf("unexpected diagnostics: %v", diags)
		}
		if got != ast.Directive(tx) {
			t.Error("Apply() balanced txn should alias input")
		}
	})

	t.Run("zero postings", func(t *testing.T) {
		tx := &ast.Transaction{Date: time.Now(), Narration: "empty"}
		got, diags := applyOne(t, h, tx)
		if len(diags) != 0 {
			t.Errorf("unexpected diagnostics: %v", diags)
		}
		if got != ast.Directive(tx) {
			t.Error("Apply() zero-posting txn should alias input")
		}
	})
}

// TestApply_MixedDirectives verifies mixed input (non-txn + balanced + single-leg)
// is handled correctly.
func TestApply_MixedDirectives(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "."
account     = "Expenses:Misc"
`)
	note := &ast.Note{Date: time.Now(), Account: "Assets:Cash", Comment: "note"}
	balanced := balancedTxn()
	single := singleLegTxn("Shop", "item", "10.00", "USD")

	in := hook.HookInput{Directives: []ast.Directive{note, balanced, single}}
	res, err := h.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if res.Directives[0] != ast.Directive(note) {
		t.Error("Apply() note should alias input")
	}
	if res.Directives[1] != ast.Directive(balanced) {
		t.Error("Apply() balanced txn should alias input")
	}
	result, ok := res.Directives[2].(*ast.Transaction)
	if !ok {
		t.Fatalf("Directives[2]: got %T, want *ast.Transaction", res.Directives[2])
	}
	if len(result.Postings) != 2 {
		t.Errorf("single-leg txn: len(Postings) = %d, want 2", len(result.Postings))
	}
}

// TestApply_AliasOnNoSingleLeg verifies the output Directives aliases the input
// slice when no single-leg transaction is present.
func TestApply_AliasOnNoSingleLeg(t *testing.T) {
	h := emptyHook(t)
	note := &ast.Note{Date: time.Now(), Account: "Assets:Cash", Comment: "n"}
	balanced := balancedTxn()

	in := hook.HookInput{Directives: []ast.Directive{note, balanced}}
	res, err := h.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(res.Directives) != len(in.Directives) {
		t.Fatalf("Apply() len(Directives) = %d, want %d", len(res.Directives), len(in.Directives))
	}
	for i := range in.Directives {
		if res.Directives[i] != in.Directives[i] {
			t.Errorf("Directives[%d] is not the same pointer", i)
		}
	}
}

// TestApply_InputNotMutated verifies Apply does not mutate the input.
func TestApply_InputNotMutated(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "."
account     = "Expenses:Misc"
`)
	tx := singleLegTxn("Shop", "item", "10.00", "USD")
	origClone := tx.Clone()

	_, err := h.Apply(context.Background(), hook.HookInput{Directives: []ast.Directive{tx}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if diff := cmp.Diff(origClone, tx, cmpOpts); diff != "" {
		t.Errorf("Apply mutated input (-want +got):\n%s", diff)
	}
}

// TestApply_HintsNotConsulted verifies that a non-nil Hints map does not
// override the configured rule's account.
func TestApply_HintsNotConsulted(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "."
account     = "Expenses:Rule"
`)
	tx := singleLegTxn("Shop", "item", "10.00", "USD")
	in := hook.HookInput{
		Directives: []ast.Directive{tx},
		Hints:      map[string]string{"account": "Expenses:Hint"},
	}
	res, err := h.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %v", res.Diagnostics)
	}
	result, ok := res.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("Directives[0]: got %T, want *ast.Transaction", res.Directives[0])
	}
	if result.Postings[1].Account != "Expenses:Rule" {
		t.Errorf("counterpart Account = %q, want Expenses:Rule", result.Postings[1].Account)
	}
}

// TestApply_CancelledContext verifies Apply respects ctx.Err() at the top.
func TestApply_CancelledContext(t *testing.T) {
	h := emptyHook(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h.Apply(ctx, hook.HookInput{Directives: []ast.Directive{
		singleLegTxn("P", "N", "1.00", "USD"),
	}})
	if err == nil {
		t.Error("Apply with cancelled ctx should return non-nil error")
	}
}
