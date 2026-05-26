package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestSmoke_EndToEnd exercises the full LSP server lifecycle against a real
// beancount workspace loaded by a real session (no stubs). It verifies
// initialize, didOpen, publishDiagnostics, formatting, documentSymbol,
// shutdown, and exit in sequence.
func TestSmoke_EndToEnd(t *testing.T) {
	const timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// --- workspace setup ---
	dir := t.TempDir()

	subPath := filepath.Join(dir, "sub.beancount")
	if err := os.WriteFile(subPath, []byte(`2024-01-01 open Expenses:Food USD
`), 0o644); err != nil {
		t.Fatalf("write sub.beancount: %v", err)
	}

	mainContent := `option "title" "Smoke Test"

include "sub.beancount"

2024-01-01 open Assets:Cash USD

2024-06-01 * "Groceries"
  Assets:Cash        -50.00 USD
  Expenses:Food       50.00 USD
`
	mainPath := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("write main.beancount: %v", err)
	}

	// --- server + client wiring via net.Pipe ---
	srv := NewServer(WithDebounce(0))

	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(ctx, jsonrpc2.NewStream(serverConn))
	}()
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("server did not exit within cleanup deadline")
		}
	})

	// diagCh receives publishDiagnostics params as they arrive.
	diagCh := make(chan *protocol.PublishDiagnosticsParams, 20)

	clientHandler := jsonrpc2.Handler(func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		_ = reply(ctx, nil, nil)
		if req.Method() != "textDocument/publishDiagnostics" {
			return nil
		}
		var params protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(req.Params(), &params); err != nil {
			return nil
		}
		select {
		case diagCh <- &params:
		default:
		}
		return nil
	})

	cc := jsonrpc2.NewConn(jsonrpc2.NewStream(clientConn))
	go func() {
		cc.Go(ctx, jsonrpc2.AsyncHandler(clientHandler))
		<-cc.Done()
	}()

	client := &lspClient{conn: cc}

	call := func(method string, params, result any) {
		t.Helper()
		if err := client.call(ctx, method, params, result); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	notify := func(method string, params any) {
		t.Helper()
		if err := client.notify(ctx, method, params); err != nil {
			t.Fatalf("%s notify: %v", method, err)
		}
	}

	// --- initialize ---
	var initResult protocol.InitializeResult
	call("initialize", &protocol.InitializeParams{
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{URI: string(uri.File(dir)), Name: "smoke"},
		},
	}, &initResult)

	if initResult.ServerInfo == nil {
		t.Errorf("TestSmoke_EndToEnd: ServerInfo = nil, want non-nil")
	} else if initResult.ServerInfo.Name != "beancount-lsp" {
		t.Errorf("TestSmoke_EndToEnd: ServerInfo.Name = %q, want beancount-lsp", initResult.ServerInfo.Name)
	}

	// --- initialized notification ---
	notify("initialized", &protocol.InitializedParams{})

	// --- textDocument/didOpen ---
	mainURI := uri.File(mainPath)
	notify("textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     mainURI,
			Version: 1,
			Text:    mainContent,
		},
	})

	// --- await publishDiagnostics ---
	// The session publishes diagnostics after every reload triggered by the
	// overlay set in didOpen. A well-formed file produces a notification with an
	// empty Diagnostics slice (zero errors). Wait for the first notification
	// whose URI matches main.beancount.
	awaitDiag := func() *protocol.PublishDiagnosticsParams {
		t.Helper()
		deadline := time.After(timeout)
		for {
			select {
			case p := <-diagCh:
				if p.URI == mainURI {
					return p
				}
			case <-deadline:
				t.Fatal("timed out waiting for publishDiagnostics for main.beancount")
			}
		}
	}
	diag := awaitDiag()
	if len(diag.Diagnostics) != 0 {
		t.Errorf("publishDiagnostics: %d unexpected diagnostics: %v", len(diag.Diagnostics), diag.Diagnostics)
	}

	// --- textDocument/formatting ---
	var fmtRaw json.RawMessage
	call("textDocument/formatting", &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: mainURI},
		Options:      protocol.FormattingOptions{},
	}, &fmtRaw)
	// A well-formatted document returns an empty array.
	var edits []protocol.TextEdit
	if err := json.Unmarshal(fmtRaw, &edits); err != nil {
		t.Fatalf("formatting unmarshal: %v", err)
	}
	// Response is valid JSON (empty array or one edit); just verify it parsed.

	// --- textDocument/documentSymbol ---
	// Retry until the real session has loaded the ledger and produced symbols.
	awaitSymbols := func(wantMin int) []protocol.DocumentSymbol {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			var symRaw json.RawMessage
			call("textDocument/documentSymbol", &protocol.DocumentSymbolParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: mainURI},
			}, &symRaw)
			var syms []protocol.DocumentSymbol
			if err := json.Unmarshal(symRaw, &syms); err != nil {
				t.Fatalf("documentSymbol unmarshal: %v", err)
			}
			if len(syms) >= wantMin || time.Now().After(deadline) {
				return syms
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	syms := awaitSymbols(1)
	if len(syms) == 0 {
		t.Error("documentSymbol: expected at least one symbol, got none")
	}

	// --- shutdown + exit ---
	call("shutdown", nil, nil)
	notify("exit", nil)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after shutdown+exit")
	}

	srv.mu.Lock()
	code := srv.exitCode
	srv.mu.Unlock()
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (clean shutdown)", code)
	}
}
