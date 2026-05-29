package main

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// renameParams is the JSON shape of textDocument/rename params, declared
// locally so the tests do not depend on the protocol package's param struct.
type renameParams struct {
	TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
	Position     protocol.Position               `json:"position"`
	NewName      string                          `json:"newName"`
}

// prepareRenameParams is the JSON shape of textDocument/prepareRename params.
type prepareRenameParams struct {
	TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
	Position     protocol.Position               `json:"position"`
}

// callRename sends textDocument/rename and returns the per-file edits. It fails
// the test on transport error; callers that expect a rejection use the raw
// client instead.
func callRename(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32, newName string) map[uri.URI][]protocol.TextEdit {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	err := client.call(ctx, "textDocument/rename", renameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Position:     protocol.Position{Line: line, Character: char},
		NewName:      newName,
	}, &raw)
	if err != nil {
		t.Fatalf("handleRename: call error: %v", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var we protocol.WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		t.Fatalf("handleRename: unmarshal: %v", err)
	}
	return we.Changes
}

// awaitRename retries callRename until edits span at least wantFiles files or
// the 3 s deadline passes, guarding against the ledger snapshot not yet being
// ready right after initialize.
func awaitRename(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32, newName string, wantFiles int) map[uri.URI][]protocol.TextEdit {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		changes := callRename(t, client, docURI, line, char, newName)
		if len(changes) >= wantFiles || time.Now().After(deadline) {
			return changes
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// callPrepareRename sends textDocument/prepareRename. ok is false when the
// server responds with null (cursor not on a renamable entity).
func callPrepareRename(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32) (protocol.Range, bool) {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/prepareRename", prepareRenameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Position:     protocol.Position{Line: line, Character: char},
	}, &raw); err != nil {
		t.Fatalf("handlePrepareRename: call error: %v", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return protocol.Range{}, false
	}
	var r protocol.Range
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("handlePrepareRename: unmarshal: %v", err)
	}
	return r, true
}

// applyEdits applies edits to src and returns the result. Edits are applied
// right-to-left so earlier byte offsets stay valid.
func applyEdits(src string, edits []protocol.TextEdit) string {
	b := []byte(src)
	lo := computeLineOffsets(b)
	type span struct {
		start, end int
		text       string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		spans = append(spans, span{
			start: lspPositionToByte(e.Range.Start, b, lo),
			end:   lspPositionToByte(e.Range.End, b, lo),
			text:  e.NewText,
		})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	for _, sp := range spans {
		out := make([]byte, 0, len(b)-(sp.end-sp.start)+len(sp.text))
		out = append(out, b[:sp.start]...)
		out = append(out, sp.text...)
		out = append(out, b[sp.end:]...)
		b = out
	}
	return string(b)
}

// rangeText returns the substring of src covered by r.
func rangeText(src string, r protocol.Range) string {
	b := []byte(src)
	lo := computeLineOffsets(b)
	start := lspPositionToByte(r.Start, b, lo)
	end := lspPositionToByte(r.End, b, lo)
	return string(b[start:end])
}

func TestPrepareRename_TagExcludesSigil(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 * \"x\" #trip\n  Assets:Cash  -1 USD\n  Expenses:Misc  1 USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// '#trip' starts at char 17 ('#'); the name 't' is at 18.
	r, ok := callPrepareRename(t, client, docURI, 0, 18)
	if !ok {
		t.Fatal("handlePrepareRename: TagExcludesSigil: got null, want a range")
	}
	if got := rangeText(src, r); got != "trip" {
		t.Errorf("handlePrepareRename: TagExcludesSigil: range text = %q, want %q", got, "trip")
	}
}

func TestPrepareRename_AccountWholeToken(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	r, ok := callPrepareRename(t, client, docURI, 0, 18)
	if !ok {
		t.Fatal("handlePrepareRename: AccountWholeToken: got null, want a range")
	}
	if got := rangeText(src, r); got != "Assets:Bank" {
		t.Errorf("handlePrepareRename: AccountWholeToken: range text = %q, want %q", got, "Assets:Bank")
	}
}

func TestPrepareRename_NotRenamable(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// char 0 is on the date token.
	if _, ok := callPrepareRename(t, client, docURI, 0, 0); ok {
		t.Error("handlePrepareRename: NotRenamable: got a range on a date token, want null")
	}
}

func TestRename_TagAcrossPushPop(t *testing.T) {
	dir := t.TempDir()
	const src = "pushtag #trip\n2024-01-01 * \"x\" #trip\n  Assets:Cash  -1 USD\n  Expenses:Misc  1 USD\npoptag #trip\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// '#trip' on the transaction line: name at char 18.
	changes := callRename(t, client, docURI, 1, 18, "vacation")
	edits := changes[docURI]
	if len(edits) != 3 {
		t.Fatalf("handleRename: TagAcrossPushPop: got %d edits, want 3", len(edits))
	}
	const want = "pushtag #vacation\n2024-01-01 * \"x\" #vacation\n  Assets:Cash  -1 USD\n  Expenses:Misc  1 USD\npoptag #vacation\n"
	if got := applyEdits(src, edits); got != want {
		t.Errorf("handleRename: TagAcrossPushPop:\n got %q\nwant %q", got, want)
	}
}

func TestRename_Link(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 * \"x\" ^inv\n  Assets:Cash  -1 USD\n  Expenses:Misc  1 USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	changes := callRename(t, client, docURI, 0, 18, "invoice-9")
	edits := changes[docURI]
	if len(edits) != 1 {
		t.Fatalf("handleRename: Link: got %d edits, want 1", len(edits))
	}
	const want = "2024-01-01 * \"x\" ^invoice-9\n  Assets:Cash  -1 USD\n  Expenses:Misc  1 USD\n"
	if got := applyEdits(src, edits); got != want {
		t.Errorf("handleRename: Link:\n got %q\nwant %q", got, want)
	}
}

func TestRename_AccountHierarchical(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 open Assets:Bank\n" +
		"2024-01-02 open Assets:Bank:Checking\n" +
		"2024-01-03 * \"x\"\n" +
		"  Assets:Bank:Checking  -1 USD\n" +
		"  Assets:Bank  1 USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// 'Assets:Bank' on the first open line: char 18 is inside it.
	changes := callRename(t, client, docURI, 0, 18, "Assets:Banco")
	edits := changes[docURI]
	if len(edits) != 4 {
		t.Fatalf("handleRename: AccountHierarchical: got %d edits, want 4", len(edits))
	}
	const want = "2024-01-01 open Assets:Banco\n" +
		"2024-01-02 open Assets:Banco:Checking\n" +
		"2024-01-03 * \"x\"\n" +
		"  Assets:Banco:Checking  -1 USD\n" +
		"  Assets:Banco  1 USD\n"
	if got := applyEdits(src, edits); got != want {
		t.Errorf("handleRename: AccountHierarchical:\n got %q\nwant %q", got, want)
	}
}

func TestRename_Currency(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 commodity USD\n" +
		"2024-01-02 open Assets:Bank USD\n" +
		"2024-01-03 * \"x\"\n" +
		"  Assets:Bank  -1 USD\n" +
		"  Expenses:X  1 USD\n" +
		"2024-01-04 price USD 1.1 EUR\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// 'USD' on the commodity line starts at char 21.
	changes := callRename(t, client, docURI, 0, 22, "USX")
	edits := changes[docURI]
	if len(edits) != 5 {
		t.Fatalf("handleRename: Currency: got %d edits, want 5", len(edits))
	}
	const want = "2024-01-01 commodity USX\n" +
		"2024-01-02 open Assets:Bank USX\n" +
		"2024-01-03 * \"x\"\n" +
		"  Assets:Bank  -1 USX\n" +
		"  Expenses:X  1 USX\n" +
		"2024-01-04 price USX 1.1 EUR\n"
	if got := applyEdits(src, edits); got != want {
		t.Errorf("handleRename: Currency:\n got %q\nwant %q", got, want)
	}
}

func TestRename_MultiFile(t *testing.T) {
	dir := t.TempDir()
	const subSrc = "2024-01-02 * \"x\"\n  Assets:Bank  -1 USD\n  Expenses:X  1 USD\n"
	subFile := writeTempFile(t, dir, "sub.beancount", subSrc)
	const rootSrc = "include \"sub.beancount\"\n2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", rootSrc)
	client := newSymbolServer(t, rootFile)
	rootURI := uri.File(rootFile)
	subURI := uri.File(subFile)

	// 'Assets:Bank' on root line 1, char 18.
	changes := awaitRename(t, client, rootURI, 1, 18, "Assets:Banco", 2)
	if len(changes) != 2 {
		t.Fatalf("handleRename: MultiFile: edits span %d files, want 2", len(changes))
	}

	const wantRoot = "include \"sub.beancount\"\n2024-01-01 open Assets:Banco USD\n"
	if got := applyEdits(rootSrc, changes[rootURI]); got != wantRoot {
		t.Errorf("handleRename: MultiFile: root\n got %q\nwant %q", got, wantRoot)
	}
	const wantSub = "2024-01-02 * \"x\"\n  Assets:Banco  -1 USD\n  Expenses:X  1 USD\n"
	if got := applyEdits(subSrc, changes[subURI]); got != wantSub {
		t.Errorf("handleRename: MultiFile: sub\n got %q\nwant %q", got, wantSub)
	}
}

func TestRename_InvalidNameRejected(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 * \"x\" #trip\n  Assets:Cash  -1 USD\n  Expenses:Misc  1 USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	var raw json.RawMessage
	err := client.call(ctx, "textDocument/rename", renameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Position:     protocol.Position{Line: 0, Character: 18},
		NewName:      "bad name",
	}, &raw)
	if err == nil {
		t.Error("handleRename: InvalidNameRejected: got nil error, want rejection")
	}
}

func TestRename_NotRenamableReturnsNull(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// char 0 is on the date token.
	if changes := callRename(t, client, docURI, 0, 0, "whatever"); changes != nil {
		t.Errorf("handleRename: NotRenamableReturnsNull: got %v, want null", changes)
	}
}
