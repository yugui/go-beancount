package syntax

import (
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
	src := `* this is org-mode text`
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

func TestParseMixedWithUnrecognized(t *testing.T) {
	src := "; comment\noption \"title\" \"Test\"\n* org mode heading\ninclude \"other.beancount\"\n"
	f := Parse(src)

	nodes := collectNodeChildren(f.Root)
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3 (comment is trivia on option)", len(nodes))
	}

	// The comment becomes leading trivia on the "option" IDENT token,
	// so the first node is the OptionDirective.
	if nodes[0].Kind != OptionDirective {
		t.Errorf("nodes[0].Kind = %v, want OptionDirective", nodes[0].Kind)
	}
	if nodes[1].Kind != UnrecognizedLineNode {
		t.Errorf("nodes[1].Kind = %v, want UnrecognizedLineNode", nodes[1].Kind)
	}
	if nodes[2].Kind != IncludeDirective {
		t.Errorf("nodes[2].Kind = %v, want IncludeDirective", nodes[2].Kind)
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
