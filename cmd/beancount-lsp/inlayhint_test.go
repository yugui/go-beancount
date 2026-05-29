package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// allRange is a zero-valued range, which the handler treats as unconstrained
// (return every hint). Tests that exercise viewport filtering use an explicit
// range instead.
var allRange protocol.Range

// callInlayHint sends textDocument/inlayHint and returns the decoded hints
// (nil when the server replied null).
func callInlayHint(t *testing.T, client *lspClient, docURI uri.URI, rng protocol.Range) []inlayHint {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/inlayHint", inlayHintParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Range:        rng,
	}, &raw); err != nil {
		t.Fatalf("inlayHint: call error: %v", err)
	}
	if string(raw) == "null" || len(raw) == 0 {
		return nil
	}
	var hints []inlayHint
	if err := json.Unmarshal(raw, &hints); err != nil {
		t.Fatalf("inlayHint: unmarshal: %v", err)
	}
	return hints
}

// awaitInlayHints retries until at least one hint arrives or the deadline
// passes, absorbing the window before the first session snapshot is ready.
func awaitInlayHints(t *testing.T, client *lspClient, docURI uri.URI, rng protocol.Range) []inlayHint {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		hints := callInlayHint(t, client, docURI, rng)
		if len(hints) > 0 || time.Now().After(deadline) {
			return hints
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// hintsOnLine returns the labels of hints anchored on the given 0-based line.
func hintsOnLine(hints []inlayHint, line uint32) []string {
	var out []string
	for _, h := range hints {
		if h.Position.Line == line {
			out = append(out, h.Label)
		}
	}
	return out
}

func TestInlayHint_Commodity_NameAndPrice(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-03-15 commodity AAPL
  name: "Apple Inc."
2024-03-15 price AAPL 195.00 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)

	hints := awaitInlayHints(t, client, uri.File(rootFile), allRange)
	got := hintsOnLine(hints, 0)
	want := "Apple Inc. · 195.00 USD @2024-03-15"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("commodity hint on line 0 = %q, want [%q]", got, want)
	}
}

func TestInlayHint_Commodity_NoNameUsesCurrency(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-03-15 commodity AAPL
2024-03-15 price AAPL 195.00 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)

	hints := awaitInlayHints(t, client, uri.File(rootFile), allRange)
	got := hintsOnLine(hints, 0)
	want := "AAPL · 195.00 USD @2024-03-15"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("commodity hint on line 0 = %q, want [%q]", got, want)
	}
}

func TestInlayHint_Commodity_NoPriceNameOnly(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-03-15 commodity AAPL
  name: "Apple Inc."
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)

	hints := awaitInlayHints(t, client, uri.File(rootFile), allRange)
	got := hintsOnLine(hints, 0)
	if len(got) != 1 || got[0] != "Apple Inc." {
		t.Fatalf("commodity hint on line 0 = %q, want [\"Apple Inc.\"]", got)
	}
}

func TestInlayHint_AutoPosting_InferredAmount(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank
2024-01-01 open Expenses:Food
2024-01-15 * "Dinner"
  Expenses:Food  20.00 USD
  Assets:Bank
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)

	hints := awaitInlayHints(t, client, uri.File(rootFile), allRange)
	got := hintsOnLine(hints, 4) // the elided "Assets:Bank" posting
	if len(got) != 1 || got[0] != "-20.00 USD" {
		t.Fatalf("auto-posting hint on line 4 = %q, want [\"-20.00 USD\"]", got)
	}
}

func TestInlayHint_CostSpec_ResolvedSingleLot(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Stock
2024-01-01 open Assets:Cash
2024-01-01 open Income:Gains
2024-01-01 * "Buy"
  Assets:Stock  10 AAPL {195.00 USD}
  Assets:Cash  -1950.00 USD
2024-03-15 * "Sell"
  Assets:Stock  -5 AAPL {}
  Assets:Cash  1000.00 USD
  Income:Gains
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)

	hints := awaitInlayHints(t, client, uri.File(rootFile), allRange)

	// Line 7: the empty {} cost spec resolves to the matched lot.
	got := hintsOnLine(hints, 7)
	if len(got) != 1 || got[0] != "{195.00 USD, 2024-01-01}" {
		t.Fatalf("cost hint on line 7 = %q, want [\"{195.00 USD, 2024-01-01}\"]", got)
	}
	// Line 9: the elided Income:Gains posting absorbs the residual.
	if got := hintsOnLine(hints, 9); len(got) != 1 || !strings.Contains(got[0], "USD") {
		t.Fatalf("auto-posting hint on line 9 = %q, want a USD amount", got)
	}
}

func TestInlayHint_CostSpec_MultiLotStripped(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Stock "FIFO"
2024-01-01 open Assets:Cash
2024-01-01 open Income:Gains
2024-01-01 * "Buy1"
  Assets:Stock  5 AAPL {100.00 USD}
  Assets:Cash  -500.00 USD
2024-01-02 * "Buy2"
  Assets:Stock  5 AAPL {110.00 USD}
  Assets:Cash  -550.00 USD
2024-01-03 * "Buy3"
  Assets:Stock  5 AAPL {120.00 USD}
  Assets:Cash  -600.00 USD
2024-03-15 * "Sell all"
  Assets:Stock  -15 AAPL {}
  Assets:Cash  2000.00 USD
  Income:Gains
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)

	hints := awaitInlayHints(t, client, uri.File(rootFile), allRange)
	got := hintsOnLine(hints, 13) // the -15 AAPL {} reduction across 3 lots
	if len(got) != 1 {
		t.Fatalf("multi-lot cost hint on line 13 = %q, want exactly one", got)
	}
	if !strings.Contains(got[0], "+1 more") {
		t.Errorf("multi-lot cost hint = %q, want a \"+1 more\" suffix", got[0])
	}
	if !strings.Contains(got[0], "{100.00 USD, 2024-01-01}") {
		t.Errorf("multi-lot cost hint = %q, want the first lot rendered", got[0])
	}
}

func TestInlayHint_RangeFilter(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("2024-03-15 commodity AAA\n")      // line 0
	sb.WriteString("2024-03-15 price AAA 1.00 USD\n") // line 1
	for i := 0; i < 40; i++ {
		sb.WriteString(";\n")
	}
	sb.WriteString("2024-03-15 commodity BBB\n")      // line 42
	sb.WriteString("2024-03-15 price BBB 2.00 USD\n") // line 43
	rootFile := writeTempFile(t, dir, "main.beancount", sb.String())
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Warm up: ensure the snapshot is ready and both commodities have hints.
	all := awaitInlayHints(t, client, docURI, allRange)
	if len(hintsOnLine(all, 0)) == 0 || len(hintsOnLine(all, 42)) == 0 {
		t.Fatalf("expected hints on lines 0 and 42, got %+v", all)
	}

	// Constrain to the first commodity only.
	rng := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 1, Character: 0},
	}
	hints := callInlayHint(t, client, docURI, rng)
	if len(hintsOnLine(hints, 0)) != 1 {
		t.Errorf("range-filtered hints missing line 0: %+v", hints)
	}
	if got := hintsOnLine(hints, 42); len(got) != 0 {
		t.Errorf("range filter leaked line 42 hint: %q", got)
	}
}

func TestInlayHint_NoImplicitInfo(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank
2024-01-01 open Expenses:Food
2024-01-15 * "Dinner"
  Expenses:Food  20.00 USD
  Assets:Bank  -20.00 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Nothing is elided or under-specified, so no hints should ever appear.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if hints := callInlayHint(t, client, docURI, allRange); len(hints) != 0 {
			t.Fatalf("expected no hints, got %+v", hints)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestInlayHint_CapabilityMarshal verifies the wrapper struct emits
// inlayHintProvider flat alongside the typed capabilities.
func TestInlayHint_CapabilityMarshal(t *testing.T) {
	caps := serverCapabilitiesExt{
		InlayHintProvider: true,
		ServerCapabilities: protocol.ServerCapabilities{
			HoverProvider: true,
		},
	}
	b, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	if !strings.Contains(js, `"inlayHintProvider":true`) {
		t.Errorf("capabilities JSON missing inlayHintProvider: %s", js)
	}
	if !strings.Contains(js, `"hoverProvider":true`) {
		t.Errorf("capabilities JSON missing hoverProvider (promotion failed): %s", js)
	}
}

func TestFmtCost(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		cost *ast.Cost
		want string
	}{
		{
			name: "number currency date",
			cost: &ast.Cost{Number: dec(t, "195.00"), Currency: "USD", Date: date},
			want: "{195.00 USD, 2024-01-01}",
		},
		{
			name: "with label",
			cost: &ast.Cost{Number: dec(t, "195.00"), Currency: "USD", Date: date, Label: "lot-a"},
			want: `{195.00 USD, 2024-01-01, "lot-a"}`,
		},
		{
			name: "no date",
			cost: &ast.Cost{Number: dec(t, "195.00"), Currency: "USD"},
			want: "{195.00 USD}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fmtCost(c.cost); got != c.want {
				t.Errorf("fmtCost = %q, want %q", got, c.want)
			}
		})
	}
}

func TestJoinStripped(t *testing.T) {
	cases := []struct {
		parts []string
		k     int
		want  string
	}{
		{nil, 2, ""},
		{[]string{"a"}, 2, "a"},
		{[]string{"a", "b"}, 2, "a, b"},
		{[]string{"a", "b", "c"}, 2, "a, b +1 more"},
		{[]string{"a", "b", "c", "d"}, 2, "a, b +2 more"},
	}
	for _, c := range cases {
		if got := joinStripped(c.parts, c.k); got != c.want {
			t.Errorf("joinStripped(%v, %d) = %q, want %q", c.parts, c.k, got, c.want)
		}
	}
}

func TestCommodityDisplayName(t *testing.T) {
	withName := &ast.Commodity{
		Currency: "AAPL",
		Meta:     ast.Metadata{Props: map[string]ast.MetaValue{"name": {Kind: ast.MetaString, String: "Apple Inc."}}},
	}
	if got := commodityDisplayName(withName); got != "Apple Inc." {
		t.Errorf("commodityDisplayName(named) = %q, want %q", got, "Apple Inc.")
	}
	bare := &ast.Commodity{Currency: "AAPL"}
	if got := commodityDisplayName(bare); got != "AAPL" {
		t.Errorf("commodityDisplayName(bare) = %q, want %q", got, "AAPL")
	}
}

// dec parses a decimal literal for test fixtures.
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("dec(%q): %v", s, err)
	}
	return *d
}
