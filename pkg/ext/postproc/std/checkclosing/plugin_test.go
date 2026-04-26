package checkclosing

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// astCmpOpts is the standard option set for comparing AST values
// produced by the plugin. apd.Decimal carries an internal
// representation (BigInt with unexported fields) that cmp.Diff cannot
// inspect by default, and time.Time has unexported monotonic-clock
// state — both need a custom comparer that defers to the type's own
// equality semantics. EquateEmpty smooths over the nil-vs-empty-map
// distinction for ast.Metadata.Props so the test does not pin the
// plugin to one particular cleanup choice.
var astCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
	cmpopts.EquateEmpty(),
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

// TestClosingBoolExpands: a posting with Meta "closing": true yields a
// cloned transaction without the closing key, plus a synthesized
// Balance on the posting's account & currency dated tx.Date + 1 day.
func TestClosingBoolExpands(t *testing.T) {
	units := amt(-1400, "QQQ180216C160")
	cash := amt(7416, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2018, 2, 16, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Options",
				Amount:  &units,
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"closing": {Kind: ast.MetaBool, Bool: true},
					"note":    {Kind: ast.MetaString, String: "keep-me"},
				}},
			},
			{Account: "Assets:Broker:Cash", Amount: &cash},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Directives) != 2 {
		t.Fatalf("len(res.Directives) = %d, want 2 (balance + cloned tx); directives = %#v", len(res.Directives), res.Directives)
	}

	bal, ok := res.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("res.Directives[0] type = %T, want *ast.Balance", res.Directives[0])
	}
	wantBal := &ast.Balance{
		Date:    tx.Date.Add(24 * time.Hour),
		Account: "Assets:Broker:Options",
		Amount:  ast.Amount{Number: zeroDec(), Currency: "QQQ180216C160"},
		Span:    tx.Span,
	}
	if diff := cmp.Diff(wantBal, bal, astCmpOpts); diff != "" {
		t.Errorf("apply synthesized Balance mismatch (-want +got):\n%s", diff)
	}

	clonedTx, ok := res.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("res.Directives[1] type = %T, want *ast.Transaction", res.Directives[1])
	}
	if clonedTx == tx {
		t.Errorf("clonedTx == tx, want a fresh clone (input must not be mutated)")
	}
	wantTx := &ast.Transaction{
		Span: tx.Span, // expected to be carried through unchanged.
		Date: tx.Date,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Options",
				Amount:  &units,
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"note": {Kind: ast.MetaString, String: "keep-me"},
				}},
			},
			{Account: "Assets:Broker:Cash", Amount: &cash},
		},
	}
	if diff := cmp.Diff(wantTx, clonedTx, astCmpOpts); diff != "" {
		t.Errorf("apply cloned transaction mismatch (-want +got):\n%s", diff)
	}
}

// TestClosingStringAccepted: a posting with Meta "closing": "TRUE"
// (string form) is recognised identically.
func TestClosingStringAccepted(t *testing.T) {
	units := amt(-1, "HOOL")
	cash := amt(1000, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Stock",
				Amount:  &units,
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"closing": {Kind: ast.MetaString, String: "TRUE"},
				}},
			},
			{Account: "Assets:Broker:Cash", Amount: &cash},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The test comment promises identical behaviour to the bool-form
	// test, so verify the exact output cardinality (not just >= 1).
	if len(res.Directives) != 2 {
		t.Fatalf("len(res.Directives) = %d, want 2 (Balance + cloned tx); directives = %#v", len(res.Directives), res.Directives)
	}
	bal, ok := res.Directives[0].(*ast.Balance)
	if !ok {
		t.Fatalf("res.Directives[0] type = %T, want *ast.Balance", res.Directives[0])
	}
	wantBal := &ast.Balance{
		Date:    tx.Date.Add(24 * time.Hour),
		Account: "Assets:Broker:Stock",
		Amount:  ast.Amount{Number: zeroDec(), Currency: "HOOL"},
		Span:    tx.Span,
	}
	if diff := cmp.Diff(wantBal, bal, astCmpOpts); diff != "" {
		t.Errorf("apply synthesized Balance mismatch (-want +got):\n%s", diff)
	}

	// Verify the cloned transaction has the closing key stripped, the
	// same contract verified by TestClosingBoolExpands. A coverage
	// gap here would let the string-form path silently leak the
	// metadata key into the output ledger.
	clonedTx, ok := res.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("res.Directives[1] type = %T, want *ast.Transaction", res.Directives[1])
	}
	if clonedTx == tx {
		t.Errorf("clonedTx == tx, want a fresh clone (input must not be mutated)")
	}
	wantTx := &ast.Transaction{
		Span: tx.Span, // expected to be carried through unchanged.
		Date: tx.Date,
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Stock",
				Amount:  &units,
				// "closing" was the sole metadata entry, so the
				// stripped result is an empty Props map. cmpopts
				// EquateEmpty (declared on astCmpOpts) treats this as
				// equivalent to a nil map, so the test does not pin
				// the plugin to one cleanup choice.
				Meta: ast.Metadata{},
			},
			{Account: "Assets:Broker:Cash", Amount: &cash},
		},
	}
	if diff := cmp.Diff(wantTx, clonedTx, astCmpOpts); diff != "" {
		t.Errorf("apply cloned transaction mismatch (-want +got):\n%s", diff)
	}
}

// TestClosingFalseIgnored: closing: false leaves the transaction
// untouched — no clone, no balance.
func TestClosingFalseIgnored(t *testing.T) {
	units := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Stock",
				Amount:  &units,
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"closing": {Kind: ast.MetaBool, Bool: false},
				}},
			},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Directives) != 1 {
		t.Fatalf("len(res.Directives) = %d, want 1 (the tx itself); directives = %#v", len(res.Directives), res.Directives)
	}
	if res.Directives[0] != ast.Directive(tx) {
		t.Errorf("res.Directives[0] = %p, want the original tx %p (no clone expected)", res.Directives[0], tx)
	}
}

// TestPostingWithoutAmountLeftAlone: a closing posting that has no
// Amount (auto-balanced) is skipped — we can't synthesize a Balance
// without a currency. The transaction is NOT cloned.
func TestPostingWithoutAmountLeftAlone(t *testing.T) {
	units := amt(-1, "HOOL")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Stock",
				// No Amount; closing: TRUE is meaningless here.
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"closing": {Kind: ast.MetaBool, Bool: true},
				}},
			},
			{Account: "Assets:Broker:Cash", Amount: &units},
		},
	}
	in := api.Input{Directives: seqOf([]ast.Directive{tx})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Directives) != 1 {
		t.Fatalf("apply: len(res.Directives) = %d, want 1 (just the tx)", len(res.Directives))
	}
	if res.Directives[0] != ast.Directive(tx) {
		t.Errorf("apply res.Directives[0] = %p, want the original tx %p (no clone expected when posting has no Amount)", res.Directives[0], tx)
	}
}

// TestInputTransactionNotMutated: the input transaction's postings and
// their metadata must remain unchanged after Plugin.Apply.
func TestInputTransactionNotMutated(t *testing.T) {
	units := amt(-1, "HOOL")
	cash := amt(1000, "USD")
	tx := &ast.Transaction{
		Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Flag: '*',
		Postings: []ast.Posting{
			{
				Account: "Assets:Broker:Stock",
				Amount:  &units,
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"closing": {Kind: ast.MetaBool, Bool: true},
				}},
			},
			{Account: "Assets:Broker:Cash", Amount: &cash},
		},
	}
	origPostingsLen := len(tx.Postings)
	origClosingVal, origHasClosing := tx.Postings[0].Meta.Props["closing"]

	in := api.Input{Directives: seqOf([]ast.Directive{tx})}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tx.Postings) != origPostingsLen {
		t.Errorf("apply mutated input: len(tx.Postings) %d -> %d", origPostingsLen, len(tx.Postings))
	}
	gotClosingVal, gotHasClosing := tx.Postings[0].Meta.Props["closing"]
	if gotHasClosing != origHasClosing || gotClosingVal != origClosingVal {
		t.Errorf("apply mutated input metadata: original (present=%v, value=%v) -> got (present=%v, value=%v)", origHasClosing, origClosingVal, gotHasClosing, gotClosingVal)
	}
}

// TestNonTransactionDirectivesPassThrough: non-transaction directives
// are kept in the output slice unchanged and in order.
func TestNonTransactionDirectivesPassThrough(t *testing.T) {
	op := &ast.Open{
		Date:    time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	com := &ast.Commodity{Currency: "USD"}
	in := api.Input{Directives: seqOf([]ast.Directive{op, com})}

	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Directives) != 2 {
		t.Fatalf("len(res.Directives) = %d, want 2 passthrough directives", len(res.Directives))
	}
	if res.Directives[0] != ast.Directive(op) || res.Directives[1] != ast.Directive(com) {
		t.Errorf("apply output directives = %#v, want [op, com] in input order", res.Directives)
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
