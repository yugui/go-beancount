package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// openDoc is a test helper that opens a document in the server via didOpen.
func openDoc(t *testing.T, client *lspClient, docURI uri.URI, text string) {
	t.Helper()
	ctx := context.Background()
	err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: text},
	})
	if err != nil {
		t.Fatalf("didOpen: %v", err)
	}
}

// callFormatting calls textDocument/formatting and returns the TextEdit array.
func callFormatting(t *testing.T, client *lspClient, docURI uri.URI) []protocol.TextEdit {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/formatting", &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Options:      protocol.FormattingOptions{},
	}, &raw); err != nil {
		t.Fatalf("handleFormatting: call error: %v", err)
	}
	var edits []protocol.TextEdit
	if err := json.Unmarshal(raw, &edits); err != nil {
		t.Fatalf("handleFormatting: unmarshal: %v", err)
	}
	return edits
}

// callRangeFormatting calls textDocument/rangeFormatting and returns the TextEdit array.
func callRangeFormatting(t *testing.T, client *lspClient, docURI uri.URI, r protocol.Range) []protocol.TextEdit {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/rangeFormatting", &protocol.DocumentRangeFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Range:        r,
		Options:      protocol.FormattingOptions{},
	}, &raw); err != nil {
		t.Fatalf("handleRangeFormatting: call error: %v", err)
	}
	var edits []protocol.TextEdit
	if err := json.Unmarshal(raw, &edits); err != nil {
		t.Fatalf("handleRangeFormatting: unmarshal: %v", err)
	}
	return edits
}

// newFormattingServer creates an initialized server+client pair for formatting tests.
func newFormattingServer(t *testing.T) (*lspClient, *stubSession) {
	t.Helper()
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return client, stub
}

// --- Whole-document formatting tests ---

// TestFormatting_DocumentFormatting_Roundtrip verifies that an already-formatted
// document returns an empty TextEdit array (no-op).
func TestFormatting_DocumentFormatting_Roundtrip(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/a.beancount")
	// Single well-formatted open directive.
	const src = "2024-01-01 open Assets:Cash USD\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	edits := callFormatting(t, client, docURI)
	if len(edits) != 0 {
		t.Errorf("handleFormatting: Roundtrip: got %d edits, want 0", len(edits))
	}
}

// TestFormatting_DocumentFormatting_Unformatted verifies that an unformatted
// document returns exactly one TextEdit replacing [0, EOF) with the formatted output.
func TestFormatting_DocumentFormatting_Unformatted(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/b.beancount")

	// Two directives with no blank line between them — format adds one.
	const src = "2024-01-01 open Assets:Cash\n2024-01-02 open Expenses:Food\n"
	const want = "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	edits := callFormatting(t, client, docURI)
	if len(edits) != 1 {
		t.Fatalf("handleFormatting: Unformatted: got %d edits, want 1", len(edits))
	}
	e := edits[0]
	if e.Range.Start.Line != 0 || e.Range.Start.Character != 0 {
		t.Errorf("handleFormatting: Unformatted: Range.Start = %v, want {0,0}", e.Range.Start)
	}
	if e.NewText != want {
		t.Errorf("handleFormatting: Unformatted: NewText =\n%q\nwant\n%q", e.NewText, want)
	}
}

// loadLedger builds an *ast.Ledger from src, failing t on error.
func loadLedger(t *testing.T, src string) *ast.Ledger {
	t.Helper()
	ledger, err := ast.Load(src)
	if err != nil {
		t.Fatalf("ast.Load: %v", err)
	}
	return ledger
}

// TestFormatting_RenderCommas verifies that whole-document formatting inserts
// thousands separators when the ledger enables render_commas.
func TestFormatting_RenderCommas(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/rc.beancount")

	const src = `option "render_commas" "True"
2020-01-02 balance Assets:A 1000 JPY
`
	stub.setSnapshot(loadLedger(t, src))
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	edits := callFormatting(t, client, docURI)
	if len(edits) != 1 {
		t.Fatalf("handleFormatting: RenderCommas: got %d edits, want 1", len(edits))
	}
	if !strings.Contains(edits[0].NewText, "1,000 JPY") {
		t.Errorf("handleFormatting: RenderCommas: NewText =\n%q\nwant it to contain %q", edits[0].NewText, "1,000 JPY")
	}
}

// TestFormatting_RenderCommasDisabled verifies that formatting leaves numbers
// without separators when render_commas is unset (the default).
func TestFormatting_RenderCommasDisabled(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/rc-off.beancount")

	const src = "2020-01-02 balance Assets:A 1000 JPY\n"
	stub.setSnapshot(loadLedger(t, src))
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	edits := callFormatting(t, client, docURI)
	for _, e := range edits {
		if strings.Contains(e.NewText, "1,000") {
			t.Errorf("handleFormatting: RenderCommasDisabled: NewText =\n%q\nwant no commas", e.NewText)
		}
	}
}

// TestRangeFormatting_RenderCommas verifies that range formatting honors the
// ledger's render_commas option even though the formatted substring excludes
// the option directive.
func TestRangeFormatting_RenderCommas(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/rc-range.beancount")

	const src = `option "render_commas" "True"

2020-01-02 balance Assets:A 1000 JPY
`
	stub.setSnapshot(loadLedger(t, src))
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range covering the balance directive (line 2).
	r := protocol.Range{
		Start: protocol.Position{Line: 2, Character: 0},
		End:   protocol.Position{Line: 2, Character: 36},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 1 {
		t.Fatalf("handleRangeFormatting: RenderCommas: got %d edits, want 1", len(edits))
	}
	if !strings.Contains(edits[0].NewText, "1,000 JPY") {
		t.Errorf("handleRangeFormatting: RenderCommas: NewText =\n%q\nwant it to contain %q", edits[0].NewText, "1,000 JPY")
	}
}

// --- Range formatting tests ---

// TestRangeFormatting_PinpointOneDirective verifies that a range exactly covering
// one already-formatted directive returns empty TextEdit array.
func TestRangeFormatting_PinpointOneDirective(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/c.beancount")

	// Two directives. Dir1: "2024-01-01 open Assets:Cash" (line 0).
	const src = "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Select exactly line 0 — only dir1, already formatted.
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 0, Character: 27},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 0 {
		t.Errorf("handleRangeFormatting: PinpointOneDirective: got %d edits, want 0", len(edits))
	}
}

// TestRangeFormatting_SpansTwoDirectives verifies that a range covering two
// directives (which each need blank-line normalization) returns one TextEdit
// with the formatted two-directive text.
func TestRangeFormatting_SpansTwoDirectives(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/d.beancount")

	// Two directives with no blank line — unformatted.
	// Dir1 ends at byte 27 (exclusive), dir2 starts at byte 28.
	// nodeByteRange excludes trivia, so the substring spans both directive texts.
	const src = "2024-01-01 open Assets:Cash\n2024-01-02 open Expenses:Food\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range covering both lines.
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 1, Character: 29},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 1 {
		t.Fatalf("handleRangeFormatting: SpansTwoDirectives: got %d edits, want 1", len(edits))
	}
	e := edits[0]
	// The NewText must contain a blank line between the two directives.
	const wantNewText = "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food"
	if e.NewText != wantNewText {
		t.Errorf("handleRangeFormatting: SpansTwoDirectives: NewText =\n%q\nwant\n%q", e.NewText, wantNewText)
	}
}

// TestRangeFormatting_PartialMidDirective verifies that a range strictly inside
// a directive is expanded to whole-directive boundaries and that an edit is
// produced when the directive needs reformatting.
func TestRangeFormatting_PartialMidDirective(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/e.beancount")

	// Unformatted transaction: tokens span [0, 90); byteOffsetToLSP(90) = {Line:2, Character:30}.
	const src = "2024-01-15 * \"Payee\" \"Narration\"\n  Assets:Cash   100.00 USD\n  Expenses:Food    -100.00 USD\n"
	const wantNewText = "2024-01-15 * \"Payee\" \"Narration\"\n  Assets:Cash                             100.00 USD\n  Expenses:Food                          -100.00 USD"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range inside the narration string — strictly within the directive.
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 14},
		End:   protocol.Position{Line: 0, Character: 20},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 1 {
		t.Fatalf("handleRangeFormatting: PartialMidDirective: got %d edits, want 1", len(edits))
	}
	e := edits[0]
	// The returned range must span the full directive, not just the requested sub-range.
	wantStart := protocol.Position{Line: 0, Character: 0}
	wantEnd := protocol.Position{Line: 2, Character: 30}
	if e.Range.Start != wantStart {
		t.Errorf("handleRangeFormatting: PartialMidDirective: Range.Start = %v, want %v", e.Range.Start, wantStart)
	}
	if e.Range.End != wantEnd {
		t.Errorf("handleRangeFormatting: PartialMidDirective: Range.End = %v, want %v", e.Range.End, wantEnd)
	}
	if e.NewText != wantNewText {
		t.Errorf("handleRangeFormatting: PartialMidDirective: NewText =\n%q\nwant\n%q", e.NewText, wantNewText)
	}
}

// TestRangeFormatting_WhitespaceOnly verifies that a non-empty range covering
// only whitespace between directives returns an empty TextEdit array.
func TestRangeFormatting_WhitespaceOnly(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/f.beancount")

	// Three lines: open, blank line, open. The blank line (line 1) contains no directive.
	const src = "2024-01-01 open Assets:A USD\n\n2024-01-02 open Assets:B EUR\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Non-empty range covering the blank line only — no directive overlaps it.
	r := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 0},
		End:   protocol.Position{Line: 2, Character: 0},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 0 {
		t.Errorf("handleRangeFormatting: WhitespaceOnly: got %d edits, want 0", len(edits))
	}
}

// TestRangeFormatting_PastEOF verifies that a range past EOF returns an empty
// TextEdit array.
func TestRangeFormatting_PastEOF(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/g.beancount")

	const src = "2024-01-01 open Assets:Cash\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range far past EOF — clamped to len(src), which is after all directives.
	r := protocol.Range{
		Start: protocol.Position{Line: 999, Character: 0},
		End:   protocol.Position{Line: 999, Character: 0},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 0 {
		t.Errorf("handleRangeFormatting: PastEOF: got %d edits, want 0", len(edits))
	}
}

// TestRangeFormatting_AlreadyFormatted verifies that a range over already-formatted
// content returns an empty TextEdit array.
func TestRangeFormatting_AlreadyFormatted(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/h.beancount")

	const src = "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range covering both directives — already formatted (blank line present).
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 2, Character: 29},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 0 {
		t.Errorf("handleRangeFormatting: AlreadyFormatted: got %d edits, want 0", len(edits))
	}
}

// TestRangeFormatting_ErrorNodeExcluded verifies that a range that does not
// overlap a malformed directive at the file start does not include it in the edit.
func TestRangeFormatting_ErrorNodeExcluded(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/i.beancount")

	// Line 0: malformed (ErrorNode), line 1: blank, line 2: valid open directive.
	const src = "BADLINE\n\n2024-01-01 open Assets:Cash\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range covering only line 2 (the open directive).
	r := protocol.Range{
		Start: protocol.Position{Line: 2, Character: 0},
		End:   protocol.Position{Line: 2, Character: 27},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	// The open directive is already formatted, so we expect empty.
	// The error node must not be included.
	if len(edits) != 0 {
		t.Fatalf("handleRangeFormatting: ErrorNodeExcluded: got %d edits, want 0", len(edits))
	}
	// Extra: if for some reason a non-empty edit is returned, assert it doesn't
	// start at line 0 (which would indicate the error node was included).
	for _, e := range edits {
		if e.Range.Start.Line == 0 {
			t.Errorf("handleRangeFormatting: ErrorNodeExcluded: edit Range.Start.Line = %d, want > 0 (error node must not be included)", e.Range.Start.Line)
		}
	}
}

// TestRangeFormatting_ErrorNodeWithinRange verifies that when a range spans both
// a malformed line and a valid directive, the result is one TextEdit whose
// NewText starts with the malformed line verbatim and contains the formatted
// valid directive.
func TestRangeFormatting_ErrorNodeWithinRange(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/j.beancount")

	// Line 0: malformed (ErrorNode), line 1: valid open directive (no blank line).
	const src = "BADLINE\n2024-01-01 open Assets:Cash\n"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range spanning both lines (from start of line 0 to end of line 1).
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 1, Character: 27},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 1 {
		t.Fatalf("handleRangeFormatting: ErrorNodeWithinRange: got %d edits, want 1", len(edits))
	}
	e := edits[0]
	// The malformed line must appear verbatim at the start of NewText.
	if len(e.NewText) < 7 || e.NewText[:7] != "BADLINE" {
		t.Errorf("handleRangeFormatting: ErrorNodeWithinRange: NewText = %q, want prefix \"BADLINE\"", e.NewText)
	}
	// The formatted directive must appear somewhere in NewText.
	const wantDirective = "2024-01-01 open Assets:Cash"
	if !strings.Contains(e.NewText, wantDirective) {
		t.Errorf("handleRangeFormatting: ErrorNodeWithinRange: NewText = %q, want it to contain %q", e.NewText, wantDirective)
	}
}

// TestRangeFormatting_OptionDirective verifies that an OptionDirective is
// recognized as a top-level directive and included in range expansion.
func TestRangeFormatting_OptionDirective(t *testing.T) {
	client, stub := newFormattingServer(t)
	docURI := uri.URI("file:///tmp/k.beancount")

	// Option + open with no blank line; tokens end at byte 50: byteOffsetToLSP(50) = {Line:1, Character:28}.
	const src = "option \"title\" \"Test\"\n2024-01-01 open Assets:A USD\n"
	const wantNewText = "option \"title\" \"Test\"\n\n2024-01-01 open Assets:A USD"
	openDoc(t, client, docURI, src)
	stub.awaitSet(t, 3*time.Second)

	// Range covering both the option and the open directive.
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 1, Character: 28},
	}
	edits := callRangeFormatting(t, client, docURI, r)
	if len(edits) != 1 {
		t.Fatalf("handleRangeFormatting: OptionDirective: got %d edits, want 1", len(edits))
	}
	e := edits[0]
	if e.NewText != wantNewText {
		t.Errorf("handleRangeFormatting: OptionDirective: NewText =\n%q\nwant\n%q", e.NewText, wantNewText)
	}
	wantStart := protocol.Position{Line: 0, Character: 0}
	wantEnd := protocol.Position{Line: 1, Character: 28}
	if e.Range.Start != wantStart || e.Range.End != wantEnd {
		t.Errorf("handleRangeFormatting: OptionDirective: Range = %v, want {%v, %v}", e.Range, wantStart, wantEnd)
	}
}
