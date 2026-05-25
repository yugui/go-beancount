package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/session"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// newHoverServer creates a server+client backed by a real session loading rootFile.
func newHoverServer(t *testing.T, rootFile string) *lspClient {
	t.Helper()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) {
		return session.New(rootFile)
	}))
	client, _, _ := newDiagTestPair(t, srv)
	ctx := context.Background()
	if err := client.call(ctx, "initialize", initializeParams(uri.File(rootFile)), nil); err != nil {
		t.Fatalf("handleHover: initialize: %v", err)
	}
	return client
}

// newHoverServerWithClock creates a hover server with an injected clock.
func newHoverServerWithClock(t *testing.T, rootFile string, clock func() time.Time) *lspClient {
	t.Helper()
	srv := NewServer(
		WithSessionFactory(func(string) (SessionAPI, error) {
			return session.New(rootFile)
		}),
		WithClock(clock),
	)
	client, _, _ := newDiagTestPair(t, srv)
	ctx := context.Background()
	if err := client.call(ctx, "initialize", initializeParams(uri.File(rootFile)), nil); err != nil {
		t.Fatalf("handleHover: initialize: %v", err)
	}
	return client
}

// callHover sends textDocument/hover and returns the (possibly nil) Hover result.
func callHover(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32) *protocol.Hover {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/hover", &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
			Position:     protocol.Position{Line: line, Character: char},
		},
	}, &raw); err != nil {
		t.Fatalf("handleHover: call error: %v", err)
	}
	if string(raw) == "null" || len(raw) == 0 {
		return nil
	}
	var h protocol.Hover
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("handleHover: unmarshal: %v", err)
	}
	return &h
}

// awaitHover retries callHover until a non-nil result arrives or the 3 s
// deadline is reached. Used when the session snapshot may not be immediately
// ready after initialize.
func awaitHover(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32) *protocol.Hover {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h := callHover(t, client, docURI, line, char)
		if h != nil || time.Now().After(deadline) {
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHover_Account_Open: cursor on account name in Open directive.
// "2024-01-01 open Assets:Bank USD\n"
//
//	012345678901234567890
//
// "Assets:Bank" starts at char 16.
func TestHover_Account_Open(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	h := awaitHover(t, client, docURI, 0, 16)

	if h == nil {
		t.Fatal("handleHover: Account_Open: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "**Assets:Bank**") {
		t.Errorf("handleHover: Account_Open: markdown missing account name:\n%s", md)
	}
	if !strings.Contains(md, "2024-01-01") {
		t.Errorf("handleHover: Account_Open: markdown missing opened date:\n%s", md)
	}
}

// TestHover_Account_Posting: cursor on account name in a posting.
// Line 3: "  Assets:Bank  -20 USD"  → "Assets:Bank" at char 2.
func TestHover_Account_Posting(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	h := awaitHover(t, client, docURI, 3, 2)

	if h == nil {
		t.Fatal("handleHover: Account_Posting: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "**Assets:Bank**") {
		t.Errorf("handleHover: Account_Posting: markdown missing account name:\n%s", md)
	}
	if !strings.Contains(md, "2024-01-01") {
		t.Errorf("handleHover: Account_Posting: markdown missing opened date:\n%s", md)
	}
}

// TestHover_Account_NotOpened: account that was never opened → nil result.
func TestHover_Account_NotOpened(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-15 * "Dinner"
  Assets:Ghost  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Poll until deadline; nil result after deadline is the correct outcome.
	deadline := time.Now().Add(3 * time.Second)
	for {
		h := callHover(t, client, docURI, 1, 2)
		if h != nil {
			t.Errorf("handleHover: Account_NotOpened: got hover result, want nil:\n%s", h.Contents.Value)
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHover_Commodity_Declared: cursor on USD in a commodity directive.
// "2024-01-01 commodity USD\n" → "USD" at char 21.
func TestHover_Commodity_Declared(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 commodity USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	h := awaitHover(t, client, docURI, 0, 21)

	if h == nil {
		t.Fatal("handleHover: Commodity_Declared: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "**USD**") {
		t.Errorf("handleHover: Commodity_Declared: markdown missing currency name:\n%s", md)
	}
	if !strings.Contains(md, "(commodity)") {
		t.Errorf("handleHover: Commodity_Declared: markdown missing '(commodity)':\n%s", md)
	}
}

// TestHover_Commodity_WithPrice: USD declared + price directive + cursor on USD
// in a later transaction.  The hover should show the price from date D.
//
// Line 0: "2024-01-01 commodity USD"
// Line 1: "2024-01-10 price USD 110.50 JPY"
// Line 2: "2024-01-15 open Assets:Bank USD"
// Line 3: "2024-01-15 * "Buy"\n"
// Line 4: "  Assets:Bank  -100 USD"  → USD at char 20.
func TestHover_Commodity_WithPrice(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-10 price USD 110.50 JPY
2024-01-15 open Assets:Bank USD
2024-01-15 * "Buy"
  Assets:Bank  -100 USD
  Expenses:Food  100 USD
2024-01-15 open Expenses:Food USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// "  Assets:Bank  -100 USD" → USD starts at char 20.
	h := awaitHover(t, client, docURI, 4, 20)

	if h == nil {
		t.Fatal("handleHover: Commodity_WithPrice: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "110.50") {
		t.Errorf("handleHover: Commodity_WithPrice: markdown missing price:\n%s", md)
	}
	if !strings.Contains(md, "JPY") {
		t.Errorf("handleHover: Commodity_WithPrice: markdown missing price currency:\n%s", md)
	}
	if !strings.Contains(md, "2024-01-10") {
		t.Errorf("handleHover: Commodity_WithPrice: markdown missing price date:\n%s", md)
	}
}

// TestHover_Commodity_NoPriceForDate: a price for USD exists only AFTER the
// transaction date, so hover should say "No price recorded".
func TestHover_Commodity_NoPriceForDate(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-20 price USD 115 JPY
2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Buy"
  Assets:Bank  -50 USD
  Expenses:Food  50 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 5: "  Assets:Bank  -50 USD" → USD at char 19.
	h := awaitHover(t, client, docURI, 5, 19)

	if h == nil {
		t.Fatal("handleHover: Commodity_NoPriceForDate: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "No price recorded") {
		t.Errorf("handleHover: Commodity_NoPriceForDate: got:\n%s\nwant: contains 'No price recorded'", md)
	}
}

// TestHover_Commodity_NoMetadata: Commodity USD with no Meta → no Metadata section.
func TestHover_Commodity_NoMetadata(t *testing.T) {
	dir := t.TempDir()
	const src = "2024-01-01 commodity USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	h := awaitHover(t, client, docURI, 0, 21)

	if h == nil {
		t.Fatal("handleHover: Commodity_NoMetadata: got nil, want hover result")
	}
	md := h.Contents.Value
	if strings.Contains(md, "*Metadata*") {
		t.Errorf("handleHover: Commodity_NoMetadata: Metadata section present but should be absent:\n%s", md)
	}
}

// TestHover_NoToken: cursor on whitespace → nil result.
// "2024-01-01 open Assets:Bank USD\n"
// The double-space gap between "open" and "Assets" is at chars 15 (one space).
// We put the cursor at char 14 (the space after "open ").
func TestHover_NoToken(t *testing.T) {
	dir := t.TempDir()
	// Two spaces between "open" and "Assets:Bank" so there is a byte
	// that falls outside any token range.
	const src = "2024-01-01 open  Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// char 15 is the second space — outside any token.
	h := callHover(t, client, docURI, 0, 15)
	if h != nil {
		t.Errorf("handleHover: NoToken: got hover result on whitespace, want nil:\n%s", h.Contents.Value)
	}
}

// TestHover_ContextDate_FromTransaction: verifies context date equals
// transaction date, not the server clock.  A price exists only on 2024-01-10;
// the transaction is on 2024-01-15; the server clock is set to 2099-12-31.
// Cursor on USD in the transaction posting should use 2024-01-15 as context
// date and find the 2024-01-10 price.
func TestHover_ContextDate_FromTransaction(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-10 price USD 99 JPY
2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Buy"
  Assets:Bank  -10 USD
  Expenses:Food  10 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)

	fixedClock := func() time.Time {
		t2, _ := time.Parse("2006-01-02", "2099-12-31")
		return t2
	}
	client := newHoverServerWithClock(t, rootFile, fixedClock)
	docURI := uri.File(rootFile)

	// Line 5: "  Assets:Bank  -10 USD" → USD at char 19.
	h := awaitHover(t, client, docURI, 5, 19)

	if h == nil {
		t.Fatal("handleHover: ContextDate_FromTransaction: got nil, want hover result")
	}
	md := h.Contents.Value
	// Context date is txn date 2024-01-15; price from 2024-01-10 is on or before.
	if !strings.Contains(md, "99") {
		t.Errorf("handleHover: ContextDate_FromTransaction: price should be visible (context date from txn), got:\n%s", md)
	}
	// The "As of" date should be the txn date, not the far-future clock date.
	if strings.Contains(md, "2099") {
		t.Errorf("handleHover: ContextDate_FromTransaction: clock date 2099 leaked into hover, got:\n%s", md)
	}
}

// TestHover_Commodity_SameDateTiebreak: two Price entries for the same currency
// on the same date — the later canonical-order entry (110 JPY) must win.
func TestHover_Commodity_SameDateTiebreak(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-10 price USD 100 JPY
2024-01-10 price USD 110 JPY
2024-01-15 open Assets:Bank USD
2024-01-15 open Expenses:Food USD
2024-01-15 * "Buy"
  Assets:Bank  -1 USD
  Expenses:Food  1 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 6: "  Assets:Bank  -1 USD" → USD at char 18.
	h := awaitHover(t, client, docURI, 6, 18)

	if h == nil {
		t.Fatal("handleHover: Commodity_SameDateTiebreak: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "110") {
		t.Errorf("handleHover: Commodity_SameDateTiebreak: want later same-date price (110), got:\n%s", md)
	}
	if strings.Contains(md, "100 JPY") {
		t.Errorf("handleHover: Commodity_SameDateTiebreak: earlier same-date price (100) should not win, got:\n%s", md)
	}
}

// TestHover_Commodity_OnPriceDirective: cursor on the CURRENCY token in a price
// directive itself should show hover with that commodity's price info.
//
// Line 1: "2024-01-10 price USD 110.50 JPY" → USD at char 17.
func TestHover_Commodity_OnPriceDirective(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-10 price USD 110.50 JPY
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newHoverServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Line 1: "2024-01-10 price USD 110.50 JPY" → USD at char 17.
	h := awaitHover(t, client, docURI, 1, 17)

	if h == nil {
		t.Fatal("handleHover: Commodity_OnPriceDirective: got nil, want hover result")
	}
	md := h.Contents.Value
	if !strings.Contains(md, "**USD**") {
		t.Errorf("handleHover: Commodity_OnPriceDirective: missing currency name, got:\n%s", md)
	}
	if !strings.Contains(md, "110.50") {
		t.Errorf("handleHover: Commodity_OnPriceDirective: missing price amount, got:\n%s", md)
	}
}

// TestHover_ContextDate_FromDirective: verifies that context date comes from the
// directive's own date, not the server clock. A price exists on 2024-01-01; the
// commodity directive is dated 2024-12-01 (after the price). The clock is set to
// 2024-06-01. Hovering on USD in the commodity directive should use 2024-12-01
// as context date, making the 2024-01-01 price visible.
//
// Note: undated directives (option, plugin, include) do not produce CURRENCY
// tokens, so clock fallback cannot be tested via a CURRENCY hover. The clock
// path is covered by contextDateFor returning s.clock() when dateFromSyntaxNode
// finds no DATE child.
func TestHover_ContextDate_FromDirective(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 price USD 50 JPY
2024-12-01 commodity USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)

	fixedClock := func() time.Time {
		t2, _ := time.Parse("2006-01-02", "2024-06-01")
		return t2
	}
	client := newHoverServerWithClock(t, rootFile, fixedClock)
	docURI := uri.File(rootFile)

	// Line 1: "2024-12-01 commodity USD" → USD at char 21.
	h := awaitHover(t, client, docURI, 1, 21)

	if h == nil {
		t.Fatal("handleHover: ContextDate_FromDirective: got nil, want hover result")
	}
	md := h.Contents.Value
	// Context date is directive date 2024-12-01; price from 2024-01-01 is visible.
	if !strings.Contains(md, "50") {
		t.Errorf("handleHover: ContextDate_FromDirective: price should be visible, got:\n%s", md)
	}
	// The "As of" date should be 2024-12-01, not the clock date 2024-06-01.
	if strings.Contains(md, "2024-06-01") {
		t.Errorf("handleHover: ContextDate_FromDirective: clock date 2024-06-01 leaked into hover, got:\n%s", md)
	}
	if !strings.Contains(md, "2024-12-01") {
		t.Errorf("handleHover: ContextDate_FromDirective: directive date 2024-12-01 missing from hover, got:\n%s", md)
	}
}
