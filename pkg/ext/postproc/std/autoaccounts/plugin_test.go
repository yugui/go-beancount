package autoaccounts

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

// filterOpens returns only the *ast.Open directives in ds, preserving
// their order. Tests asserting on synthesized opens use this to ignore
// the surrounding (passthrough) directives.
func filterOpens(ds []ast.Directive) []*ast.Open {
	var out []*ast.Open
	for _, d := range ds {
		if o, ok := d.(*ast.Open); ok {
			out = append(out, o)
		}
	}
	return out
}

// TestSynthesizesOpenForTransactionAccounts: a transaction referencing
// two accounts with no Open yields two synthesized Opens, dated at the
// transaction's date and ordered alphabetically by account.
func TestSynthesizesOpenForTransactionAccounts(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	txDate := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	tx := &ast.Transaction{
		Date: txDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0; errors = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Directives == nil {
		t.Fatalf("res.Directives = nil, want non-nil")
	}

	want := []*ast.Open{
		{Date: txDate, Account: "Assets:Cash"},
		{Date: txDate, Account: "Income:Salary"},
	}
	if diff := cmp.Diff(want, filterOpens(res.Directives), astCmpOpts); diff != "" {
		t.Errorf("synthesized Opens mismatch (-want +got):\n%s", diff)
	}
}

// TestExplicitOpenSuppressesSynthesis: an account that already has an
// explicit Open directive is not re-opened by the plugin.
func TestExplicitOpenSuppressesSynthesis(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	openDir := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openDir, tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opens := filterOpens(res.Directives)
	// Two opens: the original explicit one for Assets:Cash, plus the
	// synthesized one for Income:Salary.
	if len(opens) != 2 {
		t.Fatalf("len(opens) = %d, want 2 (original + synthesized for Income:Salary); opens = %#v", len(opens), opens)
	}
	// Locate the Open for Assets:Cash and assert it is exactly the
	// original pointer — no re-open, no copy.
	var cashOpens []*ast.Open
	for _, o := range opens {
		if o.Account == "Assets:Cash" {
			cashOpens = append(cashOpens, o)
		}
	}
	if len(cashOpens) != 1 {
		t.Fatalf("got %d Opens for Assets:Cash, want 1; cashOpens = %#v", len(cashOpens), cashOpens)
	}
	if cashOpens[0] != openDir {
		t.Errorf("Assets:Cash Open is not the original pointer: got %p (%#v), want %p", cashOpens[0], cashOpens[0], openDir)
	}
}

// TestEarliestDateWins: when an account is referenced by multiple
// directives on different dates, the synthesized Open uses the
// earliest date seen.
func TestEarliestDateWins(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	earlyDate := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	lateDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	txEarly := &ast.Transaction{
		Date: earlyDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	txLate := &ast.Transaction{
		Date: lateDate,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	// Late transaction listed first so the test does not rely on
	// input order for date selection.
	in := api.Input{Directives: seqOf([]ast.Directive{txLate, txEarly})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opens := filterOpens(res.Directives)
	if len(opens) != 2 {
		t.Fatalf("len(opens) = %d, want 2; opens = %#v", len(opens), opens)
	}
	for _, o := range opens {
		if !o.Date.Equal(earlyDate) {
			t.Errorf("Open(%q).Date = %v, want %v (earliest reference)", o.Account, o.Date, earlyDate)
		}
	}
}

// TestPadAndBalanceContributeReferences: a Pad directive contributes
// both Account and PadAccount; a Balance directive contributes its
// single account. Note and Document do likewise.
func TestPadAndBalanceContributeReferences(t *testing.T) {
	pad := &ast.Pad{
		Date:       time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Account:    "Assets:Bank",
		PadAccount: "Equity:OpeningBalances",
	}
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Bank",
		Amount:  amt(100, "USD"),
	}
	note := &ast.Note{
		Date:    time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
		Account: "Liabilities:Loan",
		Comment: "interest accrual",
	}
	doc := &ast.Document{
		Date:    time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Vault",
		Path:    "/tmp/receipt.pdf",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{pad, bal, note, doc})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotAccts := map[ast.Account]time.Time{}
	for _, o := range filterOpens(res.Directives) {
		gotAccts[o.Account] = o.Date
	}
	wantAccts := map[ast.Account]time.Time{
		"Assets:Bank":            pad.Date, // earliest of (pad, bal)
		"Equity:OpeningBalances": pad.Date,
		"Liabilities:Loan":       note.Date,
		"Assets:Vault":           doc.Date,
	}
	if diff := cmp.Diff(wantAccts, gotAccts, astCmpOpts); diff != "" {
		t.Errorf("synthesized Open (account → date) mismatch (-want +got):\n%s", diff)
	}
}

// TestCloseContributesReference: a Close directive on an account that
// was never explicitly opened still contributes a synthesized Open at
// the close date. This is upstream behavior — `getters.get_accounts_use_map`
// tracks Close in the per-directive use map.
func TestCloseContributesReference(t *testing.T) {
	closeDir := &ast.Close{
		Date:    time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Old",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{closeDir})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opens := filterOpens(res.Directives)
	if len(opens) != 1 {
		t.Fatalf("len(opens) = %d, want 1; opens = %#v", len(opens), opens)
	}
	if opens[0].Account != "Assets:Old" || !opens[0].Date.Equal(closeDir.Date) {
		t.Errorf("synthesized Open = %#v, want {Account: Assets:Old, Date: %v}", opens[0], closeDir.Date)
	}
}

// TestNoOpWhenAllAccountsOpened: a ledger where every referenced
// account is explicitly opened produces no synthesized Opens. Per the
// Result contract, "no change" is signaled by nil Directives — the
// runner then keeps the input ledger untouched.
func TestNoOpWhenAllAccountsOpened(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	openCash := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	openSalary := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Income:Salary",
	}
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{openCash, openSalary, tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil (no-change signal when no synthesis is needed)", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
	}
}

// TestEmptyInput: an input with no directives returns a Result with a
// nil Directives slice (the "no change" signal). A non-nil empty slice
// would mean "clear the ledger" per the Result contract, which is the
// wrong semantics for a no-op pass.
func TestEmptyInput(t *testing.T) {
	in := api.Input{Directives: seqOf(nil)}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for empty input (no-change signal, not clear-the-ledger)", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0 for empty input", len(res.Diagnostics))
	}
}

// TestNilDirectivesIterator: an Input with a nil Directives iterator
// (no Apply target) is a no-op — Result has nil Directives and no
// errors.
func TestNilDirectivesIterator(t *testing.T) {
	res, err := apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %#v, want nil for nil-iterator input", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("len(res.Diagnostics) = %d, want 0", len(res.Diagnostics))
	}
}

// TestCustomDoesNotContributeReferences: upstream classifies Custom
// under the "no accounts" handler. Even when a Custom directive's
// values include a MetaAccount entry, the plugin must not synthesize
// an Open for it.
func TestCustomDoesNotContributeReferences(t *testing.T) {
	cust := &ast.Custom{
		Date:     time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		TypeName: "budget",
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: "Assets:Hidden"},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{cust})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(filterOpens(res.Directives)); got != 0 {
		t.Errorf("len(filterOpens) = %d, want 0 (Custom contributes no accounts); res = %#v", got, res.Directives)
	}
}

// TestOriginalDirectivesPreserved: every input directive survives in
// the output slice, in addition to the synthesized opens.
func TestOriginalDirectivesPreserved(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sawTx := false
	for _, d := range res.Directives {
		if d == ast.Directive(tx) {
			sawTx = true
		}
	}
	if !sawTx {
		t.Errorf("apply output does not contain the original transaction; directives = %#v", res.Directives)
	}
}

// TestNoMutationOfInput: input directives must not be mutated.
func TestNoMutationOfInput(t *testing.T) {
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	origPostings := len(tx.Postings)
	origAccount := tx.Postings[0].Account
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tx.Postings) != origPostings {
		t.Errorf("apply mutated input transaction: len(tx.Postings) %d -> %d", origPostings, len(tx.Postings))
	}
	if tx.Postings[0].Account != origAccount {
		t.Errorf("apply mutated input posting account: %q -> %q", origAccount, tx.Postings[0].Account)
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
