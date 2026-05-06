package pad_test

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/pad"
)

// astCmpOpts is the standard option set for comparing AST values
// returned by the plugin. apd.Decimal carries an internal big.Int
// representation with unexported fields that cmp.Diff cannot inspect
// by default; time.Time has unexported monotonic-clock state. Both
// need a custom comparer that defers to the type's own equality
// semantics. EquateEmpty smooths over the nil-vs-empty-slice
// distinction that arises naturally when building expected
// directive lists.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
	cmpopts.EquateEmpty(),
}

// amtInt constructs an ast.Amount from a small int and currency code.
func amtInt(n int64, currency string) ast.Amount {
	var d apd.Decimal
	d.SetInt64(n)
	return ast.Amount{Number: d, Currency: currency}
}

// seqOf adapts a slice of directives into an iter.Seq2[int, ast.Directive]
// compatible with api.Input.Directives without allocating a full ast.Ledger.
func seqOf(directives []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range directives {
			if !yield(i, d) {
				return
			}
		}
	}
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// wantSynthTx constructs the *ast.Transaction the plugin synthesizes
// for a pad/balance pair on `currency`. expectedAmt is the balance
// assertion's asserted total; residualAmt is the delta the synthetic
// transaction must absorb (= expected − actual at the moment of the
// assertion). The narration mirrors the plugin's exact format so
// tests can pin both behavior and the human-facing string in a single
// cmp.Diff.
func wantSynthTx(p *ast.Pad, expectedAmt, residualAmt int64, currency string) *ast.Transaction {
	pos := amtInt(residualAmt, currency)
	neg := amtInt(-residualAmt, currency)
	return &ast.Transaction{
		Span: p.Span,
		Date: p.Date,
		Flag: '*',
		Narration: fmt.Sprintf(
			"(Padding inserted for Balance of %d %s for difference %d %s)",
			expectedAmt, currency, residualAmt, currency,
		),
		Postings: []ast.Posting{
			{Account: p.Account, Amount: &pos},
			{Account: p.PadAccount, Amount: &neg},
		},
	}
}

func TestPlugin_EmptyLedger(t *testing.T) {
	res, err := pad.Apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (no change on empty ledger)", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
	}
}

// TestPlugin_NoPads feeds a ledger with transactions and a balance but
// no pad directives. The plugin must leave the ledger unchanged
// (Directives == nil) and emit no diagnostics.
func TestPlugin_NoPads(t *testing.T) {
	pos := amtInt(100, "USD")
	neg := amtInt(-100, "USD")
	txn := &ast.Transaction{
		Date: day(2024, 2, 1),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    day(2024, 3, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (no pads means no mutation)", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
	}
}

// TestPlugin_ResolvedPad feeds a pad followed by a balance on the same
// account. The plugin must synthesize a Transaction at the pad's
// position and remove the Pad directive. The synthesized transaction
// must carry the correct residual so the balance plugin's later check
// passes.
func TestPlugin_ResolvedPad(t *testing.T) {
	padSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 5, Column: 1}}
	p := &ast.Pad{
		Span:       padSpan,
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
	}
	if len(res.Directives) != 3 {
		t.Fatalf("len(Result.Directives) = %d, want 3 (pad + synth txn + balance)", len(res.Directives))
	}
	if _, ok := res.Directives[0].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[0] = %T, want *ast.Pad (original pad retained)", res.Directives[0])
	}
	tx, ok := res.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[1] = %T, want *ast.Transaction", res.Directives[1])
	}
	if !tx.Date.Equal(p.Date) {
		t.Errorf("synth.Date = %v, want %v (= pad.Date)", tx.Date, p.Date)
	}
	if tx.Span != padSpan {
		t.Errorf("synth.Span = %v, want %v (= pad.Span)", tx.Span, padSpan)
	}
	if tx.Flag != '*' {
		t.Errorf("synth.Flag = %q, want %q", tx.Flag, '*')
	}
	if len(tx.Postings) != 2 {
		t.Fatalf("len(synth.Postings) = %d, want 2", len(tx.Postings))
	}
	// First posting: target account with +residual USD.
	if tx.Postings[0].Account != "Assets:Cash" {
		t.Errorf("synth.Postings[0].Account = %q, want %q", tx.Postings[0].Account, "Assets:Cash")
	}
	if tx.Postings[0].Amount == nil {
		t.Fatalf("synth.Postings[0].Amount = nil, want explicit amount")
	}
	if tx.Postings[0].Amount.Currency != "USD" {
		t.Errorf("synth.Postings[0].Amount.Currency = %q, want USD", tx.Postings[0].Amount.Currency)
	}
	if got := tx.Postings[0].Amount.Number.String(); got != "1000" {
		t.Errorf("synth.Postings[0].Amount.Number = %q, want %q", got, "1000")
	}
	// Second posting: source account with -residual USD.
	if tx.Postings[1].Account != "Equity:Opening" {
		t.Errorf("synth.Postings[1].Account = %q, want %q", tx.Postings[1].Account, "Equity:Opening")
	}
	if tx.Postings[1].Amount == nil {
		t.Fatalf("synth.Postings[1].Amount = nil, want explicit amount")
	}
	if got := tx.Postings[1].Amount.Number.String(); got != "-1000" {
		t.Errorf("synth.Postings[1].Amount.Number = %q, want %q", got, "-1000")
	}
	// Balance directive must remain in place.
	if _, ok := res.Directives[2].(*ast.Balance); !ok {
		t.Errorf("Result.Directives[2] = %T, want *ast.Balance", res.Directives[2])
	}
}

// TestPlugin_UnresolvedPad feeds a pad with no subsequent balance.
// The plugin must emit CodePadUnresolved and leave the Pad directive
// in the output ledger.
func TestPlugin_UnresolvedPad(t *testing.T) {
	padSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 7, Column: 1}}
	p := &ast.Pad{
		Span:       padSpan,
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
	if e.Code != string(validation.CodePadUnresolved) {
		t.Errorf("Code = %q, want %q", e.Code, string(validation.CodePadUnresolved))
	}
	if e.Span != padSpan {
		t.Errorf("Span = %v, want %v", e.Span, padSpan)
	}
	wantMsg := "pad directive for Assets:Cash from Equity:Opening was not followed by a matching balance assertion"
	if e.Message != wantMsg {
		t.Errorf("Message = %q, want %q", e.Message, wantMsg)
	}
	// Unresolved pad is left in the output, so Directives must be
	// nil — there were no successful synthesizations.
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (no successful pad resolution)", res.Directives)
	}
}

// TestPlugin_ConsecutivePadsSameAccount verifies that two pads on the
// same account with no intervening balance drop the earlier one with
// the established diagnostic wording.
func TestPlugin_ConsecutivePadsSameAccount(t *testing.T) {
	firstSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 5, Column: 1}}
	pad1 := &ast.Pad{
		Span:       firstSpan,
		Date:       day(2024, 1, 10),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	pad2 := &ast.Pad{
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:OtherOpening",
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(500, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{pad1, pad2, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
	if e.Code != string(validation.CodePadUnresolved) {
		t.Errorf("Code = %q, want %q", e.Code, string(validation.CodePadUnresolved))
	}
	if e.Span != firstSpan {
		t.Errorf("Span = %v, want %v (= first pad)", e.Span, firstSpan)
	}
	wantMsg := "pad directive for Assets:Cash from Equity:Opening was not resolved before another pad"
	if e.Message != wantMsg {
		t.Errorf("Message = %q, want %q", e.Message, wantMsg)
	}
	// pad2 must have been resolved; the output should contain the
	// dropped pad1 still in place, pad2 itself, the synthesized
	// transaction immediately after pad2, and the balance directive.
	if len(res.Directives) != 4 {
		t.Fatalf("len(Result.Directives) = %d, want 4", len(res.Directives))
	}
	if _, ok := res.Directives[0].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[0] = %T, want *ast.Pad (dropped first pad remains)", res.Directives[0])
	}
	if _, ok := res.Directives[1].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[1] = %T, want *ast.Pad (resolved pad2 retained)", res.Directives[1])
	}
	tx, ok := res.Directives[2].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[2] = %T, want *ast.Transaction (synth for pad2)", res.Directives[2])
	}
	if tx.Postings[1].Account != "Equity:OtherOpening" {
		t.Errorf("synth.Postings[1].Account = %q, want %q", tx.Postings[1].Account, "Equity:OtherOpening")
	}
}

// TestPlugin_MultiPads feeds two pads for different accounts, each
// with its own subsequent balance. Each pad must be resolved
// independently.
func TestPlugin_MultiPads(t *testing.T) {
	pad1 := &ast.Pad{
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal1 := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	pad2 := &ast.Pad{
		Date:       day(2024, 2, 15),
		Account:    "Assets:Savings",
		PadAccount: "Equity:Opening",
	}
	bal2 := &ast.Balance{
		Date:    day(2024, 3, 1),
		Account: "Assets:Savings",
		Amount:  amtInt(500, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{pad1, bal1, pad2, bal2})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
	}
	if len(res.Directives) != 6 {
		t.Fatalf("len(Result.Directives) = %d, want 6 (pad1, synth1, bal1, pad2, synth2, bal2)", len(res.Directives))
	}
	if _, ok := res.Directives[0].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[0] = %T, want *ast.Pad (pad1 retained)", res.Directives[0])
	}
	tx1, ok := res.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[1] = %T, want *ast.Transaction", res.Directives[1])
	}
	if got := tx1.Postings[0].Amount.Number.String(); got != "1000" {
		t.Errorf("tx1 target amount = %q, want %q", got, "1000")
	}
	if _, ok := res.Directives[3].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[3] = %T, want *ast.Pad (pad2 retained)", res.Directives[3])
	}
	tx2, ok := res.Directives[4].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[4] = %T, want *ast.Transaction", res.Directives[4])
	}
	if got := tx2.Postings[0].Amount.Number.String(); got != "500" {
		t.Errorf("tx2 target amount = %q, want %q", got, "500")
	}
	if tx2.Postings[0].Account != "Assets:Savings" {
		t.Errorf("tx2.Postings[0].Account = %q, want %q", tx2.Postings[0].Account, "Assets:Savings")
	}
}

// TestPlugin_PadWithPriorTransactions verifies the synthesized
// transaction's amount accounts for transactions that moved the
// account's balance between the pad and its matching balance
// assertion. The balance assertion is 150 USD, an intervening +50 USD
// transaction occurs, so the pad must inject +100 USD to make the
// assertion pass.
func TestPlugin_PadWithPriorTransactions(t *testing.T) {
	p := &ast.Pad{
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	pos := amtInt(50, "USD")
	neg := amtInt(-50, "USD")
	txn := &ast.Transaction{
		Date: day(2024, 1, 20),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(150, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, txn, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("Result.Diagnostics = %v, want empty", res.Diagnostics)
	}
	if len(res.Directives) != 4 {
		t.Fatalf("len(Result.Directives) = %d, want 4 (pad, synth, txn, bal)", len(res.Directives))
	}
	if _, ok := res.Directives[0].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[0] = %T, want *ast.Pad (original pad retained)", res.Directives[0])
	}
	tx, ok := res.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[1] = %T, want *ast.Transaction", res.Directives[1])
	}
	if got := tx.Postings[0].Amount.Number.String(); got != "100" {
		t.Errorf("synth target amount = %q, want %q (150 expected - 50 intervening)", got, "100")
	}
	if got := tx.Postings[1].Amount.Number.String(); got != "-100" {
		t.Errorf("synth source amount = %q, want %q", got, "-100")
	}
}

// TestPlugin_PadZeroAdjustment verifies that when a prior transaction
// already brings the account's balance up to the asserted total, no
// synthesized padding transaction is emitted. The pad is still
// "satisfied" by the matching balance assertion (so no
// pad-unresolved diagnostic is produced), but emitting a zero-amount
// padding entry would be noise. This matches upstream beancount,
// which gates synthesis on `abs(diff) > tolerance`.
func TestPlugin_PadZeroAdjustment(t *testing.T) {
	pos := amtInt(1000, "USD")
	neg := amtInt(-1000, "USD")
	txn := &ast.Transaction{
		Date: day(2024, 1, 10),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: &neg},
		},
	}
	p := &ast.Pad{
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn, p, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	// No synthesis happened (zero residual), so the runner-convention
	// "Directives = nil ⇒ preserve input verbatim" applies and the
	// Pad remains in place untouched.
	if res.Directives != nil {
		t.Errorf("pad.Apply: Result.Directives = %v, want nil (no padding needed → input preserved verbatim)", res.Directives)
	}
}

// TestPlugin_PadNotConsumedByDifferentAccount ensures a pending pad on
// one account is NOT consumed by a balance on a different account.
func TestPlugin_PadNotConsumedByDifferentAccount(t *testing.T) {
	padSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 7, Column: 1}}
	p := &ast.Pad{
		Span:       padSpan,
		Date:       day(2024, 1, 10),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	otherBal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Equity:Opening",
		Amount:  amtInt(0, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, otherBal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Code != string(validation.CodePadUnresolved) {
		t.Errorf("Code = %q, want %q", res.Diagnostics[0].Code, string(validation.CodePadUnresolved))
	}
	if res.Diagnostics[0].Span != padSpan {
		t.Errorf("Span = %v, want %v", res.Diagnostics[0].Span, padSpan)
	}
}

// TestPlugin_CanceledContext asserts the plugin respects a canceled
// context and returns a non-nil error without running.
func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pad.Apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("pad.Apply on canceled ctx returned nil error, want non-nil")
	}
}

// TestPlugin_AutoPostingNotBookedReports pins the defensive path:
// when raw AST slips through the booking pass, the validator emits
// CodeAutoPostingUnresolved rather than silently inferring the
// missing amount. The inventory state used by the pad insertion
// calculation must NOT include the unbooked posting's amount, so
// the synthesized padding amount equals the full asserted value
// (no inferred offset).
func TestPlugin_AutoPostingNotBookedReports(t *testing.T) {
	padSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 5, Column: 1}}
	p := &ast.Pad{
		Span:       padSpan,
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	// Transaction with one explicit posting and one nil-Amount
	// posting that should have been booked. The validator must
	// report the nil-Amount posting and skip it (no inference),
	// so only the +50 USD explicit posting is folded into the
	// running balance.
	pos := amtInt(50, "USD")
	txnSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 6, Column: 1}}
	postSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 8, Column: 3}}
	txn := &ast.Transaction{
		Span: txnSpan,
		Date: day(2024, 1, 20),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Span: postSpan, Account: "Income:Salary", Amount: nil},
		},
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(150, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, txn, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}

	// Exactly one CodeAutoPostingUnresolved diagnostic for the
	// nil-Amount posting; no padding-related diagnostics because
	// the pad resolves against the partial running balance.
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
	if e.Code != string(validation.CodeAutoPostingUnresolved) {
		t.Errorf("pad.Apply Code = %q, want %q", e.Code, string(validation.CodeAutoPostingUnresolved))
	}
	if e.Span != postSpan {
		t.Errorf("pad.Apply Span = %v, want %v (= posting Span)", e.Span, postSpan)
	}
	wantMsg := "posting on account \"Income:Salary\" has no amount; booking pass should have resolved it"
	if e.Message != wantMsg {
		t.Errorf("pad.Apply Message = %q, want %q", e.Message, wantMsg)
	}

	// The synthesized transaction must reflect that the running
	// balance saw only +50 USD from the explicit posting (no
	// inference of the missing -50 USD leg). With the assertion
	// at 150 USD, the residual to inject is 100 USD.
	if len(res.Directives) != 4 {
		t.Fatalf("len(Result.Directives) = %d, want 4 (pad, synth, txn, bal)", len(res.Directives))
	}
	tx, ok := res.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[1] = %T, want *ast.Transaction", res.Directives[1])
	}
	if got := tx.Postings[0].Amount.Number.String(); got != "100" {
		t.Errorf("synth target amount = %q, want %q (150 expected - 50 explicit; missing leg is NOT inferred)", got, "100")
	}
}

// TestPlugin_AutoPostingNoExplicitFallsBackToTxnSpan verifies the Span
// fallback when a nil-Amount posting carries no Span of its own: the
// diagnostic should attach to the enclosing transaction's Span.
func TestPlugin_AutoPostingNoExplicitFallsBackToTxnSpan(t *testing.T) {
	txnSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 9, Column: 1}}
	pos := amtInt(50, "USD")
	txn := &ast.Transaction{
		Span: txnSpan,
		Date: day(2024, 1, 20),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Income:Salary", Amount: nil},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{txn})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Span != txnSpan {
		t.Errorf("pad.Apply Span = %v, want %v (txn fallback)", res.Diagnostics[0].Span, txnSpan)
	}
}

// TestPlugin_PadTargetWithCostReports pins the defensive path: pad
// refuses to invent a lot identity for a cost-bearing posting on the
// target account and reports the structural mistake as
// CodePadTargetHasCost. The auto-pad-on-cost-account fixture exercises
// the lot-identity invariant documented in pkg/inventory's
// "# Lot identity" package doc.
func TestPlugin_PadTargetWithCostReports(t *testing.T) {
	padSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 5, Column: 1}}
	p := &ast.Pad{
		Span:       padSpan,
		Date:       day(2024, 1, 15),
		Account:    "Assets:Stock",
		PadAccount: "Equity:Opening",
	}
	// Cost-held augmentation on the pad's target account between the
	// pad and the balance assertion.
	stockAmt := amtInt(5, "ACME")
	cashAmt := amtInt(-500, "USD")
	perUnit := amtInt(100, "USD")
	postSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 8, Column: 3}}
	txn := &ast.Transaction{
		Date: day(2024, 1, 20),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Span:    postSpan,
				Account: "Assets:Stock",
				Amount:  &stockAmt,
				Cost:    &ast.CostSpec{PerUnit: &perUnit},
			},
			{Account: "Equity:Opening", Amount: &cashAmt},
		},
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Stock",
		Amount:  amtInt(10, "ACME"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, txn, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}

	// At least one CodePadTargetHasCost diagnostic that names the
	// target account.
	var costDiag *ast.Diagnostic
	for i := range res.Diagnostics {
		if res.Diagnostics[i].Code == string(validation.CodePadTargetHasCost) {
			costDiag = &res.Diagnostics[i]
			break
		}
	}
	if costDiag == nil {
		t.Fatalf("Result.Diagnostics = %v, want one with Code %q", res.Diagnostics, string(validation.CodePadTargetHasCost))
	}
	if costDiag.Span != postSpan {
		t.Errorf("pad.Apply: CodePadTargetHasCost Span = %v, want %v (= cost posting span)", costDiag.Span, postSpan)
	}

	// res.Directives may be nil (no synthesis happened) — that's the
	// success case for this defensive test. If non-nil, count the
	// *ast.Transaction entries and assert no synthetic padding was
	// inserted: input has exactly one transaction (the cost-bearing
	// one), so any synthesis would push the count to 2.
	if res.Directives == nil {
		return
	}
	got := 0
	for _, d := range res.Directives {
		if _, ok := d.(*ast.Transaction); ok {
			got++
		}
	}
	if got != 1 {
		t.Errorf("pad.Apply: synthesized %d transactions, want 1 (no padding inserted)", got)
	}
}

// TestPlugin_CostInOtherCurrencyDoesNotBlockPadding mirrors the
// upstream beancount semantics for the user-reported case: a single
// pad on an account whose inventory holds a cost-bearing position in
// one currency must still be allowed to fill an unrelated currency
// on the same account. The cost-bearing currency (STOCK) is asserted
// at exactly its actual amount (zero residual, no synthesis), and
// JPY — held without cost — must be padded so the JPY balance
// assertion passes.
//
// This is the regression test for the original report:
//
//	2025-01-01 pad Assets:A Equity:Opening-Balances
//	2025-01-01 * "initial balance"
//	  Assets:A 100 STOCK { 10 JPY }
//	  Equity:Opening-Balances -1000 JPY
//	2025-01-02 balance Assets:A 100 STOCK
//	2025-01-02 balance Assets:A 3000 JPY
func TestPlugin_CostInOtherCurrencyDoesNotBlockPadding(t *testing.T) {
	padSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 4, Column: 1}}
	p := &ast.Pad{
		Span:       padSpan,
		Date:       day(2025, 1, 1),
		Account:    "Assets:A",
		PadAccount: "Equity:Opening-Balances",
	}
	stockAmt := amtInt(100, "STOCK")
	cashAmt := amtInt(-1000, "JPY")
	perUnit := amtInt(10, "JPY")
	postSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 7, Column: 3}}
	txn := &ast.Transaction{
		Date: day(2025, 1, 1),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Span:    postSpan,
				Account: "Assets:A",
				Amount:  &stockAmt,
				Cost:    &ast.CostSpec{PerUnit: &perUnit},
			},
			{Account: "Equity:Opening-Balances", Amount: &cashAmt},
		},
	}
	balStock := &ast.Balance{
		Date:    day(2025, 1, 2),
		Account: "Assets:A",
		Amount:  amtInt(100, "STOCK"),
	}
	balJPY := &ast.Balance{
		Date:    day(2025, 1, 2),
		Account: "Assets:A",
		Amount:  amtInt(3000, "JPY"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, txn, balStock, balJPY})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	// STOCK has zero residual (balance already matches) so no STOCK
	// synth is emitted. JPY needs +3000 from Equity:Opening-Balances.
	wantDirectives := []ast.Directive{p, wantSynthTx(p, 3000, 3000, "JPY"), txn, balStock, balJPY}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_OnePadCoversMultipleCurrencies verifies that a single
// pad can synthesize independent padding transactions for two
// distinct currencies on the same account, neither of which is held
// at cost. Mirrors upstream beancount's `padded_lots` semantics:
// `active_pad` persists across balance assertions until a new Pad
// directive replaces it.
func TestPlugin_OnePadCoversMultipleCurrencies(t *testing.T) {
	p := &ast.Pad{
		Date:       day(2024, 1, 15),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	balUSD := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	balEUR := &ast.Balance{
		Date:    day(2024, 2, 2),
		Account: "Assets:Cash",
		Amount:  amtInt(500, "EUR"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, balUSD, balEUR})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	wantDirectives := []ast.Directive{
		p,
		wantSynthTx(p, 1000, 1000, "USD"),
		wantSynthTx(p, 500, 500, "EUR"),
		balUSD,
		balEUR,
	}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_PadCostBlockedOnlyForAffectedCurrency verifies that the
// pad-target-has-cost diagnostic is emitted only for the currency
// that is actually held at cost, while padding succeeds for an
// unrelated currency on the same account.
func TestPlugin_PadCostBlockedOnlyForAffectedCurrency(t *testing.T) {
	p := &ast.Pad{
		Date:       day(2024, 1, 1),
		Account:    "Assets:Mixed",
		PadAccount: "Equity:Opening",
	}
	stockAmt := amtInt(5, "ACME")
	cashAmt := amtInt(-500, "USD")
	perUnit := amtInt(100, "USD")
	postSpan := ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 6, Column: 3}}
	txn := &ast.Transaction{
		Date: day(2024, 1, 5),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Span:    postSpan,
				Account: "Assets:Mixed",
				Amount:  &stockAmt,
				Cost:    &ast.CostSpec{PerUnit: &perUnit},
			},
			{Account: "Equity:Opening", Amount: &cashAmt},
		},
	}
	// Asks pad to fill 10 ACME — but ACME is held at cost on
	// Assets:Mixed, so this must error with pad-target-has-cost.
	balACME := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Mixed",
		Amount:  amtInt(10, "ACME"),
	}
	// Asks pad to fill 250 EUR — EUR has no cost-bearing positions,
	// so this must succeed.
	balEUR := &ast.Balance{
		Date:    day(2024, 2, 2),
		Account: "Assets:Mixed",
		Amount:  amtInt(250, "EUR"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, txn, balACME, balEUR})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}

	// Exactly one diagnostic: pad-target-has-cost for the ACME
	// currency, pointing at the cost-bearing posting.
	wantDiagnostics := []ast.Diagnostic{{
		Code:    string(validation.CodePadTargetHasCost),
		Span:    postSpan,
		Message: `cannot pad account "Assets:Mixed": holds cost-bearing position`,
	}}
	if diff := cmp.Diff(wantDiagnostics, res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	// EUR is unaffected by ACME's cost: padding succeeds and the
	// synth is inserted right after the pad. ACME yields no synth
	// because the cost gate refuses to invent a lot identity.
	wantDirectives := []ast.Directive{p, wantSynthTx(p, 250, 250, "EUR"), txn, balACME, balEUR}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_CostBuiltUpThenSoldOut verifies that the cost gate
// reflects the *current* inventory, not "ever held": a position that
// was bought at cost and then fully closed out before the balance
// assertion no longer blocks padding for that currency. This matches
// upstream beancount, which inspects the inventory at the moment of
// the balance check rather than the historical sequence of postings.
func TestPlugin_CostBuiltUpThenSoldOut(t *testing.T) {
	p := &ast.Pad{
		Date:       day(2024, 1, 1),
		Account:    "Assets:Trade",
		PadAccount: "Equity:Opening",
	}
	buyAmt := amtInt(5, "ACME")
	cashOut := amtInt(-500, "USD")
	perUnit := amtInt(100, "USD")
	buy := &ast.Transaction{
		Date: day(2024, 1, 5),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Trade",
				Amount:  &buyAmt,
				Cost:    &ast.CostSpec{PerUnit: &perUnit},
			},
			{Account: "Equity:Opening", Amount: &cashOut},
		},
	}
	sellAmt := amtInt(-5, "ACME")
	cashIn := amtInt(500, "USD")
	sell := &ast.Transaction{
		Date: day(2024, 1, 10),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Trade",
				Amount:  &sellAmt,
				Cost:    &ast.CostSpec{PerUnit: &perUnit},
			},
			{Account: "Equity:Opening", Amount: &cashIn},
		},
	}
	balACME := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Trade",
		Amount:  amtInt(0, "ACME"),
	}
	balUSD := &ast.Balance{
		Date:    day(2024, 2, 2),
		Account: "Assets:Trade",
		Amount:  amtInt(100, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, buy, sell, balACME, balUSD})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	// ACME asserts to zero (buy/sell net to zero) so no ACME synth is
	// emitted. USD has actual=0 too (buy -500, sell +500) so the
	// synth fills 100 USD to match the assertion.
	wantDirectives := []ast.Directive{p, wantSynthTx(p, 100, 100, "USD"), buy, sell, balACME, balUSD}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_CostOnUnrelatedAccountDoesNotBlockPad verifies that a
// cost-bearing posting on an account *other than* the pad's target
// does not trip the pad-target-has-cost gate. costBalances is keyed
// by (account, currency), so cost held on Assets:OtherStock is
// invisible to a pad on Assets:Cash.
func TestPlugin_CostOnUnrelatedAccountDoesNotBlockPad(t *testing.T) {
	p := &ast.Pad{
		Date:       day(2024, 1, 1),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	stockAmt := amtInt(5, "ACME")
	cashAmt := amtInt(-500, "USD")
	perUnit := amtInt(100, "USD")
	// Cost-bearing posting on a different account entirely.
	txn := &ast.Transaction{
		Date: day(2024, 1, 5),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:OtherStock",
				Amount:  &stockAmt,
				Cost:    &ast.CostSpec{PerUnit: &perUnit},
			},
			{Account: "Equity:Opening", Amount: &cashAmt},
		},
	}
	bal := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, txn, bal})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	wantDirectives := []ast.Directive{p, wantSynthTx(p, 1000, 1000, "USD"), txn, bal}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_PadReplacedAfterUseDoesNotEmitUnresolved verifies that
// when a pad is paired with at least one balance assertion, a
// subsequent pad on the same account replaces it without emitting
// pad-unresolved. This is the per-account analogue of upstream's
// "active_pad persists until a new Pad replaces it" semantics: the
// first pad has done its job once any balance has consulted it.
func TestPlugin_PadReplacedAfterUseDoesNotEmitUnresolved(t *testing.T) {
	firstPad := &ast.Pad{
		Date:       day(2024, 1, 1),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal1 := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	secondPad := &ast.Pad{
		Date:       day(2024, 3, 1),
		Account:    "Assets:Cash",
		PadAccount: "Equity:OtherOpening",
	}
	bal2 := &ast.Balance{
		Date:    day(2024, 4, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1500, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{firstPad, bal1, secondPad, bal2})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got, pad1 was used before pad2 replaced it):\n%s", diff)
	}
	// firstPad fills +1000 USD; secondPad sees actual = 1000 and
	// fills +500 USD to reach the 1500 USD assertion.
	wantDirectives := []ast.Directive{
		firstPad,
		wantSynthTx(firstPad, 1000, 1000, "USD"),
		bal1,
		secondPad,
		wantSynthTx(secondPad, 1500, 500, "USD"),
		bal2,
	}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_DuplicateBalanceSameCurrencyDoesNotDoubleSynth verifies
// the paddedCurrencies bookkeeping: once a currency has been padded
// (or refused) under a given pad, a second balance assertion on the
// same currency is a no-op. This prevents redundant synthesis and
// duplicate diagnostics under unusual ledgers that assert the same
// (account, currency) twice.
func TestPlugin_DuplicateBalanceSameCurrencyDoesNotDoubleSynth(t *testing.T) {
	p := &ast.Pad{
		Date:       day(2024, 1, 1),
		Account:    "Assets:Cash",
		PadAccount: "Equity:Opening",
	}
	bal1 := &ast.Balance{
		Date:    day(2024, 2, 1),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	bal2 := &ast.Balance{
		Date:    day(2024, 2, 2),
		Account: "Assets:Cash",
		Amount:  amtInt(1000, "USD"),
	}
	in := api.Input{Directives: seqOf([]ast.Directive{p, bal1, bal2})}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if diff := cmp.Diff([]ast.Diagnostic(nil), res.Diagnostics, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Diagnostics mismatch (-want +got):\n%s", diff)
	}
	// Single synth between pad and bal1; bal2 must be a no-op.
	wantDirectives := []ast.Directive{p, wantSynthTx(p, 1000, 1000, "USD"), bal1, bal2}
	if diff := cmp.Diff(wantDirectives, res.Directives, astCmpOpts); diff != "" {
		t.Errorf("pad.Apply: Result.Directives mismatch (-want +got):\n%s", diff)
	}
}

// TestPlugin_OptionsFromRawParseError confirms malformed options
// surface as ast.Diagnostic{Code: "invalid-option"}, matching the balance
// and validations plugins' contract.
func TestPlugin_OptionsFromRawParseError(t *testing.T) {
	in := api.Input{
		Options: map[string]string{
			"inferred_tolerance_multiplier": "not-a-decimal",
		},
	}
	res, err := pad.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Apply: unexpected error %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(Result.Diagnostics) = %d, want 1; diagnostics = %v", len(res.Diagnostics), res.Diagnostics)
	}
	e := res.Diagnostics[0]
	if e.Code != "invalid-option" {
		t.Errorf("Code = %q, want %q", e.Code, "invalid-option")
	}
	if !strings.Contains(e.Message, "inferred_tolerance_multiplier") {
		t.Errorf("Message = %q, want it to mention the option key", e.Message)
	}
}
