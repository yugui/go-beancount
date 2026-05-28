package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// orderedLabels returns item labels in their original (ranked) order.
func orderedLabels(items []protocol.CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
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

// TestCompletion_Account_TextEdit: an account token already typed up to a colon
// (`Income:`) yields items whose TextEdit replaces the whole token, so a
// colon-splitting client neither duplicates the prefix nor matches all accounts.
func TestCompletion_Account_TextEdit(t *testing.T) {
	dir := t.TempDir()
	const src = `2020-01-01 open Assets:A
2020-01-01 open Assets:A:B
2020-01-01 open Income:A
2020-01-01 open Equity:Opening-Balances
2020-01-04 * "test" "test"
  Income:
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 5, char 9: cursor right after "  Income:".
	list := awaitCompletion(t, client, docURI, 5, 9, 4)

	wantRange := protocol.Range{
		Start: protocol.Position{Line: 5, Character: 2}, // start of "Income"
		End:   protocol.Position{Line: 5, Character: 9}, // cursor
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindClass {
			t.Errorf("Account_TextEdit: item %q kind = %v, want Class", it.Label, it.Kind)
		}
		if it.TextEdit == nil {
			t.Errorf("Account_TextEdit: item %q has nil TextEdit", it.Label)
			continue
		}
		if it.TextEdit.NewText != it.Label {
			t.Errorf("Account_TextEdit: item %q TextEdit.NewText = %q, want label", it.Label, it.TextEdit.NewText)
		}
		if it.TextEdit.Range != wantRange {
			t.Errorf("Account_TextEdit: item %q TextEdit.Range = %+v, want %+v", it.Label, it.TextEdit.Range, wantRange)
		}
		if it.FilterText != it.Label {
			t.Errorf("Account_TextEdit: item %q FilterText = %q, want label", it.Label, it.FilterText)
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

	for _, want := range []string{"open", "close", "commodity", "balance", "txn", "*", "!"} {
		if !containsLabel(list.Items, want) {
			t.Errorf("handleCompletion: Keyword_AfterDate: missing %q; got %v", want, labelSet(list.Items))
		}
	}
	if containsLabel(list.Items, "option") {
		t.Errorf("handleCompletion: Keyword_AfterDate: unexpected 'option' in dated-directive list")
	}
}

// TestCompletion_PartialKeyword_AfterDate covers the editor's word-boundary
// auto-trigger path: the user has typed "o" after the date and the client
// fires completion. The server returns the full date-first directive list so
// the client can prefix-filter to "open".
func TestCompletion_PartialKeyword_AfterDate(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 o\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	list := awaitCompletion(t, client, docURI, 0, 12, 5)

	for _, want := range []string{"open", "close", "commodity", "txn"} {
		if !containsLabel(list.Items, want) {
			t.Errorf("handleCompletion: PartialKeyword_AfterDate: missing %q; got %v", want, labelSet(list.Items))
		}
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

// TestCompletion_InString: cursor inside a string literal that is NOT a
// transaction header → no completions, even when payees exist in the ledger.
func TestCompletion_InString(t *testing.T) {
	dir := t.TempDir()
	// The ledger contains a transaction with a payee so candidates would exist
	// if the context were ContextPayee; the option directive line is NOT a txn
	// header, so classifyContext must produce ContextInString (odd quote count).
	const src = `2024-01-15 * "Acme" "Dinner"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
option "title" "Foo bar
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 3 is `option "title" "Foo bar` — cursor inside second string.
	list := callCompletion(t, client, docURI, 3, 23)

	if len(list.Items) != 0 {
		t.Errorf("handleCompletion: InString: got %d items, want 0: %v", len(list.Items), labelSet(list.Items))
	}
}

// TestCompletion_FirstString_Ambiguous: cursor in the first quoted string with
// no second string yet. The value may become payee or narration, so both
// candidate sets are offered, each tagged in Detail.
func TestCompletion_FirstString_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Acme" "Lunch"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Beta" "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// New header line: cursor in first string, no second string — ambiguous.
	editedSrc := src + "2024-02-01 * \"\n"
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_FirstString_Ambiguous: didOpen: %v", err)
	}

	// Line 8 (0-indexed), char 14 — cursor after the opening quote.
	list := awaitCompletion(t, client, docURI, 8, 14, 4)

	for _, want := range []string{"Acme", "Beta", "Lunch", "Dinner"} {
		if !containsLabel(list.Items, want) {
			t.Errorf("TestCompletion_FirstString_Ambiguous: missing %q; got %v", want, labelSet(list.Items))
		}
	}
	detail := map[string]string{}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindValue {
			t.Errorf("TestCompletion_FirstString_Ambiguous: item %q kind = %v, want Value", it.Label, it.Kind)
		}
		if it.InsertText != it.Label {
			t.Errorf("TestCompletion_FirstString_Ambiguous: item %q InsertText = %q, want bare label", it.Label, it.InsertText)
		}
		detail[it.Label] = it.Detail
	}
	if detail["Acme"] != "payee" {
		t.Errorf("TestCompletion_FirstString_Ambiguous: Acme Detail = %q, want \"payee\"", detail["Acme"])
	}
	if detail["Lunch"] != "narration" {
		t.Errorf("TestCompletion_FirstString_Ambiguous: Lunch Detail = %q, want \"narration\"", detail["Lunch"])
	}
}

// TestCompletion_Payee: cursor in the first string with a second string already
// present makes the first one unambiguously the payee; only payees are offered.
func TestCompletion_Payee(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Acme" "Lunch"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Beta" "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Empty first string, narration already present after it.
	editedSrc := src + "2024-02-01 * \"\" \"x\"\n"
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_Payee: didOpen: %v", err)
	}

	// Line 8 (0-indexed), char 14 — cursor inside the empty first string.
	list := awaitCompletion(t, client, docURI, 8, 14, 2)

	got := labelSet(list.Items)
	want := []string{"Acme", "Beta"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_Payee: labels = %v, want %v", got, want)
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindValue {
			t.Errorf("TestCompletion_Payee: item %q kind = %v, want Value", it.Label, it.Kind)
		}
		if it.InsertText != it.Label {
			t.Errorf("TestCompletion_Payee: item %q InsertText = %q, want bare label", it.Label, it.InsertText)
		}
	}
}

// TestCompletion_Narration_All: ledger with narrations "Lunch" and "Dinner";
// cursor at second quoted string of a transaction header returns both narrations.
func TestCompletion_Narration_All(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Acme" "Lunch"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Beta" "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Push overlay with a new transaction header line: cursor in second string.
	editedSrc := src + "2024-02-01 * \"Foo\" \"\n"
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_Narration_All: didOpen: %v", err)
	}

	// Line 8, char 20 — cursor after the opening quote of the second string.
	list := awaitCompletion(t, client, docURI, 8, 20, 2)

	if !containsLabel(list.Items, "Lunch") {
		t.Errorf("TestCompletion_Narration_All: missing 'Lunch'; got %v", labelSet(list.Items))
	}
	if !containsLabel(list.Items, "Dinner") {
		t.Errorf("TestCompletion_Narration_All: missing 'Dinner'; got %v", labelSet(list.Items))
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindValue {
			t.Errorf("TestCompletion_Narration_All: item %q kind = %v, want Value", it.Label, it.Kind)
		}
		if it.InsertText != it.Label {
			t.Errorf("TestCompletion_Narration_All: item %q InsertText = %q, want bare label", it.Label, it.InsertText)
		}
	}
}

// TestCompletion_Payee_DeduplicatedAndSorted: multiple transactions sharing the
// same payee appear only once and results are sorted.
func TestCompletion_Payee_DeduplicatedAndSorted(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Zebra" "Lunch"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Acme" "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
2024-01-17 * "Acme" "Coffee"
  Assets:Bank  -5 USD
  Expenses:Food  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Payee-only context (second string present); Acme occurs twice, Zebra once.
	editedSrc := src + "2024-02-01 * \"\" \"x\"\n"
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_Payee_DeduplicatedAndSorted: didOpen: %v", err)
	}

	// Line 11 (0-indexed), char 14 — cursor inside the empty first string.
	list := awaitCompletion(t, client, docURI, 11, 14, 2)

	// Deduplicated and ordered by frequency-descending: Acme (2) before Zebra (1).
	got := orderedLabels(list.Items)
	want := []string{"Acme", "Zebra"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_Payee_DeduplicatedAndSorted: labels = %v, want %v", got, want)
	}
}

// TestCompletion_Narration_PayeeMatchRanked: narrations used with the payee
// already on the line rank above other same-file narrations.
func TestCompletion_Narration_PayeeMatchRanked(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Acme" "Lunch"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Beta" "Coffee run"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
2024-01-17 * "Acme" "Dinner"
  Assets:Bank  -5 USD
  Expenses:Food  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// New header with payee "Acme"; cursor inside an empty (closed) narration
	// string so the in-progress narration is not itself a candidate.
	editedSrc := src + "2024-02-01 * \"Acme\" \"\"\n"
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_Narration_PayeeMatchRanked: didOpen: %v", err)
	}

	// Line 11 (0-indexed), char 21 — cursor between the empty narration quotes.
	list := awaitCompletion(t, client, docURI, 11, 21, 3)

	got := orderedLabels(list.Items)
	// Group 0 (used with Acme): Dinner, Lunch; group 1 (same file): "Coffee run".
	want := []string{"Dinner", "Lunch", "Coffee run"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_Narration_PayeeMatchRanked: labels = %v, want %v", got, want)
	}
}

// TestCompletion_Payee_SameFileRankedFirst: payees from the current file rank
// above payees from a sibling file in the same directory.
func TestCompletion_Payee_SameFileRankedFirst(t *testing.T) {
	dir := t.TempDir()
	const main = `include "sibling.beancount"
2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Local" "n1"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-02-01 * "" "edit"
  Assets:Bank  -1 USD
  Expenses:Food  1 USD
`
	const sibling = `2023-01-01 * "Neighbor" "n2"
  Assets:Bank  -5 USD
  Expenses:Food  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", main)
	writeTempFile(t, dir, "sibling.beancount", sibling)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 6 (0-indexed) is `2024-02-01 * "" "edit"`; char 14 is inside the
	// empty first string, with a second string present (payee-only context).
	list := awaitCompletion(t, client, docURI, 6, 14, 2)

	got := orderedLabels(list.Items)
	want := []string{"Local", "Neighbor"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_Payee_SameFileRankedFirst: labels = %v, want %v", got, want)
	}
}

// TestCompletion_Payee_CooccurrenceRanked: a payee from outside the current file
// and directory ranks above another such payee when it co-occurs with an account
// already used in the transaction being edited.
func TestCompletion_Payee_CooccurrenceRanked(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("TestCompletion_Payee_CooccurrenceRanked: mkdir: %v", err)
	}
	const main = `include "sub/other.beancount"
2024-01-01 open Assets:Bank USD
2024-01-01 open Assets:Cash USD
2024-01-01 open Expenses:Food USD
2024-01-01 open Expenses:Rent USD
2024-02-01 * "" "edit"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
`
	const other = `2023-01-01 * "CoOccur" "n1"
  Assets:Bank  -5 USD
  Expenses:Rent  5 USD
2023-01-02 * "FarAway" "n2"
  Assets:Cash  -5 USD
  Expenses:Rent  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", main)
	writeTempFile(t, dir, "sub/other.beancount", other)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 5 (0-indexed) is `2024-02-01 * "" "edit"`; char 14 inside the empty
	// first string (payee-only). The transaction's postings are Assets:Bank and
	// Expenses:Food, so CoOccur (shares Assets:Bank) outranks FarAway.
	list := awaitCompletion(t, client, docURI, 5, 14, 2)

	got := orderedLabels(list.Items)
	want := []string{"CoOccur", "FarAway"}
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_Payee_CooccurrenceRanked: labels = %v, want %v", got, want)
	}
}

// TestCompletion_MetaKey: ledger with metadata keys "source" and "category";
// cursor on an indented partial key returns both keys sorted alphabetically.
func TestCompletion_MetaKey(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test1"
  source: "grocery"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Test2"
  category: "food"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Overlay adds a partial metadata key line inside a new transaction.
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test1"
  source: "grocery"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Test2"
  category: "food"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
2024-01-17 * "Test3"
  sou
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaKey: didOpen: %v", err)
	}

	// Line 11 (0-indexed): "  sou", cursor at char 5.
	list := awaitCompletion(t, client, docURI, 11, 5, 2)

	want := []string{"category", "source"}
	got := labelSet(list.Items)
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_MetaKey: labels = %v, want %v", got, want)
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindProperty {
			t.Errorf("TestCompletion_MetaKey: item %q kind = %v, want Property", it.Label, it.Kind)
		}
	}
}

// TestCompletion_MetaValue_String: ledger with two transactions sharing a
// "subaccount" key with different string values; cursor on value position
// returns distinct values wrapped in quotes.
func TestCompletion_MetaValue_String(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test1"
  subaccount: "foo"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Test2"
  subaccount: "bar"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Overlay: cursor on value side of "subaccount:" (after the colon).
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test1"
  subaccount: "foo"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Test2"
  subaccount: "bar"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
2024-01-17 * "Test3"
  subaccount:
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaValue_String: didOpen: %v", err)
	}

	// Line 11 (0-indexed): "  subaccount:", cursor at char 13 (after the colon).
	list := awaitCompletion(t, client, docURI, 11, 13, 2)

	want := []string{`"bar"`, `"foo"`}
	got := labelSet(list.Items)
	if !slices.Equal(got, want) {
		t.Errorf("TestCompletion_MetaValue_String: labels = %v, want %v", got, want)
	}
	for _, it := range list.Items {
		if it.Kind != protocol.CompletionItemKindValue {
			t.Errorf("TestCompletion_MetaValue_String: item %q kind = %v, want Value", it.Label, it.Kind)
		}
	}
}

// TestCompletion_MetaValue_NotInLedger: cursor on value position for a key
// that never appears in the ledger → empty completion list.
func TestCompletion_MetaValue_NotInLedger(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test"
  source: "grocery"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	// Overlay: cursor on value side of a key ("unknown-key") not in the ledger.
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test"
  source: "grocery"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
2024-01-16 * "Test2"
  unknown-key:
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaValue_NotInLedger: didOpen: %v", err)
	}

	// Line 7 (0-indexed): "  unknown-key:", cursor at char 14 (after the colon).
	list := callCompletion(t, client, docURI, 7, 14)

	if len(list.Items) != 0 {
		t.Errorf("TestCompletion_MetaValue_NotInLedger: got %d items, want 0: %v", len(list.Items), labelSet(list.Items))
	}
}

// TestCompletion_MetaValue_Account: metadata value with MetaAccount kind
// returns the bare account name (no quotes).
func TestCompletion_MetaValue_Account(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 * "Test1"
  acct: Assets:Bank
  Assets:Bank  -10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 * "Test1"
  acct: Assets:Bank
  Assets:Bank  -10 USD
2024-01-02 * "Test2"
  acct:
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaValue_Account: didOpen: %v", err)
	}

	// Line 5 (0-indexed): "  acct:", cursor at char 7 (after colon).
	list := awaitCompletion(t, client, docURI, 5, 7, 1)

	if !containsLabel(list.Items, "Assets:Bank") {
		t.Errorf("TestCompletion_MetaValue_Account: missing 'Assets:Bank'; got %v", labelSet(list.Items))
	}
	for _, it := range list.Items {
		if it.Label == "Assets:Bank" && strings.Contains(it.Label, `"`) {
			t.Errorf("TestCompletion_MetaValue_Account: account label should be bare, got %q", it.Label)
		}
	}
}

// TestCompletion_MetaValue_ExcludedKind: metadata value with MetaNumber kind
// produces no completions.
func TestCompletion_MetaValue_ExcludedKind(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 * "Test1"
  num: 100
  Assets:Bank  -10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	const editedSrc = `2024-01-01 * "Test1"
  num: 100
  Assets:Bank  -10 USD
2024-01-02 * "Test2"
  num:
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaValue_ExcludedKind: didOpen: %v", err)
	}

	// Line 4 (0-indexed): "  num:", cursor at char 6.
	list := callCompletion(t, client, docURI, 4, 6)

	if len(list.Items) != 0 {
		t.Errorf("TestCompletion_MetaValue_ExcludedKind: got %d items, want 0: %v", len(list.Items), labelSet(list.Items))
	}
}

// TestCompletion_MetaKey_PostingLevel: metadata key defined at posting level
// (4-space indent inside a posting) appears in MetaKey completions.
func TestCompletion_MetaKey_PostingLevel(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test1"
  Assets:Bank  -10 USD
    receipt: "abc"
  Expenses:Food  10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	const editedSrc = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Test1"
  Assets:Bank  -10 USD
    receipt: "abc"
  Expenses:Food  10 USD
2024-01-16 * "Test2"
  Assets:Bank  -20 USD
    rec
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaKey_PostingLevel: didOpen: %v", err)
	}

	// Line 8 (0-indexed): "    rec", cursor at char 7.
	list := awaitCompletion(t, client, docURI, 8, 7, 1)

	if !containsLabel(list.Items, "receipt") {
		t.Errorf("TestCompletion_MetaKey_PostingLevel: missing 'receipt'; got %v", labelSet(list.Items))
	}
}

// TestCompletion_MetaValue_InStringInsert: when the cursor is inside an
// already-opened string (linePrefix ends with odd number of quotes after the
// colon), the InsertText strips the surrounding quotes so the editor does not
// duplicate the opening quote.
func TestCompletion_MetaValue_InStringInsert(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 * "Test1"
  key: "foo"
  Assets:Bank  -10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCompletionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	const editedSrc = `2024-01-01 * "Test1"
  key: "foo"
  Assets:Bank  -10 USD
2024-01-02 * "Test2"
  key: "
`
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 2, Text: editedSrc},
	}); err != nil {
		t.Fatalf("TestCompletion_MetaValue_InStringInsert: didOpen: %v", err)
	}

	// Line 4 (0-indexed): `  key: "`, cursor at char 8 (inside open string).
	list := awaitCompletion(t, client, docURI, 4, 8, 1)

	found := false
	for _, it := range list.Items {
		if it.Label == `"foo"` {
			found = true
			if it.InsertText != "foo" {
				t.Errorf("TestCompletion_MetaValue_InStringInsert: InsertText = %q, want %q", it.InsertText, "foo")
			}
		}
	}
	if !found {
		t.Errorf("TestCompletion_MetaValue_InStringInsert: missing label %q; got %v", `"foo"`, labelSet(list.Items))
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
