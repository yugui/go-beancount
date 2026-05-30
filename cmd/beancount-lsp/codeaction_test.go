package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/session"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// newCodeActionServer initializes a real-session server and returns the LSP
// client connected to it.
func newCodeActionServer(t *testing.T, rootFile string) *lspClient {
	t.Helper()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) {
		return session.New(rootFile)
	}))
	client, _, _ := newDiagTestPair(t, srv)
	ctx := context.Background()
	if err := client.call(ctx, "initialize", initializeParams(uri.File(filepath.Dir(rootFile))), nil); err != nil {
		t.Fatalf("handleCodeAction: initialize: %v", err)
	}
	return client
}

// callCodeAction sends textDocument/codeAction over the entire requested
// range, with no Only filter, and returns the decoded actions.
func callCodeAction(t *testing.T, client *lspClient, docURI uri.URI, rng protocol.Range) []protocol.CodeAction {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/codeAction", &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Range:        rng,
		Context:      protocol.CodeActionContext{},
	}, &raw); err != nil {
		t.Fatalf("handleCodeAction: call error: %v", err)
	}
	if string(raw) == "null" || len(raw) == 0 {
		return nil
	}
	var actions []protocol.CodeAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatalf("handleCodeAction: unmarshal: %v", err)
	}
	return actions
}

// awaitCodeAction retries callCodeAction until at least one action is
// returned or 3 s elapse. Guards against the initial snapshot not being
// ready when the test fires immediately after initialize.
func awaitCodeAction(t *testing.T, client *lspClient, docURI uri.URI, rng protocol.Range) []protocol.CodeAction {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a := callCodeAction(t, client, docURI, rng)
		if len(a) > 0 || time.Now().After(deadline) {
			return a
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// cursorRange builds a zero-width Range at (line, char) in LSP coordinates.
func cursorRange(line, char uint32) protocol.Range {
	pos := protocol.Position{Line: line, Character: char}
	return protocol.Range{Start: pos, End: pos}
}

// findAction returns the first action whose title equals want, or nil.
func findAction(actions []protocol.CodeAction, want string) *protocol.CodeAction {
	for i := range actions {
		if actions[i].Title == want {
			return &actions[i]
		}
	}
	return nil
}

// editNewText returns the NewText of the first TextEdit in action's
// WorkspaceEdit for docURI, or "" when none exists. Tests rely on this
// helper rather than reaching into the protocol type by hand.
func editNewText(t *testing.T, action *protocol.CodeAction, docURI uri.URI) string {
	t.Helper()
	if action == nil || action.Edit == nil {
		t.Fatal("handleCodeAction: action has no edit")
	}
	edits := action.Edit.Changes[docURI]
	if len(edits) == 0 {
		t.Fatalf("handleCodeAction: edit has no changes for %s", docURI)
	}
	return edits[0].NewText
}

// TestCodeAction_AutoPosting expands an auto-balanced posting into an
// explicit-amount posting.
func TestCodeAction_AutoPosting(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Cash USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Expenses:Food  20 USD
  Assets:Cash
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCodeActionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on the "Assets:Cash" line (line 4, char 2-13).
	actions := awaitCodeAction(t, client, docURI, cursorRange(4, 4))

	a := findAction(actions, "Expand auto-balanced amount")
	if a == nil {
		t.Fatalf("handleCodeAction: AutoPosting: no expand action; got %d actions: %+v", len(actions), actions)
	}
	if a.Kind != protocol.RefactorRewrite {
		t.Errorf("handleCodeAction: AutoPosting: kind = %q, want %q", a.Kind, protocol.RefactorRewrite)
	}

	got := editNewText(t, a, docURI)
	if !strings.Contains(got, "Assets:Cash") {
		t.Errorf("handleCodeAction: AutoPosting: new text missing account:\n%s", got)
	}
	if !strings.Contains(got, "-20 USD") {
		t.Errorf("handleCodeAction: AutoPosting: new text missing inferred -20 USD:\n%s", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("handleCodeAction: AutoPosting: expected single-line replacement, got:\n%s", got)
	}
}

// TestCodeAction_CostSpecAbbreviated expands an abbreviated cost spec
// to its fully-resolved form on a single-lot reduction.
func TestCodeAction_CostSpecAbbreviated(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Stock
2024-01-01 open Assets:Cash USD
2024-01-01 open Income:Gains USD
2024-01-05 * "Buy"
  Assets:Stock  10 ACME {100 USD}
  Assets:Cash  -1000 USD
2024-02-10 * "Sell"
  Assets:Stock  -3 ACME {}
  Assets:Cash  330 USD
  Income:Gains
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCodeActionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "Assets:Stock  -3 ACME {}" (line 7, around the cost).
	actions := awaitCodeAction(t, client, docURI, cursorRange(7, 25))

	a := findAction(actions, "Expand cost specification")
	if a == nil {
		t.Fatalf("handleCodeAction: CostSpecAbbreviated: no expand action; got %d actions: %+v", len(actions), actions)
	}
	got := editNewText(t, a, docURI)
	if !strings.Contains(got, "{100 USD, 2024-01-05}") {
		t.Errorf("handleCodeAction: CostSpecAbbreviated: new text missing resolved cost {100 USD, 2024-01-05}:\n%s", got)
	}
	if !strings.Contains(got, "-3 ACME") {
		t.Errorf("handleCodeAction: CostSpecAbbreviated: new text missing units -3 ACME:\n%s", got)
	}
}

// TestCodeAction_MultiLotReduction expands an abbreviated cost spec into
// multiple per-lot reduction lines.
func TestCodeAction_MultiLotReduction(t *testing.T) {
	dir := t.TempDir()
	const src = `option "booking_method" "FIFO"
2024-01-01 open Assets:Stock
2024-01-01 open Assets:Cash USD
2024-01-01 open Income:Gains USD
2024-01-05 * "Buy lot 1"
  Assets:Stock  2 ACME {80 USD}
  Assets:Cash  -160 USD
2024-01-06 * "Buy lot 2"
  Assets:Stock  1 ACME {90 USD}
  Assets:Cash  -90 USD
2024-02-10 * "Sell"
  Assets:Stock  -3 ACME {}
  Assets:Cash  250 USD
  Income:Gains
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCodeActionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "Assets:Stock  -3 ACME {}" (line 11).
	actions := awaitCodeAction(t, client, docURI, cursorRange(11, 25))

	a := findAction(actions, "Expand cost specification")
	if a == nil {
		t.Fatalf("handleCodeAction: MultiLotReduction: no expand action; got %d actions: %+v", len(actions), actions)
	}
	got := editNewText(t, a, docURI)

	// Expect two lines, one per lot, with each lot's per-unit cost.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("handleCodeAction: MultiLotReduction: expected 2 lines, got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "{80 USD, 2024-01-05}") {
		t.Errorf("handleCodeAction: MultiLotReduction: missing first-lot cost:\n%s", got)
	}
	if !strings.Contains(got, "{90 USD, 2024-01-06}") {
		t.Errorf("handleCodeAction: MultiLotReduction: missing second-lot cost:\n%s", got)
	}
}

// TestCodeAction_BookingFailureSilent: when the transaction balance is
// unresolvable, no code action is offered for the auto-posting.
func TestCodeAction_BookingFailureSilent(t *testing.T) {
	dir := t.TempDir()
	// Two unknowns in different currencies cannot both be inferred from a
	// single residual; the booking pass leaves the auto-posting unresolved.
	const src = `2024-01-01 open Assets:Cash USD
2024-01-01 open Assets:Bank EUR
2024-01-01 open Expenses:Misc
2024-01-15 * "Bad"
  Expenses:Misc  10 USD
  Expenses:Misc  20 EUR
  Assets:Cash
  Assets:Bank
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCodeActionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on the "Assets:Cash" auto-posting line (line 6).
	actions := callCodeAction(t, client, docURI, cursorRange(6, 4))
	if a := findAction(actions, "Expand auto-balanced amount"); a != nil {
		t.Errorf("handleCodeAction: BookingFailureSilent: unexpected expand action: %+v", *a)
	}
}

// TestCodeAction_OutsideAnyPosting returns an empty array when the cursor
// is on a directive header line.
func TestCodeAction_OutsideAnyPosting(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Cash USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Expenses:Food  20 USD
  Assets:Cash
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCodeActionServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 2 is the transaction header, no posting to expand.
	actions := callCodeAction(t, client, docURI, cursorRange(2, 5))
	for _, a := range actions {
		if a.Title == "Expand auto-balanced amount" || a.Title == "Expand cost specification" {
			t.Errorf("handleCodeAction: OutsideAnyPosting: unexpected action %q", a.Title)
		}
	}
}

// TestCodeAction_OnlyFilterRespected returns no actions when the client
// asks only for quickfix kinds.
func TestCodeAction_OnlyFilterRespected(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Cash USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Expenses:Food  20 USD
  Assets:Cash
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newCodeActionServer(t, rootFile)
	docURI := uri.File(rootFile)

	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/codeAction", &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Range:        cursorRange(4, 4),
		Context:      protocol.CodeActionContext{Only: []protocol.CodeActionKind{protocol.QuickFix}},
	}, &raw); err != nil {
		t.Fatalf("handleCodeAction: call error: %v", err)
	}
	if string(raw) == "null" {
		return
	}
	var actions []protocol.CodeAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatalf("handleCodeAction: unmarshal: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("handleCodeAction: OnlyFilterRespected: got %d actions, want 0", len(actions))
	}
}

// TestCodeAction_InitializeAdvertisesCapability verifies that the server
// declares CodeActionProvider with the refactor.rewrite kind.
func TestCodeAction_InitializeAdvertisesCapability(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	raw, _ := json.Marshal(result.Capabilities.CodeActionProvider)
	var opts protocol.CodeActionOptions
	if err := json.Unmarshal(raw, &opts); err != nil {
		t.Fatalf("CodeActionProvider unmarshal: %v", err)
	}
	found := false
	for _, k := range opts.CodeActionKinds {
		if k == protocol.RefactorRewrite {
			found = true
		}
	}
	if !found {
		t.Errorf("initialize: CodeActionProvider missing refactor.rewrite kind, got %v", opts.CodeActionKinds)
	}
}
