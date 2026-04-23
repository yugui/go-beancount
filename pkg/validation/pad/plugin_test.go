package pad_test

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/pad"
)

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

func TestPlugin_EmptyLedger(t *testing.T) {
	res, err := pad.Plugin(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (no change on empty ledger)", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (no pads means no mutation)", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
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
// already brings the account's balance up to the asserted total, the
// synthesized transaction carries a zero residual.
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
	if len(res.Directives) != 4 {
		t.Fatalf("len(Result.Directives) = %d, want 4 (txn, pad, synth, bal)", len(res.Directives))
	}
	if _, ok := res.Directives[1].(*ast.Pad); !ok {
		t.Errorf("Result.Directives[1] = %T, want *ast.Pad (original pad retained)", res.Directives[1])
	}
	tx, ok := res.Directives[2].(*ast.Transaction)
	if !ok {
		t.Fatalf("Result.Directives[2] = %T, want *ast.Transaction", res.Directives[2])
	}
	if got := tx.Postings[0].Amount.Number.String(); got != "0" {
		t.Errorf("synth target amount = %q, want %q (prior txn already covers assertion)", got, "0")
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
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if res.Errors[0].Code != string(validation.CodePadUnresolved) {
		t.Errorf("Code = %q, want %q", res.Errors[0].Code, string(validation.CodePadUnresolved))
	}
	if res.Errors[0].Span != padSpan {
		t.Errorf("Span = %v, want %v", res.Errors[0].Span, padSpan)
	}
}

// TestPlugin_CanceledContext asserts the plugin respects a canceled
// context and returns a non-nil error without running.
func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pad.Plugin(ctx, api.Input{})
	if err == nil {
		t.Fatalf("pad.Plugin on canceled ctx returned nil error, want non-nil")
	}
}

// TestPlugin_OptionsFromRawParseError confirms malformed options
// surface as api.Error{Code: "invalid-option"}, matching the balance
// and validations plugins' contract.
func TestPlugin_OptionsFromRawParseError(t *testing.T) {
	in := api.Input{
		Options: map[string]string{
			"inferred_tolerance_multiplier": "not-a-decimal",
		},
	}
	res, err := pad.Plugin(context.Background(), in)
	if err != nil {
		t.Fatalf("pad.Plugin: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "invalid-option" {
		t.Errorf("Code = %q, want %q", e.Code, "invalid-option")
	}
	if !strings.Contains(e.Message, "inferred_tolerance_multiplier") {
		t.Errorf("Message = %q, want it to mention the option key", e.Message)
	}
}
