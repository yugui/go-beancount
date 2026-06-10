package main

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// callReferences sends textDocument/references and returns the location array.
func callReferences(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32, includeDecl bool) []protocol.Location {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/references", &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
			Position:     protocol.Position{Line: line, Character: char},
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: includeDecl},
	}, &raw); err != nil {
		t.Fatalf("handleReferences: call error: %v", err)
	}
	if string(raw) == "null" {
		t.Fatalf("handleReferences: result is JSON null, want []")
	}
	var locs []protocol.Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		t.Fatalf("handleReferences: unmarshal: %v", err)
	}
	return locs
}

// awaitReferences retries callReferences until at least wantCount locations are
// returned or the 3 s deadline elapses, guarding against the session snapshot
// not yet being available right after initialize.
func awaitReferences(t *testing.T, client *lspClient, docURI uri.URI, line, char uint32, includeDecl bool, wantCount int) []protocol.Location {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		locs := callReferences(t, client, docURI, line, char, includeDecl)
		if len(locs) >= wantCount || time.Now().After(deadline) {
			return locs
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// refStart is a location's (uri, start-line, start-char), the identity asserted
// by the reference tests.
type refStart struct {
	uri  string
	line uint32
	char uint32
}

// startsOf projects locations to sorted refStart tuples for order-independent
// assertions.
func startsOf(locs []protocol.Location) []refStart {
	out := make([]refStart, len(locs))
	for i, l := range locs {
		out[i] = refStart{string(l.URI), l.Range.Start.Line, l.Range.Start.Character}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.uri != b.uri:
			return a.uri < b.uri
		case a.line != b.line:
			return a.line < b.line
		default:
			return a.char < b.char
		}
	})
	return out
}

func TestReferences_Account_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Cash USD
2024-01-01 open Assets:Cash:Sub USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Lunch"
  Assets:Cash  -5 USD
  Expenses:Food  5 USD
2024-01-31 balance Assets:Cash 95 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "Assets:Cash" in the open on line 0 (char 16).
	locs := awaitReferences(t, client, docURI, 0, 16, true, 3)

	// Exact match excludes Assets:Cash:Sub on line 1. Expected occurrences:
	//   line 0 open, line 4 posting, line 6 balance.
	if got := len(locs); got != 3 {
		t.Errorf("Account_ExactMatch: got %d locations, want 3: %+v", got, locs)
	}
	want := []refStart{
		{string(docURI), 0, 16},
		{string(docURI), 4, 2},
		{string(docURI), 6, 19},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Account_ExactMatch: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_Account_ExcludeDeclaration(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Cash USD
2024-01-15 * "Lunch"
  Assets:Cash  -5 USD
  Expenses:Food  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Wait until both occurrences are visible, then re-query with
	// IncludeDeclaration=false.
	awaitReferences(t, client, docURI, 0, 16, true, 2)
	locs := callReferences(t, client, docURI, 0, 16, false)

	// The open token on line 0 is dropped; only the posting on line 2 remains.
	want := []refStart{
		{string(docURI), 2, 2},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Account_ExcludeDeclaration: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_Commodity_AllOccurrences(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 commodity USD
2024-01-01 open Assets:Cash USD
2024-01-15 * "Lunch"
  Assets:Cash  -5 USD
  Expenses:Food  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "USD" in the commodity directive (line 0, char 21).
	locs := awaitReferences(t, client, docURI, 0, 21, true, 4)

	// USD source tokens: commodity (line 0), open (line 1), two postings
	// (lines 3, 4).
	want := []refStart{
		{string(docURI), 0, 21},
		{string(docURI), 1, 28},
		{string(docURI), 3, 18},
		{string(docURI), 4, 19},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Commodity_AllOccurrences: starts mismatch (-want +got):\n%s", diff)
	}

	// IncludeDeclaration=false drops the commodity-directive token on line 0.
	locsNoDecl := callReferences(t, client, docURI, 0, 21, false)
	wantNoDecl := []refStart{
		{string(docURI), 1, 28},
		{string(docURI), 3, 18},
		{string(docURI), 4, 19},
	}
	if diff := cmp.Diff(wantNoDecl, startsOf(locsNoDecl), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Commodity_AllOccurrences no-decl: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_Tag_InlineAndImplied(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
pushtag #trip
2024-01-10 * "A"
  Assets:Bank  -1 USD
  Expenses:Food  1 USD
2024-01-11 * "B" #trip
  Assets:Bank  -2 USD
  Expenses:Food  2 USD
poptag #trip
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on the inline "#trip" in transaction B (line 6, char 17).
	locs := awaitReferences(t, client, docURI, 6, 17, true, 4)

	// (A) directive header ranges for the two transactions carrying tag trip:
	//   line 3 (implied via pushtag) and line 6 (inline + implied), each at char 0.
	// (B) the #trip tokens inside pushtag (line 2 char 8) and poptag (line 9 char 7).
	// A and B are disjoint; dedup keeps each once.
	want := []refStart{
		{string(docURI), 2, 8},
		{string(docURI), 3, 0},
		{string(docURI), 6, 0},
		{string(docURI), 9, 7},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Tag_InlineAndImplied: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_Tag_Document(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 document Assets:Bank "/receipt.pdf" #scan
2024-01-10 * "Expense" #scan
  Assets:Bank  -5 USD
  Expenses:Food  5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "#scan" in the document directive (line 1, char 47).
	locs := awaitReferences(t, client, docURI, 1, 47, true, 2)

	// (A) directive header ranges: document (line 1) and transaction (line 2), each at char 0.
	want := []refStart{
		{string(docURI), 1, 0},
		{string(docURI), 2, 0},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Tag_Document: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_Link_InlineOnly(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-11 * "B" ^inv
  Assets:Bank  -2 USD
  Expenses:Food  2 USD
2024-01-12 note Assets:Bank "see ^inv" ^inv
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "^inv" in transaction B (line 2, char 17).
	locs := awaitReferences(t, client, docURI, 2, 17, true, 2)

	// Two directive header ranges: the transaction (line 2) and the note
	// (line 5), each at char 0. No pushlink construct exists, so source (B) is empty.
	want := []refStart{
		{string(docURI), 2, 0},
		{string(docURI), 5, 0},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("Link_InlineOnly: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_CursorNotOnSymbol(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Cash USD
2024-01-15 * "Lunch"
  Assets:Cash  -5 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Char 15 on line 2 is on the NUMBER token "-5", not a symbol.
	locs := callReferences(t, client, docURI, 2, 15, true)
	if len(locs) != 0 {
		t.Errorf("CursorNotOnSymbol (number): got %d locations, want 0: %+v", len(locs), locs)
	}

	// The date token on line 1 is not a reference symbol either.
	locs = callReferences(t, client, docURI, 1, 0, true)
	if len(locs) != 0 {
		t.Errorf("CursorNotOnSymbol (date): got %d locations, want 0: %+v", len(locs), locs)
	}
}

// TestReferences_SyntheticCommodity verifies that a currency inferred by booking
// on an auto-balanced posting (the second posting omits the amount) does not
// appear as a reference: only source-written USD tokens are returned.
func TestReferences_SyntheticCommodity(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Expenses:Food USD
2024-01-15 * "Dinner"
  Assets:Bank  -20 USD
  Expenses:Food
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "USD" in the first open (line 0, char 28).
	locs := awaitReferences(t, client, docURI, 0, 28, true, 3)

	// Source USD tokens: two opens (lines 0, 1) and the one posting (line 3).
	// The inferred USD on line 4's auto-balanced posting carries no token.
	want := []refStart{
		{string(docURI), 0, 28},
		{string(docURI), 1, 30},
		{string(docURI), 3, 19},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("SyntheticCommodity: starts mismatch (-want +got):\n%s", diff)
	}
}

// TestReferences_SyntheticPad verifies that the transaction synthesized by a
// pad/balance pair contributes no references: the pad-synthesized postings carry
// no source tokens, so only literal account/commodity tokens are returned.
func TestReferences_SyntheticPad(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-01-01 open Equity:Open USD
2024-02-01 pad Assets:Bank Equity:Open
2024-02-28 balance Assets:Bank 100 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	// Cursor on "Assets:Bank" in the open (line 0, char 16).
	locs := awaitReferences(t, client, docURI, 0, 16, true, 3)

	// Source Assets:Bank tokens: open (line 0), pad (line 2), balance (line 3).
	// The pad-synthesized transaction's postings carry no source token.
	want := []refStart{
		{string(docURI), 0, 16},
		{string(docURI), 2, 15},
		{string(docURI), 3, 19},
	}
	if diff := cmp.Diff(want, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("SyntheticPad: starts mismatch (-want +got):\n%s", diff)
	}
}

func TestReferences_MultiFile(t *testing.T) {
	dir := t.TempDir()
	const subSrc = `2024-01-05 * "Sub" ^inv
  Assets:Cash  -3 USD
  Expenses:Food  3 USD
`
	subFile := writeTempFile(t, dir, "sub.beancount", subSrc)
	mainSrc := `include "sub.beancount"
2024-01-01 open Assets:Cash USD
2024-01-01 open Expenses:Food USD
2024-01-10 * "Main" ^inv
  Assets:Cash  -4 USD
  Expenses:Food  4 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", mainSrc)
	client := newSymbolServer(t, rootFile)
	mainURI := uri.File(rootFile)
	subURI := uri.File(subFile)

	// Cursor on "^inv" in main's transaction (line 3, char 20).
	locs := awaitReferences(t, client, mainURI, 3, 20, true, 2)

	// One directive header range per transaction: sub (line 0) and main (line 3).
	if got := len(locs); got != 2 {
		t.Errorf("MultiFile link: got %d locations, want 2: %+v", got, locs)
	}
	wantLink := []refStart{
		{string(mainURI), 3, 0},
		{string(subURI), 0, 0},
	}
	if diff := cmp.Diff(wantLink, startsOf(locs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("MultiFile link: starts mismatch (-want +got):\n%s", diff)
	}

	// Account in the included file resolves with the correct URI.
	// Cursor on "Assets:Cash" in main's open (line 1, char 16).
	// Expected: open (main line 1), posting (main line 4), posting (sub line 1).
	accLocs := awaitReferences(t, client, mainURI, 1, 16, true, 3)
	wantAcc := []refStart{
		{string(mainURI), 1, 16},
		{string(mainURI), 4, 2},
		{string(subURI), 1, 2},
	}
	if diff := cmp.Diff(wantAcc, startsOf(accLocs), cmpopts.EquateComparable(refStart{})); diff != "" {
		t.Errorf("MultiFile account: starts mismatch (-want +got):\n%s", diff)
	}
}
