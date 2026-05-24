package comprehensivebalance

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

// TestNetZeroCommodityAsserted documents the post-delegation contract:
// a currency that appeared in any prior posting (even if the net is
// zero) is part of the commodity universe and gets a zero-balance
// assertion. The downstream balance plugin verifies the actual residual
// matches; this plugin no longer pre-filters by computed sum.
func TestNetZeroCommodityAsserted(t *testing.T) {
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
	eur := got["Assets:Checking:EUR"]
	if eur == nil {
		t.Fatalf("expected zero-balance EUR assertion; got %#v", res.Directives)
	}
	if eur.Amount.Number.Sign() != 0 {
		t.Errorf("EUR assertion amount = %s, want 0", eur.Amount.Number.String())
	}
	if eur.Tolerance != nil {
		t.Errorf("EUR assertion tolerance = %v, want nil", eur.Tolerance)
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

func TestEmittedBalancesSortedByCurrency(t *testing.T) {
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
	wantCurrencies := []string{"AUD", "CHF", "GBP", "JPY", "USD"}
	if diff := cmp.Diff(wantCurrencies, seenCurrencies); diff != "" {
		t.Errorf("emit order mismatch (-want +got):\n%s", diff)
	}
	got := balancesByCurrency(t, res)
	usd := got["Assets:X:USD"]
	if usd == nil {
		t.Fatalf("missing USD balance")
	}
	wantUSD := decimalLit(t, "100")
	if usd.Amount.Number.Cmp(&wantUSD) != 0 {
		t.Errorf("USD amount = %s, want 100", usd.Amount.Number.String())
	}
	for _, cur := range []string{"AUD", "CHF", "GBP", "JPY"} {
		b := got["Assets:X:"+cur]
		if b == nil {
			t.Fatalf("missing %s zero-balance", cur)
		}
		if b.Amount.Number.Sign() != 0 {
			t.Errorf("%s zero-balance got %s, want 0", cur, b.Amount.Number.String())
		}
	}
}

// TestPadBridgedAccount: a pad+balance pair preceding the Custom must
// pass through unchanged, and the Custom must emit Balance directives
// whose evaluation the downstream pad→balance pipeline will resolve
// against the padded inventory.
func TestPadBridgedAccount(t *testing.T) {
	day1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	day3 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	pad := &ast.Pad{Date: day1, Account: "Assets:Foo", PadAccount: "Equity:Opening"}
	priorBal := &ast.Balance{Date: day2, Account: "Assets:Foo", Amount: amt(1000, "USD")}
	cust := custom(day3, "Assets:Foo", "1000.00 USD")

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{pad, priorBal, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	if res.Directives[0] != ast.Directive(pad) {
		t.Errorf("pad directive not preserved at position 0; got %#v", res.Directives[0])
	}
	if res.Directives[1] != ast.Directive(priorBal) {
		t.Errorf("prior balance not preserved at position 1; got %#v", res.Directives[1])
	}
	bal := balancesByCurrency(t, res)["Assets:Foo:USD"]
	if bal == nil {
		t.Fatalf("expected emitted USD balance from Custom; got %#v", res.Directives)
	}
	want := decimalLit(t, "1000.00")
	if bal.Amount.Number.Cmp(&want) != 0 {
		t.Errorf("USD amount = %s, want 1000.00", bal.Amount.Number.String())
	}
	if bal.Date != day3 {
		t.Errorf("emitted balance date = %v, want %v", bal.Date, day3)
	}
}

// TestPriorBalanceContributesCommodity: a currency that appeared only
// in a prior *ast.Balance (never in a posting before the Custom) is
// nonetheless in the universe, so the Custom emits a zero-assertion
// for it.
func TestPriorBalanceContributesCommodity(t *testing.T) {
	day1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	priorBal := &ast.Balance{Date: day1, Account: "Assets:Foo", Amount: amt(1000, "USD")}
	cust := custom(day2, "Assets:Foo", "0 EUR")

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{priorBal, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	got := balancesByCurrency(t, res)
	usd := got["Assets:Foo:USD"]
	if usd == nil {
		t.Fatalf("expected USD balance from prior-balance contribution; got %#v", res.Directives)
	}
	if usd.Amount.Number.Sign() != 0 {
		t.Errorf("USD assertion amount = %s, want 0 (unlisted)", usd.Amount.Number.String())
	}
	if usd.Tolerance != nil {
		t.Errorf("USD assertion tolerance = %v, want nil", usd.Tolerance)
	}
	eur := got["Assets:Foo:EUR"]
	if eur == nil {
		t.Fatalf("expected listed EUR balance; got %#v", res.Directives)
	}
	if eur.Amount.Number.Sign() != 0 {
		t.Errorf("EUR assertion amount = %s, want 0 (listed)", eur.Amount.Number.String())
	}
}

// TestFutureBalanceIgnored: a *ast.Balance appearing AFTER the Custom
// in source order must not contribute to the universe — only prior
// directives do.
func TestFutureBalanceIgnored(t *testing.T) {
	day1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	tx := makeTx(day1, []ast.Posting{
		{Account: "Assets:Foo", Amount: &pos},
		{Account: "Expenses:X", Amount: &neg},
	})
	cust := custom(day1, "Assets:Foo", "100 USD")
	futureBal := &ast.Balance{Date: day2, Account: "Assets:Foo", Amount: amt(50, "EUR")}

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{tx, cust, futureBal}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	got := balancesByCurrency(t, res)
	// EUR appears in res.Directives only via the trailing user-written
	// balance, not via a Custom-emitted assertion. We can't distinguish
	// the two via balancesByCurrency directly, so check the Custom's
	// emit count is exactly 1 (USD only) by counting Balance directives
	// dated at the Custom's date.
	var custEmitted []*ast.Balance
	for _, d := range res.Directives {
		if b, ok := d.(*ast.Balance); ok && b.Date.Equal(cust.Date) {
			custEmitted = append(custEmitted, b)
		}
	}
	if len(custEmitted) != 1 {
		t.Fatalf("Custom-emitted balance count at %v = %d, want 1; got %#v", cust.Date, len(custEmitted), custEmitted)
	}
	if custEmitted[0].Amount.Currency != "USD" {
		t.Errorf("Custom-emitted currency = %q, want USD", custEmitted[0].Amount.Currency)
	}
	// futureBal must still be in the output unchanged.
	if got["Assets:Foo:EUR"] != futureBal {
		t.Errorf("future EUR balance not preserved verbatim")
	}
}

// TestPendingPadAtCustomDate: a pad directive immediately followed by
// a comprehensive_balance Custom with no intervening user balance —
// the Custom's emitted *ast.Balance is the pad's fire target. Here we
// only verify the Custom emits Balance directives in the expected
// position; the pad firing semantics are validated by pad's own tests.
func TestPendingPadAtCustomDate(t *testing.T) {
	day1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	pad := &ast.Pad{Date: day1, Account: "Assets:Foo", PadAccount: "Equity:Opening"}
	cust := custom(day2, "Assets:Foo", "1000.00 USD")

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{pad, cust}),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", res.Diagnostics)
	}
	if len(res.Directives) != 2 {
		t.Fatalf("len(res.Directives) = %d, want 2 (pad + emitted balance); got %#v", len(res.Directives), res.Directives)
	}
	if res.Directives[0] != ast.Directive(pad) {
		t.Errorf("pad not preserved at position 0; got %#v", res.Directives[0])
	}
	b, ok := res.Directives[1].(*ast.Balance)
	if !ok {
		t.Fatalf("position 1 = %#v, want *ast.Balance", res.Directives[1])
	}
	want := decimalLit(t, "1000.00")
	if b.Amount.Number.Cmp(&want) != 0 || b.Amount.Currency != "USD" {
		t.Errorf("emitted balance = %s %s, want 1000.00 USD", b.Amount.Number.String(), b.Amount.Currency)
	}
	if b.Account != "Assets:Foo" {
		t.Errorf("emitted balance account = %q, want Assets:Foo", b.Account)
	}
	if b.Date != day2 {
		t.Errorf("emitted balance date = %v, want %v", b.Date, day2)
	}
}
