package classify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook"
)

// TestConcurrentApply verifies the goroutine-safety guarantee: concurrent Apply
// calls on the same [classify.Hook] must not race.
func TestConcurrentApply(t *testing.T) {
	h := newHook(t, `
[[rule]]
payee_regex = "(?i)acme"
account     = "Expenses:Office"

[[rule]]
narration_regex = "(?i)salary"
account         = "Income:Salary"
`)

	directives := []ast.Directive{
		&ast.Transaction{
			Date:      time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			Narration: "ACME purchase",
			Postings: []ast.Posting{
				{Account: "Assets:Bank", Amount: &ast.Amount{Number: mustDecimal("50.00"), Currency: "USD"}},
			},
		},
		&ast.Transaction{
			Date:      time.Date(2024, 3, 16, 0, 0, 0, 0, time.UTC),
			Narration: "Monthly Salary",
			Postings: []ast.Posting{
				{Account: "Assets:Bank", Amount: &ast.Amount{Number: mustDecimal("3000.00"), Currency: "USD"}},
			},
		},
		&ast.Note{Date: time.Date(2024, 3, 17, 0, 0, 0, 0, time.UTC), Account: "Assets:Bank", Comment: "note"},
	}
	in := hook.HookInput{Directives: directives}

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			res, err := h.Apply(context.Background(), in)
			if err != nil {
				t.Errorf("Apply: %v", err)
				return
			}
			if len(res.Directives) != len(directives) {
				t.Errorf("len(Directives) = %d, want %d", len(res.Directives), len(directives))
			}
		}()
	}
	wg.Wait()
}
