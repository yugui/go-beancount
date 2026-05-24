package fiscalincomeexpense

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

var diagCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(ast.Diagnostic{}, "Message"),
}

var testPluginDir = &ast.Plugin{Span: ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 1}}}

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

func amtStr(t *testing.T, s, cur string) ast.Amount {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("bad test setup: cannot parse %q: %v", s, err)
	}
	return ast.Amount{Number: d, Currency: cur}
}

func tx(date time.Time, postings []ast.Posting) *ast.Transaction {
	return &ast.Transaction{Date: date, Flag: '*', Postings: postings}
}

// custom builds a Custom directive of TypeName "fiscal_income_expense"
// with the supplied date and values. Tests construct values directly
// so they can exercise both two-arg and three-arg shapes.
func custom(date time.Time, values []ast.MetaValue) *ast.Custom {
	return &ast.Custom{
		Date:     date,
		TypeName: customTypeName,
		Values:   values,
	}
}

// metaAcct, metaDate, metaAmount, metaString construct typed MetaValues.
func metaAcct(s string) ast.MetaValue    { return ast.MetaValue{Kind: ast.MetaAccount, String: s} }
func metaDate(d time.Time) ast.MetaValue { return ast.MetaValue{Kind: ast.MetaDate, Date: d} }
func metaAmount(a ast.Amount) ast.MetaValue {
	return ast.MetaValue{Kind: ast.MetaAmount, Amount: a}
}
func metaString(s string) ast.MetaValue { return ast.MetaValue{Kind: ast.MetaString, String: s} }

func TestMatchingBalanceExplicitDates(t *testing.T) {
	tx1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amt(3000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-3000, "JPY"))},
	})
	tx2 := tx(time.Date(2023, 6, 20, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Restaurant", Amount: ptrAmt(amt(2000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-2000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(5000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx1, tx2, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	for _, d := range res.Directives {
		if _, isCustom := d.(*ast.Custom); isCustom {
			t.Errorf("Custom directive must be stripped from result")
		}
	}
}

func TestImplicitBeginDateJan1(t *testing.T) {
	prior := tx(time.Date(2022, 12, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amt(1000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-1000, "JPY"))},
	})
	current := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amt(3000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-3000, "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaAmount(amt(3000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prior, current, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (implicit begin should exclude prior-year tx)", res.Diagnostics)
	}
}

func TestStringFormExplicitTolerance(t *testing.T) {
	// Expected 50000 with tolerance 1; actual 49999.5 — diff 0.5 ≤ 1.
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amtStr(t, "49999.5", "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amtStr(t, "-49999.5", "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaString("50000 ~ 1 JPY"),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
}

func TestStringFormExplicitToleranceFails(t *testing.T) {
	// Expected 50000 with tolerance 1; actual 49998 — diff 2 > 1.
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amt(49998, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-49998, "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaString("50000 ~ 1 JPY"),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeMismatch {
		t.Fatalf("diagnostics = %v, want one mismatch", res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "tolerance 1") {
		t.Errorf("message %q missing tolerance 1", res.Diagnostics[0].Message)
	}
}

func TestInferredToleranceWholeNumberPasses(t *testing.T) {
	// 50000 JPY (exponent 0) → tolerance 0.5; actual 49999.6 → diff 0.4 ≤ 0.5.
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amtStr(t, "49999.6", "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amtStr(t, "-49999.6", "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaAmount(amt(50000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (diff 0.4 within tolerance 0.5)", res.Diagnostics)
	}
}

func TestInferredToleranceWholeNumberFails(t *testing.T) {
	// 50000 JPY → tol 0.5; actual 49999 → diff 1 > 0.5.
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amt(49999, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-49999, "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaAmount(amt(50000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeMismatch {
		t.Fatalf("diagnostics = %v, want one mismatch", res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "tolerance 0.5") {
		t.Errorf("message %q missing tolerance 0.5", res.Diagnostics[0].Message)
	}
}

func TestInferredToleranceFractional(t *testing.T) {
	// 50000.00 JPY (exponent -2) → tolerance 0.005.
	expected := amtStr(t, "50000.00", "JPY")
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaAmount(expected),
	})
	// Actual 50000.01 → diff 0.01 > 0.005.
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amtStr(t, "50000.01", "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amtStr(t, "-50000.01", "JPY"))},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeMismatch {
		t.Fatalf("diagnostics = %v, want one mismatch (diff > tolerance 0.005)", res.Diagnostics)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "tolerance 0.005") {
		t.Errorf("message %q missing tolerance 0.005", res.Diagnostics[0].Message)
	}
}

func TestSubAccountAggregation(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amt(1000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-1000, "JPY"))},
	})
	t2 := tx(time.Date(2023, 6, 20, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Restaurant", Amount: ptrAmt(amt(2000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-2000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(3000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, t2, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (sub-accounts must aggregate)", res.Diagnostics)
	}
}

func TestEmptyPeriodMatchesZero(t *testing.T) {
	old := tx(time.Date(2022, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amt(1000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-1000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(0, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{old, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (no postings in period; expected 0)", res.Diagnostics)
	}
}

func TestBoundaryDatesInclusive(t *testing.T) {
	t1 := tx(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amt(1000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-1000, "JPY"))},
	})
	t2 := tx(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amt(2000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-2000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(3000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, t2, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (both boundary dates inclusive)", res.Diagnostics)
	}
}

func TestMismatchBeyondTolerance(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food:Groceries", Amount: ptrAmt(amt(3000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-3000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(5000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{Code: codeMismatch, Span: testPluginDir.Span, Severity: ast.Error}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("diagnostics mismatch (-want +got):\n%s", diff)
	}
	msg := res.Diagnostics[0].Message
	if !strings.Contains(msg, "5000") || !strings.Contains(msg, "3000") {
		t.Errorf("message %q should mention expected and actual amounts", msg)
	}
}

func TestMalformedParameterCount(t *testing.T) {
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeInvalidConfig {
		t.Fatalf("diagnostics = %v, want one invalid-config", res.Diagnostics)
	}
}

func TestMalformedTooManyParams(t *testing.T) {
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(5000, "JPY")),
		metaString("extra"),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeInvalidConfig {
		t.Fatalf("diagnostics = %v, want one invalid-config", res.Diagnostics)
	}
}

func TestBeginAfterEnd(t *testing.T) {
	cust := custom(time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(5000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != codeInvalidConfig {
		t.Fatalf("diagnostics = %v, want one invalid-config (begin > end)", res.Diagnostics)
	}
}

func TestParseDiagnosticRebased(t *testing.T) {
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaString("100 + JPY"),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatalf("expected at least one parse diagnostic")
	}
	if got := res.Diagnostics[0].Code; got != codeParse {
		t.Errorf("Code = %q, want %q", got, codeParse)
	}
	if res.Diagnostics[0].Span != testPluginDir.Span {
		t.Errorf("Span = %#v, want plugin span (rebased from ast)", res.Diagnostics[0].Span)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "amount-expr-parse") {
		t.Errorf("Message %q must preserve rebased ast code", res.Diagnostics[0].Message)
	}
}

func TestNoDirectiveMutation(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amt(3000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-3000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(3000, "JPY")),
	})
	origVals := append([]ast.MetaValue(nil), cust.Values...)
	origPostings := len(t1.Postings)

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(origVals) != len(cust.Values) {
		t.Errorf("Custom.Values length mutated: %d -> %d", len(origVals), len(cust.Values))
	} else {
		for i := range origVals {
			if origVals[i].Kind != cust.Values[i].Kind || origVals[i].String != cust.Values[i].String {
				t.Errorf("Custom.Values[%d] mutated", i)
			}
		}
	}
	if len(t1.Postings) != origPostings {
		t.Errorf("Transaction.Postings mutated: %d -> %d", origPostings, len(t1.Postings))
	}
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("apply error = nil, want non-nil on canceled context")
	}
}

func TestEmptyInput(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil)}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("res.Diagnostics = %v, want none", res.Diagnostics)
	}
}

func TestIncomeAccountNegativeMatches(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 25, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Assets:Bank", Amount: ptrAmt(amt(300000, "JPY"))},
		{Account: "Income:Salary", Amount: ptrAmt(amt(-300000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Income:Salary"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(-300000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
}

func TestCurrencyFilter(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Travel", Amount: ptrAmt(amt(10000, "JPY"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-10000, "JPY"))},
	})
	t2 := tx(time.Date(2023, 6, 20, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Travel", Amount: ptrAmt(amt(100, "USD"))},
		{Account: "Assets:Bank", Amount: ptrAmt(amt(-15000, "JPY"))},
	})
	cust := custom(time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Travel"),
		metaDate(time.Date(2023, 4, 1, 0, 0, 0, 0, time.UTC)),
		metaAmount(amt(10000, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, t2, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (USD postings must not affect JPY check)", res.Diagnostics)
	}
}

func ptrAmt(a ast.Amount) *ast.Amount { return &a }

func costSpec(perUnit int64, cur string) *ast.CostSpec {
	var d apd.Decimal
	d.SetInt64(perUnit)
	return &ast.CostSpec{PerUnit: &d, Currency: cur}
}

func TestCostOnFlowAccountWarns(t *testing.T) {
	postingSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 42}}
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{
			Span:    postingSpan,
			Account: "Expenses:Investments",
			Amount:  ptrAmt(amt(100, "JPY")),
			Cost:    costSpec(1, "JPY"),
		},
		{Account: "Assets:Cash", Amount: ptrAmt(amt(-100, "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Investments"),
		metaAmount(amt(100, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{
		Code:     codeCostOnFlowAccount,
		Span:     postingSpan,
		Severity: ast.Warning,
	}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("diagnostics mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "Expenses:Investments") {
		t.Errorf("message %q should name the offending account", res.Diagnostics[0].Message)
	}
}

func TestCostOnFlowAccountOutOfWindowSilent(t *testing.T) {
	t1 := tx(time.Date(2022, 12, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{
			Account: "Expenses:Investments",
			Amount:  ptrAmt(amt(100, "JPY")),
			Cost:    costSpec(1, "JPY"),
		},
		{Account: "Assets:Cash", Amount: ptrAmt(amt(-100, "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Investments"),
		metaAmount(amt(0, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (cost-bearing posting outside window)", res.Diagnostics)
	}
}

func TestCostOnNonTargetAccountSilent(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{Account: "Expenses:Food", Amount: ptrAmt(amt(100, "JPY"))},
		{
			Account: "Assets:Brokerage",
			Amount:  ptrAmt(amt(-100, "JPY")),
			Cost:    costSpec(1, "JPY"),
		},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Food"),
		metaAmount(amt(100, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none (cost on non-target account)", res.Diagnostics)
	}
}

func TestCostOnFlowAccountSumUnchanged(t *testing.T) {
	t1 := tx(time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC), []ast.Posting{
		{
			Account: "Expenses:Investments",
			Amount:  ptrAmt(amt(100, "JPY")),
			Cost:    costSpec(1, "JPY"),
		},
		{Account: "Assets:Cash", Amount: ptrAmt(amt(-100, "JPY"))},
	})
	cust := custom(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), []ast.MetaValue{
		metaAcct("Expenses:Investments"),
		metaAmount(amt(50, "JPY")),
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{t1, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	codes := make(map[string]int)
	for _, d := range res.Diagnostics {
		codes[d.Code]++
	}
	if codes[codeCostOnFlowAccount] != 1 {
		t.Errorf("want 1 cost-on-flow-account warning, got %d (diagnostics=%v)", codes[codeCostOnFlowAccount], res.Diagnostics)
	}
	if codes[codeMismatch] != 1 {
		t.Errorf("want 1 mismatch error (sum is raw 100 vs expected 50), got %d (diagnostics=%v)", codes[codeMismatch], res.Diagnostics)
	}
}
