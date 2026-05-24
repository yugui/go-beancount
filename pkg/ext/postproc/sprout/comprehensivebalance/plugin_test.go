package comprehensivebalance

import (
	"context"
	"iter"
	"sort"
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

func decimalLit(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("bad test setup: cannot parse %q: %v", s, err)
	}
	return d
}

func makeTx(date time.Time, postings []ast.Posting) *ast.Transaction {
	return &ast.Transaction{Date: date, Flag: '*', Postings: postings}
}

// custom builds an *ast.Custom of TypeName "comprehensive_balance" with
// the given account and body string.
func custom(date time.Time, account string, body string) *ast.Custom {
	return &ast.Custom{
		Date:     date,
		TypeName: customTypeName,
		Values: []ast.MetaValue{
			{Kind: ast.MetaAccount, String: account},
			{Kind: ast.MetaString, String: body},
		},
	}
}

// balancesByCurrency collects every *ast.Balance from res keyed by
// (account, currency) so test assertions can probe each independently
// of emission order.
func balancesByCurrency(t *testing.T, res api.Result) map[string]*ast.Balance {
	t.Helper()
	out := map[string]*ast.Balance{}
	for _, d := range res.Directives {
		if b, ok := d.(*ast.Balance); ok {
			out[string(b.Account)+":"+b.Amount.Currency] = b
		}
	}
	return out
}

func TestSimpleSingleCurrency(t *testing.T) {
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pos := amt(1000, "USD")
	neg := amt(-1000, "USD")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Checking", Amount: &pos},
		{Account: "Expenses:Coffee", Amount: &neg},
	})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, custom(day2, "Assets:Checking", "1000.00 USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	bal := balancesByCurrency(t, res)["Assets:Checking:USD"]
	if bal == nil {
		t.Fatalf("expected synthesized Balance for Assets:Checking USD, got nothing; result = %#v", res.Directives)
	}
	want := decimalLit(t, "1000.00")
	if bal.Amount.Number.Cmp(&want) != 0 {
		t.Errorf("amount = %s, want 1000.00", bal.Amount.Number.String())
	}
	if bal.Date != day2 {
		t.Errorf("date = %v, want %v", bal.Date, day2)
	}
	if bal.Tolerance != nil {
		t.Errorf("tolerance = %v, want nil", bal.Tolerance)
	}
	// Original Custom must be removed.
	for _, d := range res.Directives {
		if _, ok := d.(*ast.Custom); ok {
			t.Errorf("Custom directive not removed from result")
		}
	}
}

func TestMultipleCurrencies(t *testing.T) {
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	usdPos := amt(1000, "USD")
	usdNeg := amt(-1000, "USD")
	eurPos := amt(500, "EUR")
	eurNeg := amt(-500, "EUR")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Checking", Amount: &usdPos},
		{Account: "Assets:Checking", Amount: &eurPos},
		{Account: "Expenses:Other", Amount: &usdNeg},
		{Account: "Expenses:Other", Amount: &eurNeg},
	})

	body := "\n  1000.00 USD\n  500.00 EUR  ; European holdings\n"
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, custom(day2, "Assets:Checking", body)}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	got := balancesByCurrency(t, res)
	for cur, want := range map[string]string{"USD": "1000.00", "EUR": "500.00"} {
		key := "Assets:Checking:" + cur
		b := got[key]
		if b == nil {
			t.Fatalf("missing Balance for %s", key)
		}
		wantNum := decimalLit(t, want)
		if b.Amount.Number.Cmp(&wantNum) != 0 {
			t.Errorf("%s amount = %s, want %s", cur, b.Amount.Number.String(), want)
		}
	}
}

func TestUnlistedCommodityZeroAssertion(t *testing.T) {
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	usd := amt(1000, "USD")
	usdNeg := amt(-1000, "USD")
	jpy := amt(100, "JPY")
	jpyNeg := amt(-100, "JPY")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Checking", Amount: &usd},
		{Account: "Assets:Checking", Amount: &jpy},
		{Account: "Expenses:X", Amount: &usdNeg},
		{Account: "Expenses:X", Amount: &jpyNeg},
	})

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, custom(day2, "Assets:Checking", "1000.00 USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	got := balancesByCurrency(t, res)
	if got["Assets:Checking:USD"] == nil {
		t.Errorf("missing USD assertion")
	}
	jpyBal := got["Assets:Checking:JPY"]
	if jpyBal == nil {
		t.Fatalf("missing zero-balance assertion for JPY (unlisted but held)")
	}
	if jpyBal.Amount.Number.Sign() != 0 {
		t.Errorf("JPY assertion = %s, want 0", jpyBal.Amount.Number.String())
	}
}

func TestZeroBalanceCommodityIgnored(t *testing.T) {
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	usd := amt(1000, "USD")
	usdNeg := amt(-1000, "USD")
	eurIn := amt(100, "EUR")
	eurOut := amt(-100, "EUR")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Checking", Amount: &usd},
		{Account: "Assets:Checking", Amount: &eurIn},
		{Account: "Assets:Checking", Amount: &eurOut},
		{Account: "Expenses:X", Amount: &usdNeg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, custom(day2, "Assets:Checking", "1000.00 USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	got := balancesByCurrency(t, res)
	if _, has := got["Assets:Checking:EUR"]; has {
		t.Errorf("zero-EUR account got an unwanted Balance directive: %v", got["Assets:Checking:EUR"])
	}
}

func TestArithmeticExpression(t *testing.T) {
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pos := amt(150, "USD")
	neg := amt(-150, "USD")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Checking", Amount: &pos},
		{Account: "Expenses:Coffee", Amount: &neg},
	})
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, custom(day2, "Assets:Checking", "100 + 50 USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	b := balancesByCurrency(t, res)["Assets:Checking:USD"]
	want := decimalLit(t, "150")
	if b == nil || b.Amount.Number.Cmp(&want) != 0 {
		t.Errorf("amount = %v, want 150", b)
	}
}

func TestCommaFormattedNumbers(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{custom(day, "Assets:Checking", "1,234.56 USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	b := balancesByCurrency(t, res)["Assets:Checking:USD"]
	want := decimalLit(t, "1234.56")
	if b == nil || b.Amount.Number.Cmp(&want) != 0 {
		t.Errorf("amount = %v, want 1234.56", b)
	}
}

func TestLocalTolerance(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{custom(day, "Assets:Inv", "319.020 ~ 0.002 RGAGX")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	b := balancesByCurrency(t, res)["Assets:Inv:RGAGX"]
	if b == nil {
		t.Fatalf("no balance produced; got %#v", res.Directives)
	}
	wantNum := decimalLit(t, "319.020")
	if b.Amount.Number.Cmp(&wantNum) != 0 {
		t.Errorf("amount = %s, want 319.020", b.Amount.Number.String())
	}
	if b.Tolerance == nil {
		t.Fatalf("tolerance is nil, want 0.002")
	}
	wantTol := decimalLit(t, "0.002")
	if b.Tolerance.Cmp(&wantTol) != 0 {
		t.Errorf("tolerance = %s, want 0.002", b.Tolerance.String())
	}
}

func TestDuplicateCurrency(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	body := "\n  1000.00 USD\n  500.00 USD  ; second USD\n"
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{custom(day, "Assets:Checking", body)}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{Code: codeDuplicateCurrency, Span: testPluginDir.Span, Severity: ast.Error}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

func TestInvalidLine(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{custom(day, "Assets:Checking", "100 + USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatalf("expected a parse diagnostic, got none")
	}
	if got := res.Diagnostics[0].Code; got != codeParse {
		t.Errorf("Code = %q, want %q", got, codeParse)
	}
	if !strings.Contains(res.Diagnostics[0].Message, "amount-expr-parse") {
		t.Errorf("Message %q should mention rebased ast code amount-expr-parse", res.Diagnostics[0].Message)
	}
}

func TestInvalidParameters(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		cust *ast.Custom
	}{
		{"missing-body", &ast.Custom{
			Date: day, TypeName: customTypeName,
			Values: []ast.MetaValue{{Kind: ast.MetaAccount, String: "Assets:X"}},
		}},
		{"wrong-account-kind", &ast.Custom{
			Date: day, TypeName: customTypeName,
			Values: []ast.MetaValue{
				{Kind: ast.MetaString, String: "Assets:X"},
				{Kind: ast.MetaString, String: "100 USD"},
			},
		}},
		{"wrong-body-kind", &ast.Custom{
			Date: day, TypeName: customTypeName,
			Values: []ast.MetaValue{
				{Kind: ast.MetaAccount, String: "Assets:X"},
				{Kind: ast.MetaAmount, Amount: amt(100, "USD")},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := api.Input{
				Directive:  testPluginDir,
				Directives: seqOf([]ast.Directive{tc.cust}),
			}
			res, err := apply(context.Background(), in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(res.Diagnostics) != 1 {
				t.Fatalf("len(diagnostics) = %d, want 1; got %v", len(res.Diagnostics), res.Diagnostics)
			}
			if got := res.Diagnostics[0].Code; got != codeInvalidConfig {
				t.Errorf("Code = %q, want %q", got, codeInvalidConfig)
			}
		})
	}
}

func TestEmptyAndCommentLines(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	body := "\n; comment\n\n  1000 USD ; trailing\n; another comment\n"
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{custom(day, "Assets:Checking", body)}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	b := balancesByCurrency(t, res)["Assets:Checking:USD"]
	if b == nil {
		t.Fatalf("missing USD balance; got %#v", res.Directives)
	}
}

func TestSpanFallbackToPlugin(t *testing.T) {
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cust := &ast.Custom{
		Date: day, TypeName: customTypeName,
		Values: []ast.MetaValue{{Kind: ast.MetaAccount, String: "Assets:X"}},
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("len(diagnostics) = %d, want 1", len(res.Diagnostics))
	}
	if res.Diagnostics[0].Span != testPluginDir.Span {
		t.Errorf("Span = %#v, want plugin span %#v", res.Diagnostics[0].Span, testPluginDir.Span)
	}
}

func TestNoDirectiveMutation(t *testing.T) {
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pos := amt(1000, "USD")
	neg := amt(-1000, "USD")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Checking", Amount: &pos},
		{Account: "Expenses:X", Amount: &neg},
	})
	cust := custom(day2, "Assets:Checking", "1000 USD")
	origValues := append([]ast.MetaValue(nil), cust.Values...)
	origPostings := len(tx.Postings)

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, cust}),
	}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(origValues) != len(cust.Values) {
		t.Errorf("Custom.Values length mutated: %d -> %d", len(origValues), len(cust.Values))
	} else {
		for i := range origValues {
			if origValues[i].Kind != cust.Values[i].Kind || origValues[i].String != cust.Values[i].String {
				t.Errorf("Custom.Values[%d] mutated", i)
			}
		}
	}
	if len(tx.Postings) != origPostings {
		t.Errorf("tx.Postings mutated: %d -> %d", origPostings, len(tx.Postings))
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

func TestUnlistedSortedDeterministically(t *testing.T) {
	// Many unlisted currencies — sorted output is required for stable
	// downstream diffs.
	day1 := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	postings := []ast.Posting{}
	for _, cur := range []string{"USD", "JPY", "GBP", "AUD", "CHF"} {
		amount := amt(100, cur)
		negAmount := amt(-100, cur)
		postings = append(postings,
			ast.Posting{Account: "Assets:X", Amount: &amount},
			ast.Posting{Account: "Expenses:Y", Amount: &negAmount},
		)
	}
	tx := makeTx(day1, postings)
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, custom(day2, "Assets:X", "100 USD")}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	var seenCurrencies []string
	for _, d := range res.Directives {
		if b, ok := d.(*ast.Balance); ok {
			seenCurrencies = append(seenCurrencies, b.Amount.Currency)
		}
	}
	// First N-1 are sorted unlisted zero-balances; last is the declared USD.
	unlisted := seenCurrencies[:len(seenCurrencies)-1]
	declared := seenCurrencies[len(seenCurrencies)-1]
	if declared != "USD" {
		t.Errorf("declared assertion not last: %q", declared)
	}
	if !sort.StringsAreSorted(unlisted) {
		t.Errorf("unlisted balances not sorted: %v", unlisted)
	}
	// Spot-check that USD did not appear as zero (since it's declared).
	for _, cur := range unlisted {
		if cur == "USD" {
			t.Errorf("USD appears as zero-balance assertion; it should be declared only")
		}
	}
}
