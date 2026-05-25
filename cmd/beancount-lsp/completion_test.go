package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/session"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// newCompletionServer creates a server+client pair backed by a real session
// loading rootFile.
func newCompletionServer(t *testing.T, rootFile string) *lspClient {
	t.Helper()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) {
		return session.New(rootFile)
	}))
	client, _, _ := newDiagTestPair(t, srv)
	ctx := context.Background()
	if err := client.call(ctx, "initialize", initializeParams(uri.File(rootFile)), nil); err != nil {
		t.Fatalf("handleCompletion: initialize: %v", err)
	}
	return client
}

// callCompletion sends textDocument/completion and returns the CompletionList.
func callCompletion(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32) *protocol.CompletionList {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/completion", &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
			Position:     protocol.Position{Line: line, Character: char},
		},
	}, &raw); err != nil {
		t.Fatalf("handleCompletion: call error: %v", err)
	}
	var list protocol.CompletionList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("handleCompletion: unmarshal: %v", err)
	}
	return &list
}

// awaitCompletion polls until at least wantCount items appear or 3 s elapses.
func awaitCompletion(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32, wantCount int) *protocol.CompletionList {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		list := callCompletion(t, client, docURI, line, char)
		if len(list.Items) >= wantCount || time.Now().After(deadline) {
			return list
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func labelSet(items []protocol.CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	slices.Sort(out)
	return out
}

func containsLabel(items []protocol.CompletionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

// TestCompletion_Account_OnPosting: two Open directives; cursor on partial
// account in a posting returns both accounts.
func TestCompletion_Account_OnPosting(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Assets:Bank
  Expenses:Food
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := awaitCompletion(t, client, docURI, 3, 8, 2)

	want := []string{"Assets:Bank", "Expenses:Food"}
	got := labelSet(list.Items)
	if !slices.Equal(got, want) {
		t.Errorf("handleCompletion: Account_OnPosting: labels = %v, want %v", got, want)
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindClass {
			t.Errorf("handleCompletion: Account_OnPosting: item %q kind = %v, want Class", it.Label, it.Kind)
		}
	}
}

// TestCompletion_Currency_AfterAmount: Commodity USD and EUR; cursor after
// amount in a posting returns both currencies.
func TestCompletion_Currency_AfterAmount(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-01 commodity EUR
2024-01-01 open Assets:Bank USD
2024-01-15 * "Transfer"
  Assets:Bank  100 US
  Assets:Bank  -100 EUR
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := awaitCompletion(t, client, docURI, 4, 21, 2)

	wantCurr := []string{"EUR", "USD"}
	gotCurr := labelSet(list.Items)
	if !slices.Equal(gotCurr, wantCurr) {
		t.Errorf("handleCompletion: Currency_AfterAmount: labels = %v, want %v", gotCurr, wantCurr)
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindConstant {
			t.Errorf("handleCompletion: Currency_AfterAmount: item %q kind = %v, want Constant", it.Label, it.Kind)
		}
	}
}

// TestCompletion_Keyword_TopLevel: empty line, cursor at start → returns
// top-level directive keywords (option, plugin, etc.).
func TestCompletion_Keyword_TopLevel(t *testing.T) {
	dir := t.TempDir()
	const src = "\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := awaitCompletion(t, client, docURI, 0, 0, 4)

	for _, want := range []string{"option", "plugin", "include", "pushtag", "poptag"} {
		if !containsLabel(list.Items, want) {
			t.Errorf("handleCompletion: Keyword_TopLevel: missing %q; got %v", want, labelSet(list.Items))
		}
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindKeyword {
			t.Errorf("handleCompletion: Keyword_TopLevel: item %q kind = %v, want Keyword", it.Label, it.Kind)
		}
	}
}

// TestCompletion_Keyword_AfterDate: line "2024-01-01 " cursor at end → returns
// dated-directive keywords (open, close, commodity, etc.).
func TestCompletion_Keyword_AfterDate(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 \n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := awaitCompletion(t, client, docURI, 0, 11, 5)

	for _, want := range []string{"open", "close", "commodity", "balance", "txn"} {
		if !containsLabel(list.Items, want) {
			t.Errorf("handleCompletion: Keyword_AfterDate: missing %q; got %v", want, labelSet(list.Items))
		}
	}
	if containsLabel(list.Items, "option") {
		t.Errorf("handleCompletion: Keyword_AfterDate: unexpected 'option' in dated-directive list")
	}
}

// TestCompletion_Flag: line "2024-01-01 *" → flag context returns "*" and "!".
func TestCompletion_Flag(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 *\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := awaitCompletion(t, client, docURI, 0, 12, 2)

	wantFlags := []string{"!", "*"}
	gotFlags := labelSet(list.Items)
	if !slices.Equal(gotFlags, wantFlags) {
		t.Errorf("handleCompletion: Flag: labels = %v, want %v", gotFlags, wantFlags)
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindValue {
			t.Errorf("handleCompletion: Flag: item %q kind = %v, want Value", it.Label, it.Kind)
		}
	}
}

// TestCompletion_Tag: file with #foo and #bar tags; cursor on "#b" at line
// start (tag heuristic) returns "foo" and "bar".
func TestCompletion_Tag(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test" #foo #bar
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Push overlay with a line starting with "#b" so classifyContext → ContextTag.
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test" #foo #bar
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
#b
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("handleCompletion: Tag: didOpen: %v", err)
	}

	list := awaitCompletion(t, client, docURI, 5, 2, 2)

	if !containsLabel(list.Items, "foo") {
		t.Errorf("handleCompletion: Tag: missing 'foo'; got %v", labelSet(list.Items))
	}
	if !containsLabel(list.Items, "bar") {
		t.Errorf("handleCompletion: Tag: missing 'bar'; got %v", labelSet(list.Items))
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindEnum {
			t.Errorf("handleCompletion: Tag: item %q kind = %v, want Enum", it.Label, it.Kind)
		}
	}
}

// TestCompletion_Link: file with ^myLink; cursor on "^my" at line start
// (link heuristic) returns "myLink".
func TestCompletion_Link(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test" ^myLink
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Push overlay with a line starting with "^my" so classifyContext → ContextLink.
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test" ^myLink
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
^my
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("handleCompletion: Link: didOpen: %v", err)
	}

	list := awaitCompletion(t, client, docURI, 5, 3, 1)

	if !containsLabel(list.Items, "myLink") {
		t.Errorf("handleCompletion: Link: missing 'myLink'; got %v", labelSet(list.Items))
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindReference {
			t.Errorf("handleCompletion: Link: item %q kind = %v, want Reference", it.Label, it.Kind)
		}
	}
}

// TestCompletion_InString: cursor inside a "..." string → empty list.
func TestCompletion_InString(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-15 * "Dinner
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := callCompletion(t, client, docURI, 0, 19)

	if len(list.Items) != 0 {
		t.Errorf("handleCompletion: InString: got %d items, want 0: %v", len(list.Items), labelSet(list.Items))
	}
}

// TestCompletion_EmptyLedger: no Open/Commodity directives, cursor in posting
// account context → empty item list.
func TestCompletion_EmptyLedger(t *testing.T) {
	dir := t.TempDir()
	const src = "  Assets:Bank\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := callCompletion(t, client, docURI, 0, 13)

	if len(list.Items) != 0 {
		t.Errorf("handleCompletion: EmptyLedger: got %d items, want 0: %v", len(list.Items), labelSet(list.Items))
	}
}
