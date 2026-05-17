package format

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
)

// TestRenderCommasOption verifies that option "render_commas" threads
// through ledger.Options.Bool into WithCommaGrouping. Callers read the
// option directly; no helper exists in pkg/ast.
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
		got := Format(src, WithCommaGrouping(ledger.Options.Bool("render_commas")))
		if !strings.Contains(got, "1,000.00 USD") {
			t.Errorf("want 1,000.00 USD in output, got:\n%s", got)
		}
	})

	t.Run("render_commas_false_no_commas", func(t *testing.T) {
		src := `option "render_commas" "FALSE"

2024-01-15 * "Test"
  Expenses:Food  1000.00 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		got := Format(src, WithCommaGrouping(ledger.Options.Bool("render_commas")))
		if strings.Contains(got, "1,000") {
			t.Errorf("want no comma in output, got:\n%s", got)
		}
		if !strings.Contains(got, "1000.00 USD") {
			t.Errorf("want 1000.00 USD in output, got:\n%s", got)
		}
	})

	t.Run("unset_no_commas", func(t *testing.T) {
		// Without the option, default false → no commas.
		src := `2024-01-15 * "Test"
  Expenses:Food  1000.00 USD
  Assets:Cash
`
		ledger, err := ast.Load(src)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		got := Format(src, WithCommaGrouping(ledger.Options.Bool("render_commas")))
		if strings.Contains(got, "1,000") {
			t.Errorf("unset render_commas: want no comma, got:\n%s", got)
		}
	})
}
