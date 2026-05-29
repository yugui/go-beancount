package inspect_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
	"github.com/yugui/go-beancount/pkg/inventory/inspect"
	"github.com/yugui/go-beancount/pkg/loader"
)

// loadLedger writes src to a temp file and loads it through the full pipeline
// (booking + pad), returning the processed ledger and the file path so tests
// can build [inspect.Target] values with real spans.
func loadLedger(t *testing.T, src string) (*ast.Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	led, err := loader.LoadFile(context.Background(), path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return led, path
}

// offsetOf returns the byte offset of the first occurrence of sub in src.
func offsetOf(t *testing.T, src, sub string) int {
	t.Helper()
	i := strings.Index(src, sub)
	if i < 0 {
		t.Fatalf("substring %q not found in fixture", sub)
	}
	return i
}

// accountView returns the AccountView for acct, or fails.
func accountView(t *testing.T, v inspect.View, acct ast.Account) inspect.AccountView {
	t.Helper()
	for _, av := range v.Accounts {
		if av.Account == acct {
			return av
		}
	}
	t.Fatalf("account %q not in view (accounts=%v)", acct, accounts(v))
	return inspect.AccountView{}
}

func accounts(v inspect.View) []ast.Account {
	var out []ast.Account
	for _, av := range v.Accounts {
		out = append(out, av.Account)
	}
	return out
}

// onlyUSD returns the USD position number text of inv, or "" when empty/nil.
func onlyUSD(inv *inventory.Inventory) string {
	if inv == nil {
		return ""
	}
	ps := inv.Get("USD")
	if len(ps) == 0 {
		return ""
	}
	return ps[0].Units.Number.Text('f')
}

func TestResolve_Transaction(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	led, path := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, `"Dinner"`)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindTransaction {
		t.Fatalf("Kind = %v, want KindTransaction", v.Kind)
	}
	if got := len(v.Accounts); got != 2 {
		t.Fatalf("len(Accounts) = %d, want 2 (%v)", got, accounts(v))
	}
	// Posting order is preserved.
	if v.Accounts[0].Account != "Assets:Bank" {
		t.Errorf("Accounts[0] = %q, want Assets:Bank", v.Accounts[0].Account)
	}
	bank := accountView(t, v, "Assets:Bank")
	if bank.Before != nil {
		t.Errorf("Assets:Bank Before = %v, want nil (first touch)", bank.Before)
	}
	if got := onlyUSD(bank.After); got != "-20" {
		t.Errorf("Assets:Bank After USD = %q, want -20", got)
	}
}

func TestResolve_Balance_StrictlyBeforeDate(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Open USD
2024-01-10 * "deposit"
  Assets:Bank  100 USD
  Equity:Open  -100 USD
2024-02-01 * "later, must NOT count toward the assertion"
  Assets:Bank  5 USD
  Equity:Open  -5 USD
2024-02-01 balance Assets:Bank 100 USD
`
	led, path := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, "balance Assets:Bank")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindBalance {
		t.Fatalf("Kind = %v, want KindBalance", v.Kind)
	}
	if v.AssertedAccount != "Assets:Bank" {
		t.Errorf("AssertedAccount = %q, want Assets:Bank", v.AssertedAccount)
	}
	if v.Asserted == nil || v.Asserted.Number.Text('f') != "100" {
		t.Errorf("Asserted = %v, want 100 USD", v.Asserted)
	}
	bank := accountView(t, v, "Assets:Bank")
	// The 2024-02-01 transaction is on the balance date and must be excluded.
	if got := onlyUSD(bank.After); got != "100" {
		t.Errorf("Balance actual = %q, want 100 (same-date txn excluded)", got)
	}
}

func TestResolve_Close_InclusiveDate(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Open USD
2024-01-10 * "deposit"
  Assets:Bank  100 USD
  Equity:Open  -100 USD
2024-03-01 * "same-day movement counts for close"
  Assets:Bank  7 USD
  Equity:Open  -7 USD
2024-03-01 close Assets:Bank
`
	led, path := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, "close Assets:Bank")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindClose {
		t.Fatalf("Kind = %v, want KindClose", v.Kind)
	}
	bank := accountView(t, v, "Assets:Bank")
	if got := onlyUSD(bank.After); got != "107" {
		t.Errorf("Close final = %q, want 107 (same-date txn included)", got)
	}
}

func TestResolve_Pad(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Open USD
2024-01-05 pad Assets:Bank Equity:Open
2024-02-01 balance Assets:Bank 100 USD
`
	led, path := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, "pad Assets:Bank")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindTransaction {
		t.Fatalf("Kind = %v, want KindTransaction (pad's synthesized txn)", v.Kind)
	}
	bank := accountView(t, v, "Assets:Bank")
	if got := onlyUSD(bank.After); got != "100" {
		t.Errorf("padded Assets:Bank After = %q, want 100", got)
	}
}

func TestResolve_NoEffectDirectives(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD
2024-01-02 commodity USD
2024-01-03 price USD 1.1 JPY
2024-01-04 note Assets:Bank "memo"
2024-01-05 custom "budget" Assets:Bank "monthly"
`
	led, path := loadLedger(t, src)
	for _, sub := range []string{"open Assets:Bank", "commodity USD", "price USD", "note Assets:Bank", `custom "budget"`} {
		v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, sub)})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", sub, err)
		}
		if v.Kind != inspect.KindNone {
			t.Errorf("Resolve(%q) Kind = %v, want KindNone", sub, v.Kind)
		}
	}
}

func TestResolve_OutsideAnyDirective(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD

2024-01-15 * "x"
  Assets:Bank  1 USD
  Equity:Open  -1 USD
`
	led, path := loadLedger(t, src)
	// Offset of the blank second line (the '\n' after the open line).
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, "\n\n") + 1})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindNone {
		t.Errorf("Kind = %v, want KindNone for a cursor outside every directive", v.Kind)
	}
}

func TestResolve_WrongFilename(t *testing.T) {
	const src = `2024-01-01 open Assets:Bank USD
2024-01-15 * "x"
  Assets:Bank  1 USD
  Equity:Open  -1 USD
`
	led, _ := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: "/nowhere/other.beancount", Offset: offsetOf(t, src, `"x"`)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindNone {
		t.Errorf("Kind = %v, want KindNone for a non-matching filename", v.Kind)
	}
}

func TestResolve_Reduction_RealizedGain(t *testing.T) {
	const src = `2024-01-01 open Assets:Stock STOCK
2024-01-01 open Assets:Cash USD
2024-01-01 open Equity:Open USD
2024-01-01 open Income:Gains USD
2024-01-02 * "fund"
  Assets:Cash  1000 USD
  Equity:Open  -1000 USD
2024-01-05 * "buy"
  Assets:Stock  10 STOCK {10 USD}
  Assets:Cash  -100 USD
2024-01-10 * "sell"
  Assets:Stock  -10 STOCK {10 USD} @ 15 USD
  Assets:Cash  150 USD
  Income:Gains
`
	led, path := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, `"sell"`)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindTransaction {
		t.Fatalf("Kind = %v, want KindTransaction", v.Kind)
	}
	// The booked set carries the resolved postings, including the auto-balanced
	// Income:Gains leg the booking pass filled from the residual.
	var gains *inventory.BookedPosting
	for i := range v.Booked {
		if v.Booked[i].Account == "Income:Gains" {
			gains = &v.Booked[i]
		}
	}
	if gains == nil {
		t.Fatalf("View.Booked has no Income:Gains posting: %+v", v.Booked)
	}
	if got := gains.Units.Number.Text('f'); got != "-50" || gains.Units.Currency != "USD" {
		t.Errorf("Income:Gains booked units = %s %s, want -50 USD", got, gains.Units.Currency)
	}
	stock := accountView(t, v, "Assets:Stock")
	var gain *inventory.ReductionStep
	for _, b := range stock.Booked {
		if b.Reduction != nil && b.Reduction.RealizedGain != nil {
			gain = b.Reduction
		}
	}
	if gain == nil {
		t.Fatalf("Assets:Stock booked postings carry no realized gain: %+v", stock.Booked)
	}
	if got := gain.RealizedGain.Text('f'); got != "50" {
		t.Errorf("realized gain = %q, want 50", got)
	}
}

func TestResolve_MultiLotReductionExpanded(t *testing.T) {
	const src = `2024-01-01 open Assets:Stock STOCK "FIFO"
2024-01-01 open Equity:Open USD
2024-01-02 * "buy lot 1"
  Assets:Stock  10 STOCK {10 USD}
  Equity:Open  -100 USD
2024-01-03 * "buy lot 2"
  Assets:Stock  10 STOCK {12 USD}
  Equity:Open  -120 USD
2024-01-10 * "sell across lots"
  Assets:Stock  -15 STOCK {}
  Equity:Open
`
	led, path := loadLedger(t, src)
	v, err := inspect.Resolve(led, inspect.Target{Filename: path, Offset: offsetOf(t, src, `"sell across lots"`)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v.Kind != inspect.KindTransaction {
		t.Fatalf("Kind = %v, want KindTransaction", v.Kind)
	}
	// The single -15 STOCK reduction crosses the lot boundary (10 from the
	// first lot, 5 from the second), so it expands into two booked reductions.
	var reductions int
	for _, b := range v.Booked {
		if b.Account == "Assets:Stock" && b.Reduction != nil {
			reductions++
		}
	}
	if reductions != 2 {
		t.Errorf("Assets:Stock reductions = %d, want 2 (multi-lot expansion): %+v", reductions, v.Booked)
	}
}
