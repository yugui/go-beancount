package checkdrained

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// astCmpOpts is the standard option set for comparing AST values
// produced by the plugin. apd.Decimal carries an internal
// representation (BigInt with unexported fields) that cmp.Diff cannot
// inspect by default, and time.Time has unexported monotonic-clock
// state — both need a custom comparer that defers to the type's own
// equality semantics.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

// zeroDec returns a fresh apd.Decimal with value 0, for use in
// constructing expected Balance directives.
func zeroDec() apd.Decimal {
	var d apd.Decimal
	d.SetInt64(0)
	return d
}

func seqOf(dirs []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range dirs {
			if !yield(i, d) {
				return
			}
		}
	}
}

func amt(n int64, cur string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: cur}
}

// TestSynthesizesBalancesOnCloseMultipleCurrencies: an asset account
// with USD and EUR transactions, then a Close, yields two
// zero-balances dated one day after the Close — one per currency,
// alphabetically ordered.
func TestSynthesizesBalancesOnCloseMultipleCurrencies(t *testing.T) {
	pos1 := amt(100, "USD")
	neg1 := amt(-100, "USD")
	pos2 := amt(50, "EUR")
	neg2 := amt(-50, "EUR")
	tx1 := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Project", Amount: &pos1},
			{Account: "Income:Proj", Amount: &neg1},
		},
	}
	tx2 := &ast.Transaction{
		Date: time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Project", Amount: &pos2},
			{Account: "Income:Proj", Amount: &neg2},
		},
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Project",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx1, tx2, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Directives == nil {
		t.Fatalf("res.Directives = nil, want non-nil")
	}

	wantDate := closeDir.Date.Add(24 * time.Hour)
	// Synthesized balances must appear in alphabetical-by-currency
	// order (EUR then USD) so the test's expected slice can be a
	// fixed list.
	wantBalances := []*ast.Balance{
		{
			Date:    wantDate,
			Account: "Assets:Project",
			Amount:  ast.Amount{Number: zeroDec(), Currency: "EUR"},
			Span:    closeDir.Span,
			Meta:    closeDir.Meta,
		},
		{
			Date:    wantDate,
			Account: "Assets:Project",
			Amount:  ast.Amount{Number: zeroDec(), Currency: "USD"},
			Span:    closeDir.Span,
			Meta:    closeDir.Meta,
		},
	}
	if diff := cmp.Diff(wantBalances, filterBalances(res.Directives), astCmpOpts); diff != "" {
		t.Errorf("apply synthesized balances mismatch (-want +got):\n%s", diff)
	}
}

// TestNonBalanceSheetAccountSkipped: Close on an Income or Expenses
// account does not synthesize anything (upstream leaves P&L alone).
func TestNonBalanceSheetAccountSkipped(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &neg},
			{Account: "Expenses:Food", Amount: &pos},
		},
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Expenses:Food",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bs := filterBalances(res.Directives); len(bs) != 0 {
		t.Errorf("len(bs) = %d, want 0 (non-balance-sheet account); balances = %#v", len(bs), bs)
	}
}

// TestOpenCurrenciesCoverNoTransactions: currencies declared by the
// Open directive's constraint are covered even without any
// transactions referencing them.
func TestOpenCurrenciesCoverNoTransactions(t *testing.T) {
	op := &ast.Open{
		Date:       time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Vault",
		Currencies: []string{"GOLD", "SLVR"},
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Vault",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{op, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	balances := filterBalances(res.Directives)
	if len(balances) != 2 {
		t.Fatalf("len(balances) = %d, want 2; balances = %#v", len(balances), balances)
	}
}

// TestExistingBalanceSuppressesSynthesized: if the ledger already has
// a user-authored Balance on (account, close-date, currency), the
// plugin does not emit a duplicate. Upstream checks against the
// Close directive's date specifically (not date + 1).
func TestExistingBalanceSuppressesSynthesized(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Proj", Amount: &neg},
		},
	}
	userBal := &ast.Balance{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), // same as the Close's date
		Account: "Assets:Cash",
		Amount:  amt(0, "USD"),
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx, userBal, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The only Balance in the output is the user-authored one; no
	// synthesized Balance is added.
	bs := filterBalances(res.Directives)
	if len(bs) != 1 {
		t.Fatalf("len(balances in output) = %d, want 1 (the user's only); balances = %#v", len(bs), bs)
	}
	if !bs[0].Date.Equal(userBal.Date) {
		t.Errorf("apply output Balance.Date = %v, want %v (the user-authored balance)", bs[0].Date, userBal.Date)
	}
}

// TestAllOriginalDirectivesPreserved: every input directive survives
// in the output slice.
func TestAllOriginalDirectivesPreserved(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Proj", Amount: &neg},
		},
	}
	closeDir := &ast.Close{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx, closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sawTx, sawClose := false, false
	for _, d := range res.Directives {
		switch v := d.(type) {
		case *ast.Transaction:
			if v == tx {
				sawTx = true
			}
		case *ast.Close:
			if v == closeDir {
				sawClose = true
			}
		}
	}
	if !sawTx {
		t.Errorf("apply output is missing the input transaction directive")
	}
	if !sawClose {
		t.Errorf("apply output is missing the input close directive")
	}
}

// TestNoMutationOfInput: the plugin must not mutate input directives.
func TestNoMutationOfInput(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Proj", Amount: &neg},
		},
	}
	origPostings := len(tx.Postings)
	closeDir := &ast.Close{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	origAccount := closeDir.Account
	in := api.Input{Directives: seqOf([]ast.Directive{tx, closeDir})}

	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tx.Postings) != origPostings {
		t.Errorf("apply mutated input transaction: len(tx.Postings) %d -> %d", origPostings, len(tx.Postings))
	}
	if closeDir.Account != origAccount {
		t.Errorf("apply mutated input close directive: closeDir.Account %q -> %q", origAccount, closeDir.Account)
	}
}

// TestCanceledContext: the plugin respects a canceled context.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
}

// filterBalances returns only the *ast.Balance directives in ds,
// preserving their order. Used by tests that focus on synthesized
// balances and want to ignore the surrounding transactions and
// closes.
func filterBalances(ds []ast.Directive) []*ast.Balance {
	var out []*ast.Balance
	for _, d := range ds {
		if b, ok := d.(*ast.Balance); ok {
			out = append(out, b)
		}
	}
	return out
}
