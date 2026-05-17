package printer_test

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
)

// TestRenderCommasOption verifies that option "render_commas" threads
// through ledger.Options.Bool into WithCommaGrouping for the AST
// printer. Callers read the option directly; no helper exists in
// pkg/ast.
func TestRenderCommasOption(t *testing.T) {
	t.Run("render_commas_true_inserts_commas", func(t *testing.T) {
		src := `option "render_commas" "TRUE"

2024-01-15 * "Test"
  Expenses:Food  1000.00 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		var txn *ast.Transaction
		for _, d := range ledger.All() {
			if tx, ok := d.(*ast.Transaction); ok {
				txn = tx
				break
			}
		}
		if txn == nil {
			t.Fatal("no Transaction in ledger")
		}
		got := print(t, txn, format.WithCommaGrouping(ledger.Options.Bool("render_commas")))
		if !strings.Contains(got, "1,000.00 USD") {
			t.Errorf("want 1,000.00 USD in output, got:\n%s", got)
		}
	})

	t.Run("unset_no_commas", func(t *testing.T) {
		src := `2024-01-15 * "Test"
  Expenses:Food  1000.00 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		var txn *ast.Transaction
		for _, d := range ledger.All() {
			if tx, ok := d.(*ast.Transaction); ok {
				txn = tx
				break
			}
		}
		if txn == nil {
			t.Fatal("no Transaction in ledger")
		}
		got := print(t, txn, format.WithCommaGrouping(ledger.Options.Bool("render_commas")))
		if strings.Contains(got, "1,000") {
			t.Errorf("unset render_commas: want no comma, got:\n%s", got)
		}
	})
}
