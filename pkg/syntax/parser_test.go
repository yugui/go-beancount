package syntax

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOption(t *testing.T) {
	src := `option "title" "My Ledger"`
	f := Parse(src)

	assertNoErrors(t, f)

	// Root should have 1 directive + EOF token.
	root := f.Root
	if root.Kind != FileNode {
		t.Fatalf("root kind = %v, want FileNode", root.Kind)
	}

	// Find the OptionDirective node.
	opt := root.FindNode(OptionDirective)
	if opt == nil {
		t.Fatal("expected OptionDirective node")
	}
	if len(opt.Children) != 3 {
		t.Fatalf("OptionDirective children = %d, want 3", len(opt.Children))
	}

	assertTokenChild(t, opt.Children[0], IDENT, "option")
	assertTokenChild(t, opt.Children[1], STRING, `"title"`)
	assertTokenChild(t, opt.Children[2], STRING, `"My Ledger"`)

	assertRoundTrip(t, src, f)
}

func TestParsePlugin(t *testing.T) {
	src := `plugin "module.name"`
	f := Parse(src)

	assertNoErrors(t, f)

	plug := f.Root.FindNode(PluginDirective)
	if plug == nil {
		t.Fatal("expected PluginDirective node")
	}
	if len(plug.Children) != 2 {
		t.Fatalf("PluginDirective children = %d, want 2", len(plug.Children))
	}

	assertTokenChild(t, plug.Children[0], IDENT, "plugin")
	assertTokenChild(t, plug.Children[1], STRING, `"module.name"`)

	assertRoundTrip(t, src, f)
}

func TestParsePluginWithConfig(t *testing.T) {
	src := `plugin "module.name" "config"`
	f := Parse(src)

	assertNoErrors(t, f)

	plug := f.Root.FindNode(PluginDirective)
	if plug == nil {
		t.Fatal("expected PluginDirective node")
	}
	if len(plug.Children) != 3 {
		t.Fatalf("PluginDirective children = %d, want 3", len(plug.Children))
	}

	assertTokenChild(t, plug.Children[0], IDENT, "plugin")
	assertTokenChild(t, plug.Children[1], STRING, `"module.name"`)
	assertTokenChild(t, plug.Children[2], STRING, `"config"`)

	assertRoundTrip(t, src, f)
}

func TestParseInclude(t *testing.T) {
	src := `include "other.beancount"`
	f := Parse(src)

	assertNoErrors(t, f)

	inc := f.Root.FindNode(IncludeDirective)
	if inc == nil {
		t.Fatal("expected IncludeDirective node")
	}
	if len(inc.Children) != 2 {
		t.Fatalf("IncludeDirective children = %d, want 2", len(inc.Children))
	}

	assertTokenChild(t, inc.Children[0], IDENT, "include")
	assertTokenChild(t, inc.Children[1], STRING, `"other.beancount"`)

	assertRoundTrip(t, src, f)
}

func TestParsePushtag(t *testing.T) {
	src := `pushtag #trip`
	f := Parse(src)

	assertNoErrors(t, f)

	pt := f.Root.FindNode(PushtagDirective)
	if pt == nil {
		t.Fatal("expected PushtagDirective node")
	}
	if len(pt.Children) != 2 {
		t.Fatalf("PushtagDirective children = %d, want 2", len(pt.Children))
	}

	assertTokenChild(t, pt.Children[0], IDENT, "pushtag")
	assertTokenChild(t, pt.Children[1], TAG, "#trip")

	assertRoundTrip(t, src, f)
}

func TestParsePoptag(t *testing.T) {
	src := `poptag #trip`
	f := Parse(src)

	assertNoErrors(t, f)

	pt := f.Root.FindNode(PoptagDirective)
	if pt == nil {
		t.Fatal("expected PoptagDirective node")
	}
	if len(pt.Children) != 2 {
		t.Fatalf("PoptagDirective children = %d, want 2", len(pt.Children))
	}

	assertTokenChild(t, pt.Children[0], IDENT, "poptag")
	assertTokenChild(t, pt.Children[1], TAG, "#trip")

	assertRoundTrip(t, src, f)
}

func TestParseMultipleDirectives(t *testing.T) {
	src := "option \"title\" \"Test\"\ninclude \"other.beancount\"\nplugin \"module\"\n"
	f := Parse(src)

	assertNoErrors(t, f)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 3 {
		t.Fatalf("got %d directive nodes, want 3", len(nodes))
	}
	if nodes[0].Kind != OptionDirective {
		t.Errorf("nodes[0].Kind = %v, want OptionDirective", nodes[0].Kind)
	}
	if nodes[1].Kind != IncludeDirective {
		t.Errorf("nodes[1].Kind = %v, want IncludeDirective", nodes[1].Kind)
	}
	if nodes[2].Kind != PluginDirective {
		t.Errorf("nodes[2].Kind = %v, want PluginDirective", nodes[2].Kind)
	}

	assertRoundTrip(t, src, f)
}

func TestParseUnrecognizedLine(t *testing.T) {
	src := `some unrecognized text`
	f := Parse(src)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	if nodes[0].Kind != UnrecognizedLineNode {
		t.Errorf("node kind = %v, want UnrecognizedLineNode", nodes[0].Kind)
	}

	assertRoundTrip(t, src, f)
}

func TestParseOrgModeHeadingAsTrivia(t *testing.T) {
	src := `* this is org-mode text`
	f := Parse(src)

	// Heading is trivia — no directive nodes expected
	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 0 {
		t.Fatalf("got %d nodes, want 0 (heading should be trivia)", len(nodes))
	}

	assertRoundTrip(t, src, f)
}

func TestParseMixedWithUnrecognized(t *testing.T) {
	src := "; comment\noption \"title\" \"Test\"\n* org mode heading\ninclude \"other.beancount\"\n"
	f := Parse(src)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2 (comment and heading are trivia)", len(nodes))
	}

	// The comment becomes leading trivia on the "option" IDENT token,
	// and the heading becomes leading trivia on the "include" IDENT token.
	if nodes[0].Kind != OptionDirective {
		t.Errorf("nodes[0].Kind = %v, want OptionDirective", nodes[0].Kind)
	}
	if nodes[1].Kind != IncludeDirective {
		t.Errorf("nodes[1].Kind = %v, want IncludeDirective", nodes[1].Kind)
	}

	assertRoundTrip(t, src, f)
}

func TestParseOptionMissingValue(t *testing.T) {
	src := "option \"title\"\n"
	f := Parse(src)

	if len(f.Errors) == 0 {
		t.Fatal("expected at least one error")
	}

	// Should still produce an OptionDirective node.
	opt := f.Root.FindNode(OptionDirective)
	if opt == nil {
		t.Fatal("expected OptionDirective node even with error")
	}

	// Parsing should continue -- the newline before next line means
	// we don't consume extra tokens.
	assertRoundTrip(t, src, f)
}

func TestParseEmptyInput(t *testing.T) {
	src := ""
	f := Parse(src)

	assertNoErrors(t, f)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 0 {
		t.Fatalf("got %d nodes, want 0", len(nodes))
	}

	assertRoundTrip(t, src, f)
}

func TestParseOnlyComments(t *testing.T) {
	src := "; just a comment\n; another comment\n"
	f := Parse(src)

	assertNoErrors(t, f)

	// Comments are trivia attached to EOF, so no directive nodes.
	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 0 {
		t.Fatalf("got %d nodes, want 0", len(nodes))
	}

	assertRoundTrip(t, src, f)
}

func TestParsePluginConfigOnNextLine(t *testing.T) {
	// The config string is on the next line, so it should NOT be consumed
	// as part of the plugin directive.
	src := "plugin \"module\"\n\"config\"\n"
	f := Parse(src)

	plug := f.Root.FindNode(PluginDirective)
	if plug == nil {
		t.Fatal("expected PluginDirective node")
	}
	if len(plug.Children) != 2 {
		t.Fatalf("PluginDirective children = %d, want 2 (config on next line should not be consumed)", len(plug.Children))
	}

	assertRoundTrip(t, src, f)
}

func TestParseClose(t *testing.T) {
	src := `2024-01-01 close Assets:Old:Account`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(CloseDirective)
	if node == nil {
		t.Fatal("expected CloseDirective")
	}
	assertTokenChild(t, node.Children[0], DATE, "2024-01-01")
	assertTokenChild(t, node.Children[1], IDENT, "close")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Old:Account")
	assertRoundTrip(t, src, f)
}

func TestParseCommodity(t *testing.T) {
	src := `2024-01-01 commodity HOOL`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(CommodityDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected CommodityDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "commodity")
	assertTokenChild(t, node.Children[2], CURRENCY, "HOOL")
	assertRoundTrip(t, src, f)
}

func TestParseNote(t *testing.T) {
	src := `2024-01-01 note Assets:Bank:Checking "Opened account"`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(NoteDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected NoteDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "note")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Bank:Checking")
	assertTokenChild(t, node.Children[3], STRING, `"Opened account"`)
	assertRoundTrip(t, src, f)
}

func TestParseNoteMultilineString(t *testing.T) {
	src := "2024-01-01 note Assets:Bank:Checking \"Line one\nline two\""
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(NoteDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected NoteDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "note")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Bank:Checking")
	assertTokenChild(t, node.Children[3], STRING, "\"Line one\nline two\"")
	assertRoundTrip(t, src, f)
}

func TestParseDocument(t *testing.T) {
	src := `2024-01-01 document Assets:Bank:Checking "/path/to/statement.pdf"`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(DocumentDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected DocumentDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "document")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Bank:Checking")
	assertRoundTrip(t, src, f)
}

func TestParseEvent(t *testing.T) {
	src := `2024-01-01 event "location" "Paris"`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(EventDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected EventDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "event")
	assertTokenChild(t, node.Children[2], STRING, `"location"`)
	assertRoundTrip(t, src, f)
}

func TestParseQuery(t *testing.T) {
	src := `2024-01-01 query "net-worth" "SELECT account, sum(position)"`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(QueryDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected QueryDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "query")
	assertTokenChild(t, node.Children[2], STRING, `"net-worth"`)
	assertRoundTrip(t, src, f)
}

func TestParsePrice(t *testing.T) {
	src := `2024-07-09 price HOOL 579.18 USD`
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(PriceDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected PriceDirective node", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "price")
	assertTokenChild(t, node.Children[2], CURRENCY, "HOOL")
	// Check Amount sub-node
	amt := node.FindNode(AmountNode)
	if amt == nil {
		t.Fatal("expected AmountNode in PriceDirective")
	}
	assertRoundTrip(t, src, f)
}

func TestParseMultipleDatedDirectives(t *testing.T) {
	src := "2024-01-01 commodity USD\n2024-01-01 close Assets:Old\n2024-07-09 price HOOL 100.00 USD\n"
	f := Parse(src)
	assertNoErrors(t, f)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	if nodes[0].Kind != CommodityDirective {
		t.Errorf("nodes[0] = %v, want CommodityDirective", nodes[0].Kind)
	}
	if nodes[1].Kind != CloseDirective {
		t.Errorf("nodes[1] = %v, want CloseDirective", nodes[1].Kind)
	}
	if nodes[2].Kind != PriceDirective {
		t.Errorf("nodes[2] = %v, want PriceDirective", nodes[2].Kind)
	}
	assertRoundTrip(t, src, f)
}

func TestParseDatedWithTrailingComment(t *testing.T) {
	src := `2024-01-01 close Assets:Old ; closing old account`
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseMetadataSingleLine(t *testing.T) {
	src := "2024-01-01 commodity HOOL\n  name: \"Google Class A\"\n"
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(CommodityDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected CommodityDirective", src)
	}
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatal("expected MetadataLineNode")
	}
	assertTokenChild(t, meta.Children[0], IDENT, "name")
	assertTokenChild(t, meta.Children[1], COLON, ":")
	assertTokenChild(t, meta.Children[2], STRING, `"Google Class A"`)
	assertRoundTrip(t, src, f)
}

func TestParseMetadataMultipleLines(t *testing.T) {
	src := "2024-01-01 commodity HOOL\n  name: \"Google\"\n  asset-class: \"equity\"\n"
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(CommodityDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected CommodityDirective", src)
	}
	metas := node.FindAllNodes(MetadataLineNode)
	if len(metas) != 2 {
		t.Fatalf("got %d MetadataLineNodes, want 2", len(metas))
	}
	assertRoundTrip(t, src, f)
}

func TestParseMetadataValueTypes(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		valKind TokenKind
	}{
		{"string", "2024-01-01 close Assets:A\n  reason: \"closed\"\n", STRING},
		{"number", "2024-01-01 close Assets:A\n  seq: 42\n", NUMBER},
		{"date", "2024-01-01 close Assets:A\n  opened: 2020-01-01\n", DATE},
		{"account", "2024-01-01 close Assets:A\n  transfer: Assets:B\n", ACCOUNT},
		{"currency", "2024-01-01 close Assets:A\n  denomination: USD\n", CURRENCY},
		{"tag", "2024-01-01 close Assets:A\n  category: #savings\n", TAG},
		{"link", "2024-01-01 close Assets:A\n  ref: ^doc-123\n", LINK},
		{"bool-as-currency", "2024-01-01 close Assets:A\n  active: TRUE\n", CURRENCY}, // TRUE is lexed as CURRENCY (all-caps)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Parse(tt.src)
			assertNoErrors(t, f)
			node := f.Root.FindNode(CloseDirective)
			if node == nil {
				t.Fatalf("Parse(%q): expected CloseDirective", tt.src)
			}
			meta := node.FindNode(MetadataLineNode)
			if meta == nil {
				t.Fatal("expected MetadataLineNode")
			}
			if len(meta.Children) < 3 {
				t.Fatalf("MetadataLineNode has %d children, want >= 3", len(meta.Children))
			}
			if meta.Children[2].Token == nil || meta.Children[2].Token.Kind != tt.valKind {
				gotKind := ILLEGAL
				if meta.Children[2].Token != nil {
					gotKind = meta.Children[2].Token.Kind
				}
				t.Errorf("metadata value kind = %v, want %v", gotKind, tt.valKind)
			}
			assertRoundTrip(t, tt.src, f)
		})
	}
}

func TestParseMetadataOnPrice(t *testing.T) {
	src := "2024-01-01 price HOOL 100.00 USD\n  source: \"Yahoo Finance\"\n"
	f := Parse(src)
	assertNoErrors(t, f)

	node := f.Root.FindNode(PriceDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected PriceDirective", src)
	}
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatal("expected MetadataLineNode on PriceDirective")
	}
	assertRoundTrip(t, src, f)
}

func TestParseNoMetadataOnUnindented(t *testing.T) {
	// Next directive on column 0 should NOT be treated as metadata
	src := "2024-01-01 commodity HOOL\n2024-01-02 commodity USD\n"
	f := Parse(src)
	assertNoErrors(t, f)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
	for i, n := range nodes {
		if n.FindNode(MetadataLineNode) != nil {
			t.Errorf("nodes[%d] unexpectedly has metadata", i)
		}
	}
	assertRoundTrip(t, src, f)
}

func TestParseOpen(t *testing.T) {
	src := `2024-01-01 open Assets:Bank:Checking`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(OpenDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected OpenDirective", src)
	}
	assertTokenChild(t, node.Children[0], DATE, "2024-01-01")
	assertTokenChild(t, node.Children[1], IDENT, "open")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Bank:Checking")
	assertRoundTrip(t, src, f)
}

func TestParseOpenWithCurrencies(t *testing.T) {
	src := `2024-01-01 open Assets:Bank:Checking USD,EUR`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(OpenDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected OpenDirective", src)
	}
	// children: DATE, IDENT("open"), ACCOUNT, CURRENCY("USD"), COMMA, CURRENCY("EUR")
	if len(node.Children) != 6 {
		t.Fatalf("Parse(%q): OpenDirective children = %d, want 6", src, len(node.Children))
	}
	assertTokenChild(t, node.Children[3], CURRENCY, "USD")
	assertTokenChild(t, node.Children[4], COMMA, ",")
	assertTokenChild(t, node.Children[5], CURRENCY, "EUR")
	assertRoundTrip(t, src, f)
}

func TestParseOpenWithBooking(t *testing.T) {
	src := `2024-01-01 open Assets:Bank:Checking USD "STRICT"`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(OpenDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected OpenDirective", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseOpenWithCurrenciesAndBooking(t *testing.T) {
	src := `2024-01-01 open Assets:Bank:Checking USD,EUR "STRICT"`
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseOpenWithMetadata(t *testing.T) {
	src := "2024-01-01 open Assets:Bank:Checking USD\n  institution: \"Bank of America\"\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(OpenDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected OpenDirective", src)
	}
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatal("expected MetadataLineNode on OpenDirective")
	}
	assertRoundTrip(t, src, f)
}

func TestParseBalance(t *testing.T) {
	src := `2024-01-31 balance Assets:Bank:Checking 1000.00 USD`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(BalanceDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected BalanceDirective", src)
	}
	assertTokenChild(t, node.Children[0], DATE, "2024-01-31")
	assertTokenChild(t, node.Children[1], IDENT, "balance")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Bank:Checking")
	// Children[3] should be BalanceAmountNode
	if node.Children[3].Node == nil || node.Children[3].Node.Kind != BalanceAmountNode {
		t.Fatal("expected BalanceAmountNode as 4th child")
	}
	assertRoundTrip(t, src, f)
}

func TestParseBalanceWithTolerance(t *testing.T) {
	// Official Beancount example: currency appears once at the end and
	// applies to both the main amount and the tolerance number.
	src := `2013-09-20 balance Assets:Investing:Funds 319.020 ~ 0.002 RGAGX`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(BalanceDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected BalanceDirective", src)
	}
	body := node.FindNode(BalanceAmountNode)
	if body == nil {
		t.Fatalf("Parse(%q): expected BalanceAmountNode", src)
	}
	exprs := body.FindAllNodes(ArithExprNode)
	if len(exprs) != 2 {
		t.Fatalf("Parse(%q): got %d ArithExprNode children, want 2", src, len(exprs))
	}
	if body.FindToken(TILDE) == nil {
		t.Fatalf("Parse(%q): expected TILDE token in balance body", src)
	}
	cur := body.FindToken(CURRENCY)
	if cur == nil || cur.Raw != "RGAGX" {
		t.Fatalf("Parse(%q): expected trailing CURRENCY=RGAGX", src)
	}
	assertRoundTrip(t, src, f)
}

// TestParseBalanceRedundantCurrencyRejected ensures that the old
// non-standard syntax `Number Currency ~ Number Currency` is rejected as
// a hard parse error. The balance directive body still parses (so the
// balance node is present and we don't crash), but f.Errors must contain
// a diagnostic flagging the stray tokens after the currency.
func TestParseBalanceRedundantCurrencyRejected(t *testing.T) {
	src := `2024-01-31 balance Assets:Bank 1000.00 USD ~ 0.01 USD`
	f := Parse(src)
	bal := f.Root.FindNode(BalanceDirective)
	if bal == nil {
		t.Fatalf("Parse(%q): expected BalanceDirective", src)
	}
	body := bal.FindNode(BalanceAmountNode)
	if body == nil {
		t.Fatalf("Parse(%q): expected BalanceAmountNode", src)
	}
	// Canonical body form: exactly one ArithExprNode (no tolerance).
	// The stray tokens after the currency are captured as trailing
	// tokens on the body for trivia preservation, but they do not form
	// a tolerance branch.
	if len(body.FindAllNodes(ArithExprNode)) != 1 {
		t.Fatalf("Parse(%q): expected exactly one expression inside balance body", src)
	}
	// A hard parse error must be reported mentioning the stray tokens.
	if len(f.Errors) == 0 {
		t.Fatalf("Parse(%q): expected at least one error for stray tokens, got none", src)
	}
	foundStrayErr := false
	for _, e := range f.Errors {
		if strings.Contains(e.Msg, "after balance amount") {
			foundStrayErr = true
			break
		}
	}
	if !foundStrayErr {
		t.Fatalf("Parse(%q): expected error mentioning stray tokens after balance amount, got %v", src, f.Errors)
	}
}

func TestParsePad(t *testing.T) {
	src := `2024-01-01 pad Assets:Bank:Checking Equity:Opening-Balances`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(PadDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected PadDirective", src)
	}
	assertTokenChild(t, node.Children[0], DATE, "2024-01-01")
	assertTokenChild(t, node.Children[1], IDENT, "pad")
	assertTokenChild(t, node.Children[2], ACCOUNT, "Assets:Bank:Checking")
	assertTokenChild(t, node.Children[3], ACCOUNT, "Equity:Opening-Balances")
	assertRoundTrip(t, src, f)
}

func TestParsePadWithMetadata(t *testing.T) {
	src := "2024-01-01 pad Assets:Bank:Checking Equity:Opening-Balances\n  note: \"Initial balance\"\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(PadDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected PadDirective", src)
	}
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatalf("Parse(%q): expected MetadataLineNode on PadDirective", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseTransactionStar(t *testing.T) {
	src := `2024-01-15 * "Payee" "Narration"`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	assertTokenChild(t, node.Children[0], DATE, "2024-01-15")
	assertTokenChild(t, node.Children[1], STAR, "*")
	assertTokenChild(t, node.Children[2], STRING, `"Payee"`)
	assertTokenChild(t, node.Children[3], STRING, `"Narration"`)
	assertRoundTrip(t, src, f)
}

func TestParseTransactionBang(t *testing.T) {
	src := `2024-01-15 ! "Narration only"`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	assertTokenChild(t, node.Children[1], BANG, "!")
	assertTokenChild(t, node.Children[2], STRING, `"Narration only"`)
	assertRoundTrip(t, src, f)
}

func TestParseTransactionTxn(t *testing.T) {
	src := `2024-01-15 txn "Narration"`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	assertTokenChild(t, node.Children[1], IDENT, "txn")
	assertRoundTrip(t, src, f)
}

func TestParseTransactionNarrationOnly(t *testing.T) {
	src := `2024-01-15 * "Just narration"`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	// Should have: DATE, STAR, STRING (narration only, no payee)
	if len(node.Children) != 3 {
		t.Fatalf("Parse(%q): TransactionDirective children = %d, want 3", src, len(node.Children))
	}
	assertRoundTrip(t, src, f)
}

func TestParseTransactionWithTagsAndLinks(t *testing.T) {
	src := `2024-01-15 * "Payee" "Narration" #trip ^invoice-123`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	// DATE, STAR, STRING(payee), STRING(narration), TAG, LINK
	if len(node.Children) != 6 {
		t.Fatalf("Parse(%q): TransactionDirective children = %d, want 6", src, len(node.Children))
	}
	assertTokenChild(t, node.Children[4], TAG, "#trip")
	assertTokenChild(t, node.Children[5], LINK, "^invoice-123")
	assertRoundTrip(t, src, f)
}

func TestParseTransactionMultipleTags(t *testing.T) {
	src := `2024-01-15 * "Narration" #tag1 #tag2 #tag3`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	// DATE, STAR, STRING(narration), TAG, TAG, TAG
	if len(node.Children) != 6 {
		t.Fatalf("Parse(%q): TransactionDirective children = %d, want 6", src, len(node.Children))
	}
	assertTokenChild(t, node.Children[3], TAG, "#tag1")
	assertTokenChild(t, node.Children[4], TAG, "#tag2")
	assertTokenChild(t, node.Children[5], TAG, "#tag3")
	assertRoundTrip(t, src, f)
}

func TestParseTransactionNoStrings(t *testing.T) {
	// Transaction with just a flag, no payee or narration
	src := `2024-01-15 *`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	if len(node.Children) != 2 {
		t.Fatalf("Parse(%q): TransactionDirective children = %d, want 2", src, len(node.Children))
	}
	assertRoundTrip(t, src, f)
}

func TestParseTransactionWithMetadata(t *testing.T) {
	src := "2024-01-15 * \"Narration\"\n  note: \"some note\"\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatalf("Parse(%q): expected MetadataLineNode on TransactionDirective", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseTransactionWithTrailingComment(t *testing.T) {
	src := `2024-01-15 * "Payee" "Narration" ; comment`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseTransactionWithPostings(t *testing.T) {
	src := "2024-01-15 * \"Grocery Store\" \"Weekly groceries\"\n  Expenses:Food            50.00 USD\n  Assets:Bank:Checking   -50.00 USD\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	// First posting: Expenses:Food 50.00 USD
	assertTokenChild(t, postings[0].Children[0], ACCOUNT, "Expenses:Food")
	amt := postings[0].FindNode(AmountNode)
	if amt == nil {
		t.Fatalf("Parse(%q): expected AmountNode in first posting", src)
	}
	// Second posting: Assets:Bank:Checking -50.00 USD
	assertTokenChild(t, postings[1].Children[0], ACCOUNT, "Assets:Bank:Checking")
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithFlag(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  ! Expenses:Food  50.00 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	// First posting has BANG flag
	assertTokenChild(t, postings[0].Children[0], BANG, "!")
	assertTokenChild(t, postings[0].Children[1], ACCOUNT, "Expenses:Food")
	// Second posting has no amount (auto-balanced)
	if postings[1].FindNode(AmountNode) != nil {
		t.Fatalf("Parse(%q): second posting should have no amount", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithMetadata(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  Expenses:Food  50.00 USD\n    note: \"receipt\"\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	// Metadata on first posting
	meta := postings[0].FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatalf("Parse(%q): expected MetadataLineNode on first posting", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseTransactionMetadataBeforePostings(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  note: \"txn-level\"\n  Expenses:Food  50.00 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	// Transaction-level metadata (before any posting)
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatalf("Parse(%q): expected MetadataLineNode on transaction", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingAutoBalanced(t *testing.T) {
	src := "2024-01-15 *\n  Expenses:Food  50.00 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	// Second posting has no amount
	if postings[1].FindNode(AmountNode) != nil {
		t.Fatalf("Parse(%q): auto-balanced posting should have no amount", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingNegativeAmount(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Bank  -100.00 USD\n  Expenses:Food\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	amt := postings[0].FindNode(AmountNode)
	if amt == nil {
		t.Fatalf("Parse(%q): expected AmountNode in first posting", src)
	}
	// Verify ArithExprNode with unary minus is present in the amount
	expr := amt.FindNode(ArithExprNode)
	if expr == nil {
		t.Fatalf("Parse(%q): expected ArithExprNode in AmountNode", src)
	}
	// The unary minus expression should have a MINUS token
	if expr.FindToken(MINUS) == nil {
		t.Fatalf("Parse(%q): expected MINUS token in ArithExprNode", src)
	}
	assertRoundTrip(t, src, f)
}

// -- helpers --

func assertNoErrors(t *testing.T, f *File) {
	t.Helper()
	if len(f.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", f.Errors)
	}
}

func assertRoundTrip(t *testing.T, src string, f *File) {
	t.Helper()
	got := f.Root.FullText()
	if got != src {
		t.Errorf("round-trip mismatch:\n got: %q\nwant: %q", got, src)
	}
}

func assertTokenChild(t *testing.T, c Child, kind TokenKind, raw string) {
	t.Helper()
	if c.Token == nil {
		t.Fatalf("expected token child, got node child")
	}
	if c.Token.Kind != kind {
		t.Errorf("token kind = %v, want %v", c.Token.Kind, kind)
	}
	if c.Token.Raw != raw {
		t.Errorf("token raw = %q, want %q", c.Token.Raw, raw)
	}
}

func collectNodeChildren(n *Node) []*Node {
	var nodes []*Node
	for _, c := range n.Children {
		if c.Node != nil {
			nodes = append(nodes, c.Node)
		}
	}
	return nodes
}

func TestParsePostingWithCost(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {150.00 USD}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(TransactionDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	postings := node.FindAllNodes(PostingNode)
	if len(postings) != 2 {
		t.Fatalf("Parse(%q): got %d postings, want 2", src, len(postings))
	}
	cost := postings[0].FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode on first posting", src)
	}
	// Cost should contain: LBRACE, AmountNode, RBRACE
	assertTokenChild(t, cost.Children[0], LBRACE, "{")
	if len(cost.Children) < 3 {
		t.Fatalf("Parse(%q): CostSpec has %d children, want >= 3", src, len(cost.Children))
	}
	if cost.Children[1].Node == nil || cost.Children[1].Node.Kind != AmountNode {
		t.Fatalf("Parse(%q): expected AmountNode inside CostSpec", src)
	}
	assertTokenChild(t, cost.Children[2], RBRACE, "}")
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithCostAndDate(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {150.00 USD, 2024-01-01}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithCostDateAndLabel(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {150.00 USD, 2024-01-01, \"lot1\"}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithEmptyCost(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  -3 HOOL {}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	// Empty cost: just LBRACE and RBRACE
	assertTokenChild(t, cost.Children[0], LBRACE, "{")
	assertTokenChild(t, cost.Children[1], RBRACE, "}")
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithTotalCost(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  5 HOOL {{750.00 USD}}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	assertTokenChild(t, cost.Children[0], LBRACE2, "{{")
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithPricePerUnit(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  -3 HOOL {} @ 160.00 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	price := posting.FindNode(PriceAnnotNode)
	if price == nil {
		t.Fatalf("Parse(%q): expected PriceAnnotNode", src)
	}
	assertTokenChild(t, price.Children[0], AT, "@")
	amt := price.FindNode(AmountNode)
	if amt == nil {
		t.Fatal("expected AmountNode inside PriceAnnotNode")
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithTotalPrice(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  -2 HOOL {} @@ 310.00 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	price := posting.FindNode(PriceAnnotNode)
	if price == nil {
		t.Fatalf("Parse(%q): expected PriceAnnotNode", src)
	}
	assertTokenChild(t, price.Children[0], ATAT, "@@")
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithCostAndPrice(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {150.00 USD} @ 160.00 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	price := posting.FindNode(PriceAnnotNode)
	if price == nil {
		t.Fatalf("Parse(%q): expected PriceAnnotNode", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithCombinedCost(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {502.12 # 9.95 USD}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	amounts := cost.FindAllNodes(AmountNode)
	if len(amounts) != 2 {
		t.Fatalf("Parse(%q): cost has %d AmountNodes, want 2", src, len(amounts))
	}
	if cost.FindToken(HASH) == nil {
		t.Fatalf("Parse(%q): expected HASH token child in cost spec", src)
	}
	// Per-unit amount should have no CURRENCY token (currency inherited).
	if amounts[0].FindToken(CURRENCY) != nil {
		t.Errorf("Parse(%q): per-unit amount should have no currency token", src)
	}
	// Total amount should have a CURRENCY token.
	if amounts[1].FindToken(CURRENCY) == nil {
		t.Errorf("Parse(%q): total amount should have a currency token", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingWithCombinedCostExplicitPerUnitCurrency(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {502.12 USD # 9.95 USD}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	cost := posting.FindNode(CostSpecNode)
	if cost == nil {
		t.Fatalf("Parse(%q): expected CostSpecNode", src)
	}
	amounts := cost.FindAllNodes(AmountNode)
	if len(amounts) != 2 {
		t.Fatalf("Parse(%q): cost has %d AmountNodes, want 2", src, len(amounts))
	}
	if amounts[0].FindToken(CURRENCY) == nil {
		t.Errorf("Parse(%q): per-unit amount should have a currency token", src)
	}
	if amounts[1].FindToken(CURRENCY) == nil {
		t.Errorf("Parse(%q): total amount should have a currency token", src)
	}
	if cost.FindToken(HASH) == nil {
		t.Fatalf("Parse(%q): expected HASH token child in cost spec", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParsePostingCombinedCostInTotalBracesIsError(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {{502.12 # 9.95 USD}}\n  Assets:Cash\n"
	f := Parse(src)
	if len(f.Errors) == 0 {
		t.Fatalf("Parse(%q): expected parse error for '#' inside {{...}}", src)
	}
	if msg := f.Errors[0].Msg; !strings.Contains(msg, "'#'") || !strings.Contains(msg, "total") {
		t.Fatalf("Parse(%q): error message %q should mention '#' and total", src, msg)
	}
}

func TestParsePostingCombinedCostMissingPerUnitIsError(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {# 9.95 USD}\n  Assets:Cash\n"
	f := Parse(src)
	if len(f.Errors) == 0 {
		t.Fatalf("Parse(%q): expected parse error for missing per-unit amount", src)
	}
	if msg := f.Errors[0].Msg; !strings.Contains(msg, "expected") || !strings.Contains(msg, "amount") {
		t.Fatalf("Parse(%q): error message %q should contain 'expected' and 'amount'", src, msg)
	}
}

func TestParsePostingCombinedCostMissingTotalIsError(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {502.12 # USD}\n  Assets:Cash\n"
	f := Parse(src)
	if len(f.Errors) == 0 {
		t.Fatalf("Parse(%q): expected parse error for missing total amount", src)
	}
	if msg := f.Errors[0].Msg; !strings.Contains(msg, "total amount") {
		t.Fatalf("Parse(%q): error message %q should contain 'total amount'", src, msg)
	}
}

func TestParsePostingCombinedCostMissingTotalBeforeBraceIsError(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {502.12 #}\n  Assets:Cash\n"
	f := Parse(src)
	if len(f.Errors) != 1 {
		t.Fatalf("Parse(%q): expected exactly 1 parse error, got %d: %v", src, len(f.Errors), f.Errors)
	}
	if msg := f.Errors[0].Msg; !strings.Contains(msg, "total amount") {
		t.Fatalf("Parse(%q): error message %q should contain 'total amount'", src, msg)
	}
}

func TestParsePostingPriceWithoutCost(t *testing.T) {
	// Price annotation without cost spec
	src := "2024-01-15 *\n  Assets:Foreign  100.00 EUR @ 1.09 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	posting := f.Root.FindNode(TransactionDirective).FindAllNodes(PostingNode)[0]
	if posting.FindNode(CostSpecNode) != nil {
		t.Fatalf("Parse(%q): should not have CostSpecNode", src)
	}
	price := posting.FindNode(PriceAnnotNode)
	if price == nil {
		t.Fatalf("Parse(%q): expected PriceAnnotNode", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseAmountSimpleNumber(t *testing.T) {
	// Simple number should still work (backward compat)
	src := "2024-01-15 *\n  Expenses:Food  100 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	txn := f.Root.FindNode(TransactionDirective)
	if txn == nil {
		t.Fatalf("Parse(%q): expected TransactionDirective", src)
	}
	posting := txn.FindAllNodes(PostingNode)[0]
	amt := posting.FindNode(AmountNode)
	if amt == nil {
		t.Fatalf("Parse(%q): expected AmountNode", src)
	}
	// AmountNode should have: ArithExprNode(NUMBER), CURRENCY
	expr := amt.FindNode(ArithExprNode)
	if expr == nil {
		t.Fatalf("Parse(%q): expected ArithExprNode inside AmountNode", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParseAmountAddition(t *testing.T) {
	src := "2024-01-15 *\n  Expenses:Food  10 + 20 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountMultiplication(t *testing.T) {
	src := "2024-01-15 *\n  Expenses:Food  100 * 2 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountParenthesized(t *testing.T) {
	src := "2024-01-15 *\n  Expenses:Food  (10 + 20) USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountComplex(t *testing.T) {
	src := "2024-01-15 *\n  Expenses:Food  (100 + 50) * 2 USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountNested(t *testing.T) {
	src := "2024-01-15 *\n  Expenses:Food  ((40.00 / 3) + 5) USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountUnaryMinus(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Bank  -100.00 USD\n  Expenses:Food\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountInCostExpr(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Investments  10 HOOL {(100 / 3) USD}\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountInPriceExpr(t *testing.T) {
	src := "2024-01-15 *\n  Assets:Foreign  100 EUR @ (1.09 + 0.01) USD\n  Assets:Cash\n"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountInBalanceDirective(t *testing.T) {
	src := "2024-01-31 balance Assets:Bank:Checking 1000.00 USD"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseAmountInPriceDirective(t *testing.T) {
	src := "2024-07-09 price HOOL 579.18 USD"
	f := Parse(src)
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

func TestParseReader(t *testing.T) {
	src := "option \"title\" \"x\"\n"
	f, err := ParseReader(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseReader returned error: %v", err)
	}
	assertNoErrors(t, f)
	assertRoundTrip(t, src, f)
}

// failingReader returns its configured error on the first Read call.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestParseReaderError(t *testing.T) {
	want := errors.New("boom")
	f, err := ParseReader(failingReader{err: want})
	if f != nil {
		t.Errorf("ParseReader(failingReader{err: %v}) returned non-nil File, want nil", want)
	}
	if !errors.Is(err, want) {
		t.Errorf("ParseReader(failingReader{err: %v}) error = %v, want %v", want, err, want)
	}
}

func TestParseFile(t *testing.T) {
	src := "2024-01-01 open Assets:Bank\n"
	path := filepath.Join(t.TempDir(), "a.beancount")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(%q): %v", path, err)
	}
	assertNoErrors(t, f)
	if f.Root.FindNode(OpenDirective) == nil {
		t.Errorf("ParseFile(%q): expected OpenDirective node in result", path)
	}
	assertRoundTrip(t, src, f)
}

func TestParseFileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.beancount")
	f, err := ParseFile(path)
	if f != nil {
		t.Errorf("ParseFile(%q) returned non-nil File, want nil", path)
	}
	if err == nil {
		t.Errorf("ParseFile(%q) returned nil error, want non-nil", path)
	}
}
