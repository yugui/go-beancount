package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/session"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// callDocumentSymbol calls textDocument/documentSymbol and unmarshals the result.
// Calls t.Fatal on error.
func callDocumentSymbol(t *testing.T, client *lspClient, docURI uri.URI) []protocol.DocumentSymbol {
	t.Helper()
	ctx := context.Background()
	var raw json.RawMessage
	if err := client.call(ctx, "textDocument/documentSymbol", &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	}, &raw); err != nil {
		t.Fatalf("handleDocumentSymbol: call error: %v", err)
	}
	var symbols []protocol.DocumentSymbol
	if err := json.Unmarshal(raw, &symbols); err != nil {
		t.Fatalf("handleDocumentSymbol: unmarshal: %v", err)
	}
	return symbols
}

// newSymbolServer creates a server+client backed by a real session loading rootFile.
// rootFile is an absolute path to a .beancount file already written to disk.
func newSymbolServer(t *testing.T, rootFile string) *lspClient {
	t.Helper()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) {
		return session.New(rootFile)
	}))
	client, _, _ := newDiagTestPair(t, srv)
	ctx := context.Background()
	if err := client.call(ctx, "initialize", initializeParams(uri.File(filepath.Dir(rootFile))), nil); err != nil {
		t.Fatalf("handleDocumentSymbol: initialize: %v", err)
	}
	return client
}

// writeTempFile writes content to a temp file under dir and returns the absolute path.
// Calls t.Fatal on error.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("handleDocumentSymbol: write temp file: %v", err)
	}
	return path
}

// awaitDocumentSymbol retries callDocumentSymbol until wantCount symbols are
// returned or 3 seconds elapse. The retry guards against the session not yet
// being set after initialize when using the lazy-creation path.
func awaitDocumentSymbol(t *testing.T, client *lspClient, docURI uri.URI, wantCount int) []protocol.DocumentSymbol {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		syms := callDocumentSymbol(t, client, docURI)
		if len(syms) >= wantCount || time.Now().After(deadline) {
			return syms
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDocumentSymbol_BasicDirectives verifies that an Open, Transaction, and
// Commodity each produce a root symbol with the correct name and kind.
func TestDocumentSymbol_BasicDirectives(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-01 open Assets:Bank USD
2024-06-01 commodity EUR
2024-01-15 * "Groceries"
  Assets:Bank  -50 USD
  Expenses:Food  50 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	symbols := awaitDocumentSymbol(t, client, docURI, 3)

	if len(symbols) != 3 {
		t.Fatalf("handleDocumentSymbol: BasicDirectives: got %d symbols, want 3", len(symbols))
	}

	// Find by kind for robustness against directive ordering.
	byKind := make(map[protocol.SymbolKind]protocol.DocumentSymbol)
	for _, s := range symbols {
		if _, dup := byKind[s.Kind]; dup {
			t.Fatalf("handleDocumentSymbol: duplicate Kind %v in symbols", s.Kind)
		}
		byKind[s.Kind] = s
	}

	open, ok := byKind[protocol.SymbolKindClass]
	if !ok {
		t.Fatal("handleDocumentSymbol: BasicDirectives: no Class symbol (Open)")
	}
	if open.Name != "Assets:Bank" {
		t.Errorf("handleDocumentSymbol: BasicDirectives: Open.Name = %q, want %q", open.Name, "Assets:Bank")
	}

	comm, ok := byKind[protocol.SymbolKindConstant]
	if !ok {
		t.Fatal("handleDocumentSymbol: BasicDirectives: no Constant symbol (Commodity)")
	}
	if comm.Name != "EUR" {
		t.Errorf("handleDocumentSymbol: BasicDirectives: Commodity.Name = %q, want %q", comm.Name, "EUR")
	}

	txn, ok := byKind[protocol.SymbolKindEvent]
	if !ok {
		t.Fatal("handleDocumentSymbol: BasicDirectives: no Event symbol (Transaction)")
	}
	if txn.Name != "Groceries" {
		t.Errorf("handleDocumentSymbol: BasicDirectives: Transaction.Name = %q, want %q", txn.Name, "Groceries")
	}
}

// TestDocumentSymbol_TransactionWithPostings verifies that a transaction
// produces child Field symbols for each posting.
func TestDocumentSymbol_TransactionWithPostings(t *testing.T) {
	dir := t.TempDir()
	const src = `2024-01-15 * "Dinner"
  Assets:Cash  -20 USD
  Expenses:Food  20 USD
`
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	symbols := awaitDocumentSymbol(t, client, docURI, 1)

	if len(symbols) != 1 {
		t.Fatalf("handleDocumentSymbol: TransactionWithPostings: got %d root symbols, want 1", len(symbols))
	}
	txn := symbols[0]
	if txn.Kind != protocol.SymbolKindEvent {
		t.Errorf("handleDocumentSymbol: TransactionWithPostings: Kind = %v, want Event", txn.Kind)
	}
	if len(txn.Children) != 2 {
		t.Fatalf("handleDocumentSymbol: TransactionWithPostings: got %d children, want 2", len(txn.Children))
	}
	accounts := map[string]bool{
		"Assets:Cash":   false,
		"Expenses:Food": false,
	}
	for _, ch := range txn.Children {
		if ch.Kind != protocol.SymbolKindField {
			t.Errorf("handleDocumentSymbol: TransactionWithPostings: child Kind = %v, want Field", ch.Kind)
		}
		if _, ok := accounts[ch.Name]; ok {
			accounts[ch.Name] = true
		} else {
			t.Errorf("handleDocumentSymbol: TransactionWithPostings: unexpected child name %q", ch.Name)
		}
	}
	for acc, found := range accounts {
		if !found {
			t.Errorf("handleDocumentSymbol: TransactionWithPostings: missing child %q", acc)
		}
	}
	for i, c := range txn.Children {
		if c.Range.Start.Line < txn.Range.Start.Line || c.Range.End.Line > txn.Range.End.Line {
			t.Errorf("handleDocumentSymbol: child %d Range %v escapes parent Range %v", i, c.Range, txn.Range)
		}
	}
}

// TestDocumentSymbol_EmptyFile verifies that a file with no directives returns
// an empty symbol array.
func TestDocumentSymbol_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	rootFile := writeTempFile(t, dir, "main.beancount", "")
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	symbols := callDocumentSymbol(t, client, docURI)

	if len(symbols) != 0 {
		t.Errorf("handleDocumentSymbol: EmptyFile: got %d symbols, want 0", len(symbols))
	}
}

// TestDocumentSymbol_UnknownURI verifies that a request for a URI not present
// in the ledger returns an empty array.
func TestDocumentSymbol_UnknownURI(t *testing.T) {
	dir := t.TempDir()
	rootFile := writeTempFile(t, dir, "main.beancount", "2024-01-01 open Assets:Bank USD\n")
	client := newSymbolServer(t, rootFile)

	// Request a URI that does not correspond to any loaded file.
	unknownURI := uri.File(filepath.Join(dir, "nonexistent.beancount"))
	symbols := callDocumentSymbol(t, client, unknownURI)

	if len(symbols) != 0 {
		t.Errorf("handleDocumentSymbol: UnknownURI: got %d symbols, want 0", len(symbols))
	}
}

// TestDocumentSymbol_RangeMatchesDirective verifies that the returned Range
// covers the directive: the start line should be 0-based line of the directive
// and end should be >= start.
func TestDocumentSymbol_RangeMatchesDirective(t *testing.T) {
	dir := t.TempDir()
	// Single directive on line 0.
	const src = "2024-01-01 open Assets:Bank USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	symbols := awaitDocumentSymbol(t, client, docURI, 1)

	if len(symbols) != 1 {
		t.Fatalf("handleDocumentSymbol: RangeMatchesDirective: got %d symbols, want 1", len(symbols))
	}
	r := symbols[0].Range
	if r.Start.Line != 0 {
		t.Errorf("handleDocumentSymbol: RangeMatchesDirective: Range.Start.Line = %d, want 0", r.Start.Line)
	}
	if r.End.Line < r.Start.Line {
		t.Errorf("handleDocumentSymbol: RangeMatchesDirective: Range.End (%v) before Start (%v)", r.End, r.Start)
	}
	if r.Start.Character != 0 {
		t.Errorf("handleDocumentSymbol: RangeMatchesDirective: Range.Start.Character = %d, want 0", r.Start.Character)
	}
	if r.End.Line != 0 {
		t.Errorf("handleDocumentSymbol: RangeMatchesDirective: Range.End.Line = %d, want 0 (single-line directive)", r.End.Line)
	}
	if r.End.Character <= r.Start.Character {
		t.Errorf("handleDocumentSymbol: RangeMatchesDirective: Range.End.Character (%d) not > Start.Character (%d)", r.End.Character, r.Start.Character)
	}
}

// TestDocumentSymbol_NarrationFallback verifies that when a transaction has a
// payee but empty narration, the payee is used as the symbol name.
func TestDocumentSymbol_NarrationFallback(t *testing.T) {
	dir := t.TempDir()
	// Payee present, narration empty.
	const src = "2024-01-15 * \"BestCafe\" \"\"\n  Assets:Cash  -10 USD\n  Expenses:Food  10 USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	symbols := awaitDocumentSymbol(t, client, docURI, 1)

	if len(symbols) != 1 {
		t.Fatalf("handleDocumentSymbol: NarrationFallback: got %d symbols, want 1", len(symbols))
	}
	if symbols[0].Name != "BestCafe" {
		t.Errorf("handleDocumentSymbol: NarrationFallback: Name = %q, want %q", symbols[0].Name, "BestCafe")
	}
}

// TestDocumentSymbol_AnonymousTransaction verifies that a transaction with
// both payee and narration empty gets the name "(transaction)".
func TestDocumentSymbol_AnonymousTransaction(t *testing.T) {
	dir := t.TempDir()
	// Both payee and narration are empty strings.
	const src = "2024-01-15 * \"\" \"\"\n  Assets:Cash  -5 USD\n  Expenses:Food  5 USD\n"
	rootFile := writeTempFile(t, dir, "main.beancount", src)
	client := newSymbolServer(t, rootFile)
	docURI := uri.File(rootFile)

	symbols := awaitDocumentSymbol(t, client, docURI, 1)

	if len(symbols) != 1 {
		t.Fatalf("handleDocumentSymbol: AnonymousTransaction: got %d symbols, want 1", len(symbols))
	}
	if symbols[0].Name != "(transaction)" {
		t.Errorf("handleDocumentSymbol: AnonymousTransaction: Name = %q, want %q", symbols[0].Name, "(transaction)")
	}
}
