package syntax

import "testing"

// helper to make a token with position and optional trivia.
func tok(kind TokenKind, pos int, raw string) *Token {
	return &Token{Kind: kind, Pos: pos, Raw: raw}
}

func tokWithTrivia(kind TokenKind, pos int, raw string, leading, trailing []Trivia) *Token {
	return &Token{Kind: kind, Pos: pos, Raw: raw, LeadingTrivia: leading, TrailingTrivia: trailing}
}

func TestAddTokenAndAddNode(t *testing.T) {
	n := &Node{Kind: FileNode}
	n.AddToken(tok(IDENT, 0, "option"))
	n.AddNode(&Node{Kind: OptionDirective})

	if len(n.Children) != 2 {
		t.Fatalf("got %d children, want 2", len(n.Children))
	}
	if n.Children[0].Token == nil {
		t.Error("first child should be a token")
	}
	if n.Children[1].Node == nil {
		t.Error("second child should be a node")
	}
}

func TestFindToken(t *testing.T) {
	n := &Node{Kind: OptionDirective}
	n.AddToken(tok(IDENT, 0, "option"))
	n.AddToken(tok(STRING, 7, `"title"`))
	n.AddToken(tok(STRING, 14, `"My Ledger"`))

	got := n.FindToken(STRING)
	if got == nil {
		t.Fatal("FindToken returned nil")
	}
	if got.Raw != `"title"` {
		t.Errorf("FindToken(STRING).Raw = %q, want %q", got.Raw, `"title"`)
	}

	if n.FindToken(NUMBER) != nil {
		t.Error("FindToken(NUMBER) should return nil")
	}
}

func TestFindNode(t *testing.T) {
	file := &Node{Kind: FileNode}
	opt := &Node{Kind: OptionDirective}
	file.AddNode(opt)
	file.AddNode(&Node{Kind: IncludeDirective})

	got := file.FindNode(OptionDirective)
	if got != opt {
		t.Error("FindNode did not return the expected node")
	}
	if file.FindNode(TransactionDirective) != nil {
		t.Error("FindNode should return nil for missing kind")
	}
}

func TestFindAllNodes(t *testing.T) {
	file := &Node{Kind: FileNode}
	file.AddNode(&Node{Kind: OptionDirective})
	file.AddNode(&Node{Kind: IncludeDirective})
	file.AddNode(&Node{Kind: OptionDirective})

	opts := file.FindAllNodes(OptionDirective)
	if len(opts) != 2 {
		t.Fatalf("FindAllNodes(OptionDirective) = %d nodes, want 2", len(opts))
	}

	empty := file.FindAllNodes(TransactionDirective)
	if len(empty) != 0 {
		t.Errorf("FindAllNodes(TransactionDirective) = %d nodes, want 0", len(empty))
	}
}

func TestTokens_DepthFirst(t *testing.T) {
	// Build:  FileNode -> OptionDirective -> [IDENT, STRING, STRING]
	opt := &Node{Kind: OptionDirective}
	t1 := tok(IDENT, 0, "option")
	t2 := tok(STRING, 7, `"title"`)
	t3 := tok(STRING, 15, `"My Ledger"`)
	opt.AddToken(t1)
	opt.AddToken(t2)
	opt.AddToken(t3)

	file := &Node{Kind: FileNode}
	file.AddNode(opt)

	var got []*Token
	for tk := range file.Tokens() {
		got = append(got, tk)
	}

	if len(got) != 3 {
		t.Fatalf("Tokens() yielded %d tokens, want 3", len(got))
	}
	want := []*Token{t1, t2, t3}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("token %d: got %v, want %v", i, got[i], w)
		}
	}
}

func TestTokens_MixedChildren(t *testing.T) {
	// FileNode has a direct token (e.g. EOF) after a sub-node
	opt := &Node{Kind: OptionDirective}
	t1 := tok(IDENT, 0, "option")
	opt.AddToken(t1)

	eof := tok(EOF, 10, "")
	file := &Node{Kind: FileNode}
	file.AddNode(opt)
	file.AddToken(eof)

	var got []*Token
	for tk := range file.Tokens() {
		got = append(got, tk)
	}
	if len(got) != 2 {
		t.Fatalf("Tokens() yielded %d, want 2", len(got))
	}
	if got[0] != t1 || got[1] != eof {
		t.Error("tokens not in expected order")
	}
}

func TestFullText_SimpleRoundTrip(t *testing.T) {
	opt := &Node{Kind: OptionDirective}
	opt.AddToken(tok(IDENT, 0, "option"))
	opt.AddToken(tokWithTrivia(STRING, 7, `"title"`,
		[]Trivia{{Kind: WhitespaceTrivia, Raw: " "}}, nil))
	opt.AddToken(tokWithTrivia(STRING, 15, `"My Ledger"`,
		[]Trivia{{Kind: WhitespaceTrivia, Raw: " "}},
		[]Trivia{{Kind: NewlineTrivia, Raw: "\n"}}))

	want := `option "title" "My Ledger"` + "\n"
	got := opt.FullText()
	if got != want {
		t.Errorf("FullText() = %q, want %q", got, want)
	}
}

func TestFullText_WithComment(t *testing.T) {
	opt := &Node{Kind: OptionDirective}
	opt.AddToken(tok(IDENT, 0, "option"))
	opt.AddToken(tokWithTrivia(STRING, 7, `"title"`,
		[]Trivia{{Kind: WhitespaceTrivia, Raw: " "}}, nil))
	opt.AddToken(tokWithTrivia(STRING, 15, `"Test"`,
		[]Trivia{{Kind: WhitespaceTrivia, Raw: " "}},
		[]Trivia{
			{Kind: WhitespaceTrivia, Raw: " "},
			{Kind: CommentTrivia, Raw: "; a comment"},
			{Kind: NewlineTrivia, Raw: "\n"},
		}))

	want := `option "title" "Test" ; a comment` + "\n"
	got := opt.FullText()
	if got != want {
		t.Errorf("FullText() = %q, want %q", got, want)
	}
}

func TestFile_FullText(t *testing.T) {
	root := &Node{Kind: FileNode}
	root.AddToken(tok(IDENT, 0, "option"))

	f := &File{Root: root}
	if got := f.FullText(); got != "option" {
		t.Errorf("File.FullText() = %q, want %q", got, "option")
	}
}

func TestFile_FullText_NilRoot(t *testing.T) {
	f := &File{}
	if got := f.FullText(); got != "" {
		t.Errorf("File.FullText() with nil root = %q, want empty", got)
	}
}

func TestError_Error(t *testing.T) {
	e := &Error{Pos: 42, Msg: "unexpected token"}
	want := "offset 42: unexpected token"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
