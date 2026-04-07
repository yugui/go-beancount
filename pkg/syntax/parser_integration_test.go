package syntax

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParserRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/*.beancount")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no testdata files found")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(data)
			f := Parse(src)
			got := f.Root.FullText()
			if got != src {
				t.Errorf("round-trip failed for %s: got length %d, want length %d", file, len(got), len(src))
				for i := 0; i < len(src) && i < len(got); i++ {
					if src[i] != got[i] {
						start := max(0, i-30)
						end := min(len(src), i+30)
						t.Errorf("first diff at byte %d:\n  want: %q\n  got:  %q", i, src[start:end], got[start:min(end, len(got))])
						break
					}
				}
			}
		})
	}
}

func TestParserComprehensive(t *testing.T) {
	data, err := os.ReadFile("testdata/comprehensive.beancount")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	f := Parse(src)

	// Should parse without errors
	if len(f.Errors) != 0 {
		for _, e := range f.Errors {
			t.Logf("error at pos %d: %s", e.Pos, e.Msg)
		}
		t.Errorf("unexpected errors: %d", len(f.Errors))
	}

	// Verify we get a good variety of node types
	nodeKinds := make(map[NodeKind]int)
	for _, c := range f.Root.Children {
		if c.Node != nil {
			nodeKinds[c.Node.Kind]++
		}
	}

	expected := map[NodeKind]string{
		OptionDirective:      "OptionDirective",
		PluginDirective:      "PluginDirective",
		IncludeDirective:     "IncludeDirective",
		PushtagDirective:     "PushtagDirective",
		PoptagDirective:      "PoptagDirective",
		OpenDirective:        "OpenDirective",
		CloseDirective:       "CloseDirective",
		CommodityDirective:   "CommodityDirective",
		TransactionDirective: "TransactionDirective",
		BalanceDirective:     "BalanceDirective",
		PadDirective:         "PadDirective",
		NoteDirective:        "NoteDirective",
		DocumentDirective:    "DocumentDirective",
		PriceDirective:       "PriceDirective",
		EventDirective:       "EventDirective",
		QueryDirective:       "QueryDirective",
		CustomDirective:      "CustomDirective",
	}

	for kind, name := range expected {
		if nodeKinds[kind] == 0 {
			t.Errorf("expected at least one %s in comprehensive.beancount", name)
		}
	}
}

func TestParserErrorRecovery(t *testing.T) {
	data, err := os.ReadFile("testdata/errors.beancount")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	f := Parse(src)

	// Should have some errors
	if len(f.Errors) == 0 {
		t.Error("expected parse errors in errors.beancount")
	}

	// Should still produce a valid tree with recoverable directives
	if f.Root.FindNode(OpenDirective) == nil {
		t.Error("expected OpenDirective to survive error recovery")
	}
	if f.Root.FindNode(CommodityDirective) == nil {
		t.Error("expected CommodityDirective to survive error recovery")
	}
	if f.Root.FindNode(BalanceDirective) == nil {
		t.Error("expected BalanceDirective to survive error recovery")
	}

	// Round-trip should still work (errors preserved in tree)
	got := f.Root.FullText()
	if got != src {
		t.Errorf("round-trip failed for errors.beancount: got length %d, want length %d", len(got), len(src))
	}
}

func TestParserCustomDirective(t *testing.T) {
	src := `2024-01-01 custom "budget" Expenses:Food 500.00 USD`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(CustomDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected CustomDirective", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParserCustomDirectiveMultipleValues(t *testing.T) {
	src := `2024-01-01 custom "flag" "test-value" TRUE 2024-06-15`
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(CustomDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected CustomDirective", src)
	}
	assertRoundTrip(t, src, f)
}

func TestParserCustomDirectiveWithMetadata(t *testing.T) {
	src := "2024-01-01 custom \"budget\" Expenses:Food 500.00 USD\n  note: \"monthly budget\"\n"
	f := Parse(src)
	assertNoErrors(t, f)
	node := f.Root.FindNode(CustomDirective)
	if node == nil {
		t.Fatalf("Parse(%q): expected CustomDirective", src)
	}
	meta := node.FindNode(MetadataLineNode)
	if meta == nil {
		t.Fatalf("Parse(%q): expected MetadataLineNode on CustomDirective", src)
	}
	assertRoundTrip(t, src, f)
}
