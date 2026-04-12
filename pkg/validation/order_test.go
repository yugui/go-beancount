package validation

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

// TestOrderDirectivesSameDay verifies that on the same day, an Open comes
// before a Balance which comes before a Transaction, even though they
// appear in a different textual order in the source ledger.
func TestOrderDirectivesSameDay(t *testing.T) {
	day := mustDate(t, "2024-01-15")

	// Source order: Transaction, Balance, Open — all on the same day.
	tx := &ast.Transaction{Date: day, Narration: "coffee"}
	bal := &ast.Balance{Date: day, Account: "Assets:Cash"}
	open := &ast.Open{Date: day, Account: "Assets:Cash"}

	ledger := &ast.Ledger{
		Directives: []ast.Directive{tx, bal, open},
	}

	got := orderDirectives(ledger)
	if len(got) != 3 {
		t.Fatalf("orderDirectives returned %d entries, want 3", len(got))
	}
	if got[0].dir != open {
		t.Errorf("got[0] = %T, want *ast.Open", got[0].dir)
	}
	if got[1].dir != bal {
		t.Errorf("got[1] = %T, want *ast.Balance", got[1].dir)
	}
	if got[2].dir != tx {
		t.Errorf("got[2] = %T, want *ast.Transaction", got[2].dir)
	}
}

// TestOrderDirectivesStable verifies that two directives with identical
// date and kind retain their relative source order.
func TestOrderDirectivesStable(t *testing.T) {
	day := mustDate(t, "2024-02-01")

	tx1 := &ast.Transaction{Date: day, Narration: "first"}
	tx2 := &ast.Transaction{Date: day, Narration: "second"}
	tx3 := &ast.Transaction{Date: day, Narration: "third"}

	ledger := &ast.Ledger{
		Directives: []ast.Directive{tx1, tx2, tx3},
	}

	got := orderDirectives(ledger)
	if len(got) != 3 {
		t.Fatalf("orderDirectives returned %d entries, want 3", len(got))
	}
	if got[0].dir != tx1 || got[0].srcIdx != 0 {
		t.Errorf("got[0] = %+v, want tx1 at srcIdx 0", got[0])
	}
	if got[1].dir != tx2 || got[1].srcIdx != 1 {
		t.Errorf("got[1] = %+v, want tx2 at srcIdx 1", got[1])
	}
	if got[2].dir != tx3 || got[2].srcIdx != 2 {
		t.Errorf("got[2] = %+v, want tx3 at srcIdx 2", got[2])
	}
}

// TestOrderDirectivesAcrossDays verifies primary ordering by date.
func TestOrderDirectivesAcrossDays(t *testing.T) {
	d1 := mustDate(t, "2024-01-01")
	d2 := mustDate(t, "2024-02-01")
	d3 := mustDate(t, "2024-03-01")

	// Supply in reverse chronological source order.
	tx3 := &ast.Transaction{Date: d3}
	tx2 := &ast.Transaction{Date: d2}
	tx1 := &ast.Transaction{Date: d1}

	ledger := &ast.Ledger{
		Directives: []ast.Directive{tx3, tx2, tx1},
	}

	got := orderDirectives(ledger)
	if got[0].dir != tx1 {
		t.Errorf("got[0] = %T, want *ast.Transaction (tx1)", got[0].dir)
	}
	if got[1].dir != tx2 {
		t.Errorf("got[1] = %T, want *ast.Transaction (tx2)", got[1].dir)
	}
	if got[2].dir != tx3 {
		t.Errorf("got[2] = %T, want *ast.Transaction (tx3)", got[2].dir)
	}
}

// TestOrderDirectivesFileHeader verifies that option/plugin/include
// directives (which have no date) sort first with zero-time stability.
func TestOrderDirectivesFileHeader(t *testing.T) {
	day := mustDate(t, "2024-01-01")

	opt := &ast.Option{Key: "title", Value: "Test"}
	plg := &ast.Plugin{Name: "beancount.plugins.auto"}
	inc := &ast.Include{Path: "other.beancount"}
	open := &ast.Open{Date: day, Account: "Assets:Cash"}

	// Intentionally scramble source order.
	ledger := &ast.Ledger{
		Directives: []ast.Directive{open, opt, plg, inc},
	}

	got := orderDirectives(ledger)
	if len(got) != 4 {
		t.Fatalf("orderDirectives returned %d entries, want 4", len(got))
	}
	// Header directives (zero time) should come before the dated Open.
	if got[0].dir != opt || got[1].dir != plg || got[2].dir != inc {
		t.Errorf("file-header directives not ordered first: got [%T, %T, %T, %T]",
			got[0].dir, got[1].dir, got[2].dir, got[3].dir)
	}
	if got[3].dir != open {
		t.Errorf("got[3] = %T, want *ast.Open", got[3].dir)
	}
}
