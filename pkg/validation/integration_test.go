package validation_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
)

// loadFixture loads a beancount fixture from the testdata directory and
// fails the test if loading produces any error-severity diagnostics.
func loadFixture(t *testing.T, name string) *ast.Ledger {
	t.Helper()
	path := filepath.Join("testdata", name)
	ledger, err := ast.Load(path)
	if err != nil {
		t.Fatalf("ast.Load(%q): %v", path, err)
	}
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			t.Fatalf("ast.Load(%q): diagnostic: %s", path, d.Message)
		}
	}
	return ledger
}

func TestIntegrationGoodLedger(t *testing.T) {
	ledger := loadFixture(t, "good_ledger.beancount")
	errs := validation.Check(ledger)
	if len(errs) != 0 {
		t.Errorf("good_ledger.beancount: got %d validation errors, want 0", len(errs))
		for _, e := range errs {
			t.Logf("  %s", e)
		}
	}
}

func TestIntegrationPadAndBalance(t *testing.T) {
	ledger := loadFixture(t, "pad_and_balance.beancount")
	errs := validation.Check(ledger)
	if len(errs) != 0 {
		t.Errorf("pad_and_balance.beancount: got %d validation errors, want 0", len(errs))
		for _, e := range errs {
			t.Logf("  %s", e)
		}
	}
}

func TestIntegrationBadLedger(t *testing.T) {
	ledger := loadFixture(t, "bad_ledger.beancount")
	errs := validation.Check(ledger)

	type got struct {
		Code     validation.Code
		Basename string
	}
	var actual []got
	for _, e := range errs {
		actual = append(actual, got{
			Code:     e.Code,
			Basename: filepath.Base(e.Span.Start.Filename),
		})
	}

	// Golden expected errors in the deterministic order produced by Check
	// (sorted by filename, byte offset, code). The fixture contains:
	//   duplicate open of Assets:Cash
	//   transaction with unopened Assets:Brokerage
	//   unbalanced transaction
	//   currency EUR not allowed on Assets:Cash
	//   currency EUR not allowed on Income:Salary (both opened as USD-only)
	//   balance mismatch on Assets:Bank
	want := []got{
		{validation.CodeDuplicateOpen, "bad_ledger.beancount"},
		{validation.CodeAccountNotOpen, "bad_ledger.beancount"},
		{validation.CodeUnbalancedTransaction, "bad_ledger.beancount"},
		{validation.CodeCurrencyNotAllowed, "bad_ledger.beancount"},
		{validation.CodeCurrencyNotAllowed, "bad_ledger.beancount"},
		{validation.CodeBalanceMismatch, "bad_ledger.beancount"},
	}

	if len(actual) != len(want) {
		t.Fatalf("bad_ledger.beancount: got %d errors, want %d\nactual: %+v\nfull:\n%s",
			len(actual), len(want), actual, formatErrors(errs))
	}
	for i, w := range want {
		a := actual[i]
		if a.Code != w.Code || a.Basename != w.Basename {
			t.Errorf("error[%d] = %+v, want %+v (message: %q)", i, a, w, errs[i].Message)
		}
	}

	// Verify determinism of ordering: non-decreasing by offset.
	for i := 1; i < len(errs); i++ {
		prev, cur := errs[i-1].Span.Start, errs[i].Span.Start
		if prev.Filename == cur.Filename && prev.Offset > cur.Offset {
			t.Errorf("errors not sorted by offset at index %d: %d > %d", i, prev.Offset, cur.Offset)
		}
	}
}

// TestIntegrationDeterministicOrder runs Check twice and verifies the
// returned errors are in identical order.
func TestIntegrationDeterministicOrder(t *testing.T) {
	ledger := loadFixture(t, "bad_ledger.beancount")
	first := validation.Check(ledger)
	second := validation.Check(ledger)
	if len(first) != len(second) {
		t.Fatalf("Check is non-deterministic: %d vs %d errors", len(first), len(second))
	}
	for i := range first {
		if first[i].Code != second[i].Code || first[i].Span.Start != second[i].Span.Start {
			t.Errorf("error[%d] differs between runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func formatErrors(errs []validation.Error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("  ")
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
