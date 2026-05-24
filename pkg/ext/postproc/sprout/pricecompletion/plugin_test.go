package pricecompletion

import (
	"context"
	"iter"
	"math"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// diagCmpOpts compares ast.Diagnostic values structurally while leaving
// the human-readable Message field to per-test substring assertions.
var diagCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(ast.Diagnostic{}, "Message"),
}

// testPluginDir is a non-zero *ast.Plugin used as the api.Input.Directive
// fallback span in tests.
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

// dec parses a decimal literal for test convenience; t.Fatal on parse
// failure.
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.BaseContext.NewFromString(s)
	if err != nil {
		t.Fatalf("dec(%q): %v", s, err)
	}
	return *d
}

// optionsForTest builds an OptionValues by parsing a tiny in-memory
// ledger that declares the given operating currencies.
func optionsForTest(t *testing.T, currencies ...string) *ast.OptionValues {
	t.Helper()
	if len(currencies) == 0 {
		return ast.NewOptionValues()
	}
	ledger := &ast.Ledger{}
	for _, c := range currencies {
		ledger.Insert(&ast.Option{Key: "operating_currency", Value: c})
	}
	opts, diags := ast.ParseOptions(ledger)
	if len(diags) != 0 {
		t.Fatalf("ParseOptions diags = %v", diags)
	}
	return opts
}

func date(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func price(date time.Time, base, qNum, qCur string, line int) *ast.Price {
	var n apd.Decimal
	if _, _, err := n.SetString(qNum); err != nil {
		panic(err)
	}
	return &ast.Price{
		Date:      date,
		Commodity: base,
		Amount:    ast.Amount{Number: n, Currency: qCur},
		Span:      ast.Span{Start: ast.Position{Filename: "l.beancount", Line: line}},
	}
}

// extractDerived returns the prices in out that are not the same
// pointer as one of the input prices in orig.
func extractDerived(out []ast.Directive, orig []*ast.Price) []*ast.Price {
	keep := make(map[*ast.Price]struct{}, len(orig))
	for _, p := range orig {
		keep[p] = struct{}{}
	}
	var derived []*ast.Price
	for _, d := range out {
		p, ok := d.(*ast.Price)
		if !ok {
			continue
		}
		if _, original := keep[p]; original {
			continue
		}
		derived = append(derived, p)
	}
	return derived
}

func decEqual(a, b apd.Decimal) bool { return a.Cmp(&b) == 0 }

// decApprox checks |a - want| <= tol*want where want and tol are decimal
// literals.
func decApprox(t *testing.T, a apd.Decimal, want, tol string) bool {
	t.Helper()
	w := dec(t, want)
	tolFrac := dec(t, tol)
	diff := new(apd.Decimal)
	if _, err := apd.BaseContext.WithPrecision(34).Sub(diff, &a, &w); err != nil {
		t.Fatalf("Sub: %v", err)
	}
	absDiff := new(apd.Decimal)
	if _, err := apd.BaseContext.WithPrecision(34).Abs(absDiff, diff); err != nil {
		t.Fatalf("Abs: %v", err)
	}
	allowed := new(apd.Decimal)
	if _, err := apd.BaseContext.WithPrecision(34).Mul(allowed, &w, &tolFrac); err != nil {
		t.Fatalf("Mul: %v", err)
	}
	absAllowed := new(apd.Decimal)
	if _, err := apd.BaseContext.WithPrecision(34).Abs(absAllowed, allowed); err != nil {
		t.Fatalf("Abs: %v", err)
	}
	return absDiff.Cmp(absAllowed) <= 0
}

func findDerived(derived []*ast.Price, base, quote string) []*ast.Price {
	var out []*ast.Price
	for _, p := range derived {
		if p.Commodity == base && p.Amount.Currency == quote {
			out = append(out, p)
		}
	}
	return out
}

// TestParseConfigDefaults: empty input applies the documented defaults.
func TestParseConfigDefaults(t *testing.T) {
	cfg, diags := parseConfig("", testPluginDir)
	if cfg.temporalBase != defaultTemporalBase || cfg.temporalScale != defaultTemporalScale {
		t.Errorf("cfg = %+v, want defaults (%v,%v)", cfg, defaultTemporalBase, defaultTemporalScale)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want none", diags)
	}
}

// TestParseConfigValid: both keys are accepted; values flow through.
func TestParseConfigValid(t *testing.T) {
	cfg, diags := parseConfig("temporal_base=2.0,temporal_scale=0.5", testPluginDir)
	if cfg.temporalBase != 2.0 || cfg.temporalScale != 0.5 {
		t.Errorf("cfg = %+v, want {2.0, 0.5}", cfg)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want none", diags)
	}
}

// TestParseConfigPartial: missing key keeps its default.
func TestParseConfigPartial(t *testing.T) {
	cfg, diags := parseConfig("temporal_base=3.0", testPluginDir)
	if cfg.temporalBase != 3.0 || cfg.temporalScale != defaultTemporalScale {
		t.Errorf("cfg = %+v, want {3.0, default}", cfg)
	}
	if len(diags) != 0 {
		t.Errorf("diags = %v, want none", diags)
	}
}

// TestParseConfigInvalidValue: a non-numeric value falls back to default
// and produces a diagnostic with the documented code.
func TestParseConfigInvalidValue(t *testing.T) {
	cfg, diags := parseConfig("temporal_base=oops,temporal_scale=0.3", testPluginDir)
	if cfg.temporalBase != defaultTemporalBase {
		t.Errorf("cfg.temporalBase = %v, want default %v", cfg.temporalBase, defaultTemporalBase)
	}
	if cfg.temporalScale != 0.3 {
		t.Errorf("cfg.temporalScale = %v, want 0.3", cfg.temporalScale)
	}
	if len(diags) != 1 || diags[0].Code != codeInvalidConfig {
		t.Fatalf("diags = %v, want one with code %q", diags, codeInvalidConfig)
	}
}

// TestParseConfigUnknownKey: unknown key produces a diagnostic.
func TestParseConfigUnknownKey(t *testing.T) {
	cfg, diags := parseConfig("bogus=4.5", testPluginDir)
	if cfg.temporalBase != defaultTemporalBase || cfg.temporalScale != defaultTemporalScale {
		t.Errorf("cfg = %+v, want defaults", cfg)
	}
	if len(diags) != 1 || diags[0].Code != codeInvalidConfig {
		t.Fatalf("diags = %v, want one with code %q", diags, codeInvalidConfig)
	}
}

// TestEdgeWeightFresh: a price observation dated on the target date
// always has weight 1.0 regardless of the temporal parameters.
func TestEdgeWeightFresh(t *testing.T) {
	w := edgeWeight(date(2023, 1, 5), date(2023, 1, 5), 7.5, 11.0)
	if w != 1.0 {
		t.Errorf("edgeWeight(same date) = %v, want 1.0", w)
	}
}

// TestEdgeWeightHistorical: weight = base + scale * ln(days).
func TestEdgeWeightHistorical(t *testing.T) {
	w := edgeWeight(date(2023, 1, 5), date(2023, 1, 1), 1.0, 0.1)
	want := 1.0 + 0.1*math.Log(4)
	if math.Abs(w-want) > 1e-9 {
		t.Errorf("edgeWeight = %v, want %v", w, want)
	}
}

// TestEmptyLedger: nil/empty directive stream returns an empty result.
func TestEmptyLedger(t *testing.T) {
	in := api.Input{Directive: testPluginDir, Directives: seqOf(nil), Options: optionsForTest(t, "USD")}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Directives != nil {
		t.Errorf("res.Directives = %v, want nil for empty input", res.Directives)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("res.Diagnostics = %v, want none", res.Diagnostics)
	}
}

// TestNoPrices: when no Price directives are present, the ledger is
// returned unchanged.
func TestNoPrices(t *testing.T) {
	open := &ast.Open{Date: date(2023, 1, 1), Account: "Assets:Cash"}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{open}),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Directives) != 1 {
		t.Errorf("len(res.Directives) = %d, want 1 (unchanged)", len(res.Directives))
	}
}

// TestNoOperatingCurrencies: ledger with prices but no operating
// currency option produces no synthesis and no diagnostic.
func TestNoOperatingCurrencies(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0]}),
		Options:    ast.NewOptionValues(),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)
	if len(derived) != 0 {
		t.Errorf("derived = %v, want none with no operating_currency", derived)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("res.Diagnostics = %v, want none", res.Diagnostics)
	}
}

// TestSingleCommodityPair: USD->JPY with USD and JPY as operating
// currencies derives JPY->USD as 1/130.
func TestSingleCommodityPair(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 3), "USD", "130", "JPY", 10),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0]}),
		Options:    optionsForTest(t, "USD", "JPY"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)

	jpyUSD := findDerived(derived, "JPY", "USD")
	if len(jpyUSD) != 1 {
		t.Fatalf("len(JPY/USD) = %d, want 1", len(jpyUSD))
	}
	want := dec(t, "1") // 1/130 computed inline
	out := new(apd.Decimal)
	if _, err := arithCtx.Quo(out, &want, apd.New(130, 0)); err != nil {
		t.Fatalf("Quo: %v", err)
	}
	if !decEqual(jpyUSD[0].Amount.Number, *out) {
		t.Errorf("JPY/USD number = %s, want %s", jpyUSD[0].Amount.Number.String(), out.String())
	}
}

// TestThreeHopChain: BTC/USD and ETH/BTC let the plugin derive ETH/USD
// as 2 * 50000 = 100000.
func TestThreeHopChain(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "ETH", "2", "BTC", 20),
	}
	dirs := []ast.Directive{prices[0], prices[1]}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)
	ethUSD := findDerived(derived, "ETH", "USD")
	if len(ethUSD) != 1 {
		t.Fatalf("len(ETH/USD) = %d, want 1", len(ethUSD))
	}
	want := dec(t, "100000")
	if !decEqual(ethUSD[0].Amount.Number, want) {
		t.Errorf("ETH/USD = %s, want 100000", ethUSD[0].Amount.Number.String())
	}
}

// TestExistingPriceNotDuplicated: when an explicit BTC/USD already
// exists on a date, no synthesized BTC/USD is added.
func TestExistingPriceNotDuplicated(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "BTC", "50000", "USD", 20),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1]}),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)
	if found := findDerived(derived, "BTC", "USD"); len(found) != 0 {
		t.Errorf("derived BTC/USD = %v, want none (already present)", found)
	}
}

// TestUnreachableCommodities: EUR/GBP island is not reachable from USD;
// no EUR/USD or GBP/USD is synthesized.
func TestUnreachableCommodities(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "EUR", "0.9", "GBP", 20),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1]}),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)
	if found := findDerived(derived, "EUR", "USD"); len(found) != 0 {
		t.Errorf("derived EUR/USD = %v, want none (unreachable)", found)
	}
	if found := findDerived(derived, "GBP", "USD"); len(found) != 0 {
		t.Errorf("derived GBP/USD = %v, want none (unreachable)", found)
	}
}

// TestAllHistoricalEdgesNoCompletion: when the only path to a target
// commodity on the current date consists entirely of historical edges,
// no synthesis happens.
func TestAllHistoricalEdgesNoCompletion(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "ETH", "2", "BTC", 20),
		// Day 5 has only an unrelated USDT price — no BTC or ETH
		// observations on day 5, so any BTC/USD or ETH/USD derivation
		// for day 5 must traverse historical-only edges.
		price(date(2023, 1, 5), "USDT", "1.00", "USD", 30),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1], prices[2]}),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)

	for _, p := range derived {
		if p.Date.Equal(date(2023, 1, 5)) && (p.Commodity == "ETH" || p.Commodity == "BTC") {
			t.Errorf("unexpected day-5 derived price %s/%s = %s (path is all historical)",
				p.Commodity, p.Amount.Currency, p.Amount.Number.String())
		}
	}
}

// TestMixedFreshAndHistorical: a path with one fresh edge and one
// historical edge does produce a synthesis. ETH/BTC from day 1 plus
// BTC/USD from day 3 -> ETH/USD on day 3 = 2 * 51000.
func TestMixedFreshAndHistorical(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "ETH", "2", "BTC", 10),
		price(date(2023, 1, 3), "BTC", "51000", "USD", 20),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1]}),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)

	var day3ETH []*ast.Price
	for _, p := range derived {
		if p.Date.Equal(date(2023, 1, 3)) && p.Commodity == "ETH" && p.Amount.Currency == "USD" {
			day3ETH = append(day3ETH, p)
		}
	}
	if len(day3ETH) != 1 {
		t.Fatalf("day3 ETH/USD count = %d, want 1", len(day3ETH))
	}
	want := dec(t, "102000")
	if !decEqual(day3ETH[0].Amount.Number, want) {
		t.Errorf("day3 ETH/USD = %s, want 102000", day3ETH[0].Amount.Number.String())
	}
}

// TestMultipleOperatingCurrencies: with USD, JPY, EUR as operating
// currencies and prices BTC/USD and USD/JPY, the plugin derives BTC/JPY
// (= 50000 * 130) and JPY/USD (= 1/130).
func TestMultipleOperatingCurrencies(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "USD", "130", "JPY", 20),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1]}),
		Options:    optionsForTest(t, "USD", "JPY", "EUR"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)

	btcJPY := findDerived(derived, "BTC", "JPY")
	if len(btcJPY) != 1 {
		t.Fatalf("BTC/JPY count = %d, want 1", len(btcJPY))
	}
	// 6,500,000 is computed via JPY -> USD -> BTC then inverted, so the
	// inverse chain introduces a sub-attounit rounding artifact. Tolerate
	// any error smaller than 1e-20 of the expected value.
	if !decApprox(t, btcJPY[0].Amount.Number, "6500000", "1e-20") {
		t.Errorf("BTC/JPY = %s, want ~6500000", btcJPY[0].Amount.Number.String())
	}

	jpyUSD := findDerived(derived, "JPY", "USD")
	if len(jpyUSD) != 1 {
		t.Fatalf("JPY/USD count = %d, want 1", len(jpyUSD))
	}
	// 1/130
	one := apd.New(1, 0)
	wantInv := new(apd.Decimal)
	if _, err := arithCtx.Quo(wantInv, one, apd.New(130, 0)); err != nil {
		t.Fatalf("Quo: %v", err)
	}
	if !decEqual(jpyUSD[0].Amount.Number, *wantInv) {
		t.Errorf("JPY/USD = %s, want %s", jpyUSD[0].Amount.Number.String(), wantInv.String())
	}
}

// TestCircularReferences: cyclic A->B, B->C, C->A does not infinite-loop
// and the plugin completes.
func TestCircularReferences(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "A", "2", "B", 10),
		price(date(2023, 1, 1), "B", "3", "C", 20),
		price(date(2023, 1, 1), "C", "0.5", "A", 30),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1], prices[2]}),
		Options:    optionsForTest(t, "A"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := apply(ctx, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("diags = %v, want none", res.Diagnostics)
	}
}

// TestMultipleDatesProcessedIndependently: independent date buckets
// each get their own derived prices.
func TestMultipleDatesProcessedIndependently(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "ETH", "2", "BTC", 20),
		price(date(2023, 2, 1), "BTC", "55000", "USD", 30),
		price(date(2023, 2, 1), "ETH", "3", "BTC", 40),
	}
	dirs := make([]ast.Directive, len(prices))
	for i := range prices {
		dirs[i] = prices[i]
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)

	want := map[time.Time]string{
		date(2023, 1, 1): "100000",
		date(2023, 2, 1): "165000",
	}
	got := make(map[time.Time]apd.Decimal)
	for _, p := range derived {
		if p.Commodity == "ETH" && p.Amount.Currency == "USD" {
			got[p.Date] = p.Amount.Number
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ETH/USD derivations, want %d (got=%v)", len(got), len(want), got)
	}
	for d, ws := range want {
		w := dec(t, ws)
		g, ok := got[d]
		if !ok {
			t.Errorf("missing ETH/USD on %s", d.Format("2006-01-02"))
			continue
		}
		if !decEqual(g, w) {
			t.Errorf("ETH/USD on %s = %s, want %s", d.Format("2006-01-02"), g.String(), ws)
		}
	}
}

// TestMetadataPropagation: synthesized prices carry the metadata of the
// edge incident on the target commodity (the "closest" edge along the
// path).
func TestMetadataPropagation(t *testing.T) {
	btc := price(date(2023, 1, 1), "BTC", "50000", "USD", 10)
	btc.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: "btc"}}}
	eth := price(date(2023, 1, 1), "ETH", "2", "BTC", 20)
	eth.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: "eth"}}}

	prices := []*ast.Price{btc, eth}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{btc, eth}),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)
	ethUSD := findDerived(derived, "ETH", "USD")
	if len(ethUSD) != 1 {
		t.Fatalf("len(ETH/USD) = %d, want 1", len(ethUSD))
	}
	v, ok := ethUSD[0].Meta.Props["src"]
	if !ok || v.String != "eth" {
		t.Errorf("ETH/USD meta = %v, want src=eth (closest edge)", ethUSD[0].Meta.Props)
	}
}

// TestSynthesizedSpanFallsBackToPlugin: a synthesized Price anchors at
// the triggering plugin directive's span (no original posting exists).
func TestSynthesizedSpanFallsBackToPlugin(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 3), "USD", "130", "JPY", 10),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0]}),
		Options:    optionsForTest(t, "USD", "JPY"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)
	if len(derived) == 0 {
		t.Fatalf("no derived prices")
	}
	if derived[0].Span != testPluginDir.Span {
		t.Errorf("derived span = %#v, want plugin span %#v", derived[0].Span, testPluginDir.Span)
	}
}

// TestInvalidConfigStillRuns: a malformed config produces a diagnostic
// but does not halt synthesis; defaults are applied to the bad
// parameter.
func TestInvalidConfigStillRuns(t *testing.T) {
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "ETH", "2", "BTC", 20),
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{prices[0], prices[1]}),
		Options:    optionsForTest(t, "USD"),
		Config:     "temporal_base=oops",
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ast.Diagnostic{{Code: codeInvalidConfig, Span: testPluginDir.Span}}
	if diff := cmp.Diff(want, res.Diagnostics, diagCmpOpts); diff != "" {
		t.Fatalf("diags mismatch (-want +got):\n%s", diff)
	}
	derived := extractDerived(res.Directives, prices)
	if len(findDerived(derived, "ETH", "USD")) != 1 {
		t.Errorf("ETH/USD not synthesized despite recoverable config error")
	}
}

// TestInputDirectivesNotMutated: the plugin appends to a new slice and
// does not touch any input Price.
func TestInputDirectivesNotMutated(t *testing.T) {
	p := price(date(2023, 1, 1), "BTC", "50000", "USD", 10)
	origNumber := p.Amount.Number.String()
	origCurrency := p.Amount.Currency
	origCommodity := p.Commodity
	origDate := p.Date

	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf([]ast.Directive{p}),
		Options:    optionsForTest(t, "USD"),
	}
	if _, err := apply(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := p.Amount.Number.String(); got != origNumber {
		t.Errorf("input price number mutated: %s -> %s", origNumber, got)
	}
	if p.Amount.Currency != origCurrency || p.Commodity != origCommodity || !p.Date.Equal(origDate) {
		t.Errorf("input price fields mutated")
	}
}

// TestCanceledContext: a canceled context aborts the plugin with the
// underlying error before any work is done.
func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := apply(ctx, api.Input{Directive: testPluginDir, Options: optionsForTest(t, "USD")})
	if err == nil {
		t.Fatal("apply error = nil, want non-nil on canceled context")
	}
}

// TestTemporalWeightPrefersFreshDirectPath: when two paths exist —
// one direct fresh edge and one historical multi-hop — Dijkstra picks
// the fresh direct edge and the derived price reflects today's quote.
func TestTemporalWeightPrefersFreshDirectPath(t *testing.T) {
	// Day 1: BTC/USD at 50000 (historical from day-5 perspective).
	// Day 1: ETH/USD at 1500 (historical).
	// Day 1: ETH/BTC at 0.03 (historical) — would otherwise give a
	//   2-hop path USD -> BTC -> ETH at the same direction.
	// Day 5: ETH/USD at 1700 (fresh, direct).
	// Expected: ETH/USD derived for day 5 should NOT replace the
	// directly recorded value (already present); USD-side derivations
	// for ETH and BTC on day 5 must traverse a fresh edge to be
	// emitted.
	prices := []*ast.Price{
		price(date(2023, 1, 1), "BTC", "50000", "USD", 10),
		price(date(2023, 1, 1), "ETH", "1500", "USD", 20),
		price(date(2023, 1, 1), "ETH", "0.03", "BTC", 30),
		price(date(2023, 1, 5), "ETH", "1700", "USD", 40),
	}
	dirs := make([]ast.Directive, len(prices))
	for i := range prices {
		dirs[i] = prices[i]
	}
	in := api.Input{
		Directive:  testPluginDir,
		Directives: seqOf(dirs),
		Options:    optionsForTest(t, "USD"),
	}
	res, err := apply(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	derived := extractDerived(res.Directives, prices)

	// No ETH/USD derivation on day 5 because the direct price exists.
	for _, p := range derived {
		if p.Date.Equal(date(2023, 1, 5)) && p.Commodity == "ETH" && p.Amount.Currency == "USD" {
			t.Errorf("unexpected day-5 ETH/USD derivation %s — direct price already present", p.Amount.Number.String())
		}
	}
}
