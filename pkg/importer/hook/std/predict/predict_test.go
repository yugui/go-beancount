package predict_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
)

func permissiveDecode(src string) func(dest any) error {
	return func(dest any) error {
		_, err := toml.NewDecoder(bytes.NewBufferString(src)).Decode(dest)
		return err
	}
}

func newPredictHook(t *testing.T, tomlSrc string) *predict.Hook {
	t.Helper()
	h, err := hook.New("predict", "test", permissiveDecode(tomlSrc))
	if err != nil {
		t.Fatalf("hook.New: %v", err)
	}
	ph, ok := h.(*predict.Hook)
	if !ok {
		t.Fatalf("hook.New returned %T, want *predict.Hook", h)
	}
	return ph
}

func singleLeg(payee, narration, amount string) *ast.Transaction {
	return &ast.Transaction{
		Date:      mustDate("2024-03-15"),
		Flag:      '*',
		Payee:     payee,
		Narration: narration,
		Postings: []ast.Posting{
			{Account: "Assets:Bank:Checking", Amount: amt(amount, "USD")},
		},
	}
}

func apply(t *testing.T, h *predict.Hook, ds ...ast.Directive) hook.HookResult {
	t.Helper()
	res, err := h.Apply(context.Background(), hook.HookInput{Directives: ds})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

func mustTxn(t *testing.T, d ast.Directive) *ast.Transaction {
	t.Helper()
	tx, ok := d.(*ast.Transaction)
	if !ok {
		t.Fatalf("directive is %T, want *ast.Transaction", d)
	}
	return tx
}

const ledgerConfig = `ledger = "testdata/train.beancount"`

func TestApplyFillsKnownMerchant(t *testing.T) {
	h := newPredictHook(t, ledgerConfig)
	res := apply(t, h, singleLeg("Starbucks", "Morning coffee", "-4.50"))

	tx, ok := res.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive is %T, want *ast.Transaction", res.Directives[0])
	}
	if len(tx.Postings) != 2 {
		t.Fatalf("postings = %d, want 2 (filled)", len(tx.Postings))
	}
	if tx.Postings[1].Account != "Expenses:Coffee" {
		t.Errorf("counter account = %q, want Expenses:Coffee", tx.Postings[1].Account)
	}
	// The counter is an auto-posting: its amount is left for booking to interpolate.
	if got := tx.Postings[1].Amount; got != nil {
		t.Errorf("counter amount = %v, want nil (auto-posting)", got)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("diagnostics = %v, want none on fill", res.Diagnostics)
	}
}

func TestApplyAbstainsOnUnknown(t *testing.T) {
	h := newPredictHook(t, ledgerConfig)
	res := apply(t, h, singleLeg("Xyzzy Unknown", "mysterious", "-7.00"))

	tx := mustTxn(t, res.Directives[0])
	if len(tx.Postings) != 1 {
		t.Errorf("postings = %d, want 1 (left unbalanced)", len(tx.Postings))
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != predict.DiagAbstain {
		t.Errorf("diagnostics = %v, want one %s warning", res.Diagnostics, predict.DiagAbstain)
	}
	if res.Diagnostics[0].Severity != ast.Warning {
		t.Errorf("severity = %v, want Warning", res.Diagnostics[0].Severity)
	}
}

func TestApplyNeverPredictsClosedAccount(t *testing.T) {
	h := newPredictHook(t, ledgerConfig)
	res := apply(t, h, singleLeg("Defunct Vendor", "old purchase", "-9.00"))

	tx := mustTxn(t, res.Directives[0])
	if len(tx.Postings) != 1 {
		t.Errorf("postings = %d, want 1 (closed Expenses:Defunct must not be predicted)", len(tx.Postings))
	}
}

func TestApplyPassesThroughNonFillable(t *testing.T) {
	h := newPredictHook(t, ledgerConfig)
	twoLeg := &ast.Transaction{
		Date:  mustDate("2024-03-15"),
		Flag:  '*',
		Payee: "Starbucks",
		Postings: []ast.Posting{
			{Account: "Assets:Bank:Checking", Amount: amt("-4.50", "USD")},
			{Account: "Expenses:Coffee", Amount: amt("4.50", "USD")},
		},
	}
	open := &ast.Open{Date: mustDate("2024-01-01"), Account: "Assets:New"}
	res := apply(t, h, twoLeg, open)

	if got := mustTxn(t, res.Directives[0]); len(got.Postings) != 2 {
		t.Errorf("two-leg txn postings = %d, want 2 (unchanged)", len(got.Postings))
	}
	if _, ok := res.Directives[1].(*ast.Open); !ok {
		t.Errorf("open directive changed: %T", res.Directives[1])
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("diagnostics = %v, want none", res.Diagnostics)
	}
}

func TestApplyDoesNotMutateInput(t *testing.T) {
	h := newPredictHook(t, ledgerConfig)
	in := singleLeg("Starbucks", "Morning coffee", "-4.50")
	apply(t, h, in)
	if len(in.Postings) != 1 {
		t.Errorf("input mutated: postings = %d, want 1", len(in.Postings))
	}
}

func TestApplyHighThresholdAbstains(t *testing.T) {
	// An unreachable confidence threshold forces abstention even for an exact match.
	h := newPredictHook(t, ledgerConfig+"\nmin_confidence = 0.99\nmin_margin = 0.99")
	res := apply(t, h, singleLeg("Starbucks", "Morning coffee", "-4.50"))
	tx := mustTxn(t, res.Directives[0])
	if len(tx.Postings) != 1 {
		t.Errorf("postings = %d, want 1 (high threshold abstains)", len(tx.Postings))
	}
}

func TestNewHookRequiresLedger(t *testing.T) {
	_, err := hook.New("predict", "test", permissiveDecode(""))
	if err == nil || !strings.Contains(err.Error(), "ledger is required") {
		t.Errorf("err = %v, want 'ledger is required'", err)
	}
}
