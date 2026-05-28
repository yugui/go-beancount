package main

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// newTestPairWithClientHandler creates a server + client pair where the client
// uses a custom handler (e.g. to intercept server-initiated requests).
// Cleanup (closing streams and waiting for the server goroutine) is registered
// with t.Cleanup.
func newTestPairWithClientHandler(t *testing.T, srv *Server, clientHandler jsonrpc2.Handler) *lspClient {
	t.Helper()
	serverConn, clientConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(context.Background(), jsonrpc2.NewStream(serverConn))
	}()

	cc := jsonrpc2.NewConn(jsonrpc2.NewStream(clientConn))
	cc.Go(context.Background(), clientHandler)

	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		<-done
	})

	return &lspClient{conn: cc}
}

// initParamsWithWatchDynamic returns InitializeParams with
// Workspace.DidChangeWatchedFiles.DynamicRegistration set to the given value.
func initParamsWithWatchDynamic(rootURI uri.URI, dynamic bool) *protocol.InitializeParams {
	return &protocol.InitializeParams{
		RootURI: rootURI,
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{
				DidChangeWatchedFiles: &protocol.DidChangeWatchedFilesWorkspaceClientCapabilities{
					DynamicRegistration: dynamic,
				},
			},
		},
	}
}

func TestRegisterFileWatcher_ClientSupportsDynamic(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))

	// registerCh is closed when client/registerCapability arrives.
	registerCh := make(chan struct{})
	var registerCalled atomic.Bool

	clientHandler := jsonrpc2.Handler(func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() == "client/registerCapability" {
			if registerCalled.CompareAndSwap(false, true) {
				close(registerCh)
			}
			return reply(ctx, nil, nil)
		}
		return reply(ctx, nil, nil)
	})

	client := newTestPairWithClientHandler(t, srv, clientHandler)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initParamsWithWatchDynamic("file:///tmp", true), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := client.notify(ctx, "initialized", &protocol.InitializedParams{}); err != nil {
		t.Fatalf("initialized: %v", err)
	}

	select {
	case <-registerCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for client/registerCapability")
	}
}

func TestRegisterFileWatcher_ClientLacksDynamic(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))

	var registerCalled atomic.Bool
	clientHandler := jsonrpc2.Handler(func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() == "client/registerCapability" {
			registerCalled.Store(true)
			return reply(ctx, nil, nil)
		}
		return reply(ctx, nil, nil)
	})

	client := newTestPairWithClientHandler(t, srv, clientHandler)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initParamsWithWatchDynamic("file:///tmp", false), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := client.notify(ctx, "initialized", &protocol.InitializedParams{}); err != nil {
		t.Fatalf("initialized: %v", err)
	}

	// Give the server a window to send the request if it (incorrectly) would.
	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if registerCalled.Load() {
		t.Error("client/registerCapability should not be sent when client lacks DynamicRegistration")
	}
}

func TestDidChangeWatchedFiles_CreatedNoOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	fileURI := uri.URI("file:///tmp/new.beancount")
	// No overlay in docStore — Created event should trigger Reload.
	if err := client.notify(ctx, "workspace/didChangeWatchedFiles", &protocol.DidChangeWatchedFilesParams{
		Changes: []*protocol.FileEvent{
			{URI: fileURI, Type: protocol.FileChangeTypeCreated},
		},
	}); err != nil {
		t.Fatalf("didChangeWatchedFiles: %v", err)
	}

	stub.awaitReload(t, 3*time.Second)
}

func TestDidChangeWatchedFiles_ChangedWithOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Open the file so it exists in docStore as an overlay.
	fileURI := uri.URI("file:///tmp/open.beancount")
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     fileURI,
			Version: 1,
			Text:    "2024-01-01 open Assets:Bank USD\n",
		},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	stub.awaitSet(t, 3*time.Second)

	// Drain the reloadCh so any prior Reload doesn't interfere.
	for {
		select {
		case <-stub.reloadCh:
			continue
		default:
		}
		break
	}

	stub.mu.Lock()
	reloadsBefore := stub.reloadCalls
	stub.mu.Unlock()

	// Changed event for a file with an overlay — should NOT trigger Reload.
	if err := client.notify(ctx, "workspace/didChangeWatchedFiles", &protocol.DidChangeWatchedFilesParams{
		Changes: []*protocol.FileEvent{
			{URI: fileURI, Type: protocol.FileChangeTypeChanged},
		},
	}); err != nil {
		t.Fatalf("didChangeWatchedFiles: %v", err)
	}

	// Wait long enough for any spurious Reload to arrive.
	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	stub.mu.Lock()
	reloadsAfter := stub.reloadCalls
	stub.mu.Unlock()

	if reloadsAfter != reloadsBefore {
		t.Errorf("Reload called %d times (want 0) for Changed event with overlay", reloadsAfter-reloadsBefore)
	}
}

func TestDidChangeWatchedFiles_ChangedNoOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	fileURI := uri.URI("file:///tmp/changed.beancount")
	// No overlay in docStore — Changed event should trigger Reload.
	if err := client.notify(ctx, "workspace/didChangeWatchedFiles", &protocol.DidChangeWatchedFilesParams{
		Changes: []*protocol.FileEvent{
			{URI: fileURI, Type: protocol.FileChangeTypeChanged},
		},
	}); err != nil {
		t.Fatalf("didChangeWatchedFiles: %v", err)
	}

	stub.awaitReload(t, 3*time.Second)

	// ClearOverlay must not have been called (no overlay to clear).
	stub.mu.Lock()
	clearCalls := stub.clearCalls
	stub.mu.Unlock()
	if len(clearCalls) != 0 {
		t.Errorf("ClearOverlay called %d times (want 0) for Changed event without overlay", len(clearCalls))
	}
}

func TestDidChangeWatchedFiles_DeletedNoOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	fileURI := uri.URI("file:///tmp/gone.beancount")
	// File was never opened — Deleted event should trigger Reload but not ClearOverlay.
	if err := client.notify(ctx, "workspace/didChangeWatchedFiles", &protocol.DidChangeWatchedFilesParams{
		Changes: []*protocol.FileEvent{
			{URI: fileURI, Type: protocol.FileChangeTypeDeleted},
		},
	}); err != nil {
		t.Fatalf("didChangeWatchedFiles: %v", err)
	}

	stub.awaitReload(t, 3*time.Second)

	// ClearOverlay must not have been called (file had no overlay).
	stub.mu.Lock()
	clearCalls := stub.clearCalls
	stub.mu.Unlock()
	if len(clearCalls) != 0 {
		t.Errorf("ClearOverlay called %d times (want 0) for Deleted event without overlay", len(clearCalls))
	}
}

func TestDidChangeWatchedFiles_Deleted(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Open the file so it exists in docStore as an overlay.
	fileURI := uri.URI("file:///tmp/toDelete.beancount")
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     fileURI,
			Version: 1,
			Text:    "2024-01-01 open Assets:Bank USD\n",
		},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	stub.awaitSet(t, 3*time.Second)

	// Deleted event with an overlay → ClearOverlay + Reload.
	if err := client.notify(ctx, "workspace/didChangeWatchedFiles", &protocol.DidChangeWatchedFilesParams{
		Changes: []*protocol.FileEvent{
			{URI: fileURI, Type: protocol.FileChangeTypeDeleted},
		},
	}); err != nil {
		t.Fatalf("didChangeWatchedFiles: %v", err)
	}

	stub.awaitClear(t, 3*time.Second)
	stub.awaitReload(t, 3*time.Second)

	// docStore must no longer hold the entry.
	if _, ok := srv.docs.get(fileURI); ok {
		t.Error("docStore still has entry after Deleted event")
	}
}
