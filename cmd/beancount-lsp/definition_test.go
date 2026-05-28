package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// callDefinition sends textDocument/definition and returns the location array.
func callDefinition(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32) []protocol.Location {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/definition", &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
			Position:     protocol.Position{Line: line, Character: char},
		},
	}, &raw); err != nil {
		t.Fatalf("handleDefinition: call error: %v", err)
	}
	var locs []protocol.Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		t.Fatalf("handleDefinition: unmarshal: %v", err)
	}
	return locs
}

// awaitDefinition retries callDefinition until at least one location is returned
// or the deadline (3 s) is reached. This guards against the session snapshot not
// yet being available immediately after initialize.
func awaitDefinition(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32) []protocol.Location {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		locs := callDefinition(t, client, docURI, line, char)
		if len(locs) > 0 || time.Now().After(deadline) {
			return locs
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDefinition_Account_OpenSide(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// "Assets:Bank" is at character 16 on line 0.
	locs := awaitDefinition(t, client, docURI, 0, 16)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Account_OpenSide: got %d locations, want 1", len(locs))
	}
	if locs[0].URI != docURI {
		t.Errorf("handleDefinition: Account_OpenSide: URI = %v, want %v", locs[0].URI, docURI)
	}
	if locs[0].Range.Start.Line != 0 {
		t.Errorf("handleDefinition: Account_OpenSide: Range.Start.Line = %d, want 0", locs[0].Range.Start.Line)
	}
}

func TestDefinition_Account_TransactionPosting(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// "Assets:Bank" posting is on line 3, character 2.
	locs := awaitDefinition(t, client, docURI, 3, 2)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Account_TransactionPosting: got %d locations, want 1", len(locs))
	}
	// The Open for Assets:Bank is on line 0.
	if locs[0].Range.Start.Line != 0 {
		t.Errorf("handleDefinition: Account_TransactionPosting: Open line = %d, want 0", locs[0].Range.Start.Line)
	}
}

func TestDefinition_Account_NotOpened(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-15 * "Dinner"
  Assets:Ghost  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// "Assets:Ghost" on line 1 at char 2.
	locs := callDefinition(t, client, docURI, 1, 2)
	if len(locs) != 0 {
		t.Errorf("handleDefinition: Account_NotOpened: got %d locations, want 0", len(locs))
	}
}

func TestDefinition_Currency_CommoditySide(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 commodity EUR\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// "EUR" is at character 21 on line 0.
	locs := awaitDefinition(t, client, docURI, 0, 21)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Currency_CommoditySide: got %d locations, want 1", len(locs))
	}
	if locs[0].URI != docURI {
		t.Errorf("handleDefinition: Currency_CommoditySide: URI = %v, want %v", locs[0].URI, docURI)
	}
	if locs[0].Range.Start.Line != 0 {
		t.Errorf("handleDefinition: Currency_CommoditySide: Range.Start.Line = %d, want 0", locs[0].Range.Start.Line)
	}
}

func TestDefinition_Currency_TransactionAmount(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 commodity USD
2024-01-15 * "Buy"
  Assets:Bank  -50 USD
  Expenses:Food  50 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// "USD" on line 3 (posting amount): "  Assets:Bank  -50 USD"
	// 2 spaces + 11 (Assets:Bank) + 2 spaces + 1 (-) + 2 (50) + 1 space = 19
	locs := awaitDefinition(t, client, docURI, 3, 19)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Currency_TransactionAmount: got %d locations, want 1", len(locs))
	}
	// Commodity USD is on line 1.
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("handleDefinition: Currency_TransactionAmount: Commodity line = %d, want 1", locs[0].Range.Start.Line)
	}
}

func TestDefinition_Include_RelativePath(t *testing.T) {
	dir := t.TempDir()
	subContent := "2024-01-01 open Assets:Cash USD\n"
	subFile := writeTempFile(t, dir, "sub.beancount", subContent)
	mainContent := `include "sub.beancount"
2024-01-01 open Assets:Bank USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", mainContent)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)
	subURI := uri.File(subFile)

	// `"sub.beancount"` starts at char 8 on line 0 (after "include ").
	locs := awaitDefinition(t, client, docURI, 0, 10)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Include_RelativePath: got %d locations, want 1", len(locs))
	}
	if locs[0].URI != subURI {
		t.Errorf("handleDefinition: Include_RelativePath: URI = %v, want %v", locs[0].URI, subURI)
	}
}

func TestDefinition_Include_Glob(t *testing.T) {
	dir := t.TempDir()
	mainContent := "include \"*.beancount\"\n"
	rootFile := writeTempFile(t, dir, "main.beancount", mainContent)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Inside `"*.beancount"` at char 9.
	locs := callDefinition(t, client, docURI, 0, 9)
	if len(locs) != 0 {
		t.Errorf("handleDefinition: Include_Glob: got %d locations, want 0", len(locs))
	}
}

func TestDefinition_Include_QuoteBoundary(t *testing.T) {
	dir := t.TempDir()
	subContent := "2024-01-01 open Assets:Cash USD\n"
	subFile := writeTempFile(t, dir, "sub.beancount", subContent)
	mainContent := `include "sub.beancount"
2024-01-01 open Assets:Bank USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", mainContent)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)
	subURI := uri.File(subFile)

	// Cursor on the opening quote of `"sub.beancount"` (char 8).
	locs := awaitDefinition(t, client, docURI, 0, 8)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Include_QuoteBoundary: got %d locations, want 1", len(locs))
	}
	if locs[0].URI != subURI {
		t.Errorf("handleDefinition: Include_QuoteBoundary: URI = %v, want %v", locs[0].URI, subURI)
	}
}

func TestDefinition_Include_Empty(t *testing.T) {
	dir := t.TempDir()
	mainContent := "include \"\"\n"
	rootFile := writeTempFile(t, dir, "main.beancount", mainContent)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor inside the empty string `""` (char 9).
	locs := callDefinition(t, client, docURI, 0, 9)
	if len(locs) != 0 {
		t.Errorf("handleDefinition: Include_Empty: got %d locations, want 0", len(locs))
	}
}

func TestDefinition_Currency_CostSpec(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Stocks USD
2024-01-01 commodity USD
2024-01-15 * "Buy"
  Assets:Stocks  10 GOOG {100 USD}
  Assets:Stocks  -1000 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 3: "  Assets:Stocks  10 GOOG {100 USD}"
	// "  Assets:Stocks  10 GOOG {100 " = 30 chars → USD at char 30.
	locs := awaitDefinition(t, client, docURI, 3, 30)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Currency_CostSpec: got %d locations, want 1", len(locs))
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("handleDefinition: Currency_CostSpec: Commodity line = %d, want 1", locs[0].Range.Start.Line)
	}
}

func TestDefinition_Currency_PriceAnnotation(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Stocks USD
2024-01-01 commodity USD
2024-01-15 * "Buy"
  Assets:Stocks  10 GOOG @ 100 USD
  Assets:Stocks  -1000 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 3: "  Assets:Stocks  10 GOOG @ 100 USD"
	// "  Assets:Stocks  10 GOOG @ 100 " = 31 chars → USD at char 31.
	locs := awaitDefinition(t, client, docURI, 3, 31)

	if len(locs) != 1 {
		t.Fatalf("handleDefinition: Currency_PriceAnnotation: got %d locations, want 1", len(locs))
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("handleDefinition: Currency_PriceAnnotation: Commodity line = %d, want 1", locs[0].Range.Start.Line)
	}
}

func TestDefinition_Currency_NotDeclared(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-15 * "Spend"
  Assets:Bank  -50 USD
  Expenses:Food  50 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 0: "2024-01-01 open Assets:Bank USD" → USD at char 28.
	locs := callDefinition(t, client, docURI, 0, 28)
	if len(locs) != 0 {
		t.Errorf("handleDefinition: Currency_NotDeclared: got %d locations, want 0", len(locs))
	}
}
