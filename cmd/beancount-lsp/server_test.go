package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// stubSession records calls for testing; setCh/clearCh let tests wait for
// async notifications without time.Sleep.
type stubSession struct {
	mu         sync.Mutex
	overlays   map[string][]byte
	setCalls   []string
	clearCalls []string
	closed     bool
	panicOnSet bool

	setCh     chan struct{}    // signalled on each SetOverlay call
	clearCh   chan struct{}    // signalled on each ClearOverlay call
	subCh     chan *ast.Ledger // Subscribe returns this channel
	closeOnce sync.Once
}

func newStub() *stubSession {
	return &stubSession{
		overlays: make(map[string][]byte),
		setCh:    make(chan struct{}, 10),
		clearCh:  make(chan struct{}, 10),
		subCh:    make(chan *ast.Ledger, 10),
	}
}

func (s *stubSession) SetOverlay(absPath string, content []byte) error {
	if s.panicOnSet {
		panic("stub panic")
	}
	s.mu.Lock()
	s.overlays[absPath] = append([]byte(nil), content...)
	s.setCalls = append(s.setCalls, absPath)
	s.mu.Unlock()
	select {
	case s.setCh <- struct{}{}:
	default:
	}
	return nil
}

func (s *stubSession) ClearOverlay(absPath string) error {
	s.mu.Lock()
	delete(s.overlays, absPath)
	s.clearCalls = append(s.clearCalls, absPath)
	s.mu.Unlock()
	select {
	case s.clearCh <- struct{}{}:
	default:
	}
	return nil
}

func (s *stubSession) Snapshot(_ context.Context) (*ast.Ledger, error) {
	return nil, nil
}

func (s *stubSession) Subscribe() (<-chan *ast.Ledger, func()) {
	return s.subCh, func() { s.closeOnce.Do(func() { close(s.subCh) }) }
}

func (s *stubSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.subCh) })
	return nil
}

// awaitSet waits up to d for a SetOverlay call to arrive, failing t on timeout.
func (s *stubSession) awaitSet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-s.setCh:
	case <-time.After(d):
		t.Fatal("timed out waiting for SetOverlay call")
	}
}

// awaitClear waits up to d for a ClearOverlay call to arrive, failing t on timeout.
func (s *stubSession) awaitClear(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-s.clearCh:
	case <-time.After(d):
		t.Fatal("timed out waiting for ClearOverlay call")
	}
}

// lspClient is a helper that sends LSP messages over a net.Pipe connection.
type lspClient struct {
	conn jsonrpc2.Conn
}

// newTestPair creates a server + client pair connected via net.Pipe.
// The server runs in a goroutine; done is closed when the server exits.
func newTestPair(t *testing.T, srv *Server) (client *lspClient, done <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		srv.Run(context.Background(), jsonrpc2.NewStream(serverConn))
	}()

	cc := jsonrpc2.NewConn(jsonrpc2.NewStream(clientConn))
	cc.Go(context.Background(), jsonrpc2.MethodNotFoundHandler)
	t.Cleanup(func() { cc.Close() })

	return &lspClient{conn: cc}, ch
}

func (c *lspClient) call(ctx context.Context, method string, params, result any) error {
	_, err := c.conn.Call(ctx, method, params, result)
	return err
}

func (c *lspClient) notify(ctx context.Context, method string, params any) error {
	return c.conn.Notify(ctx, method, params)
}

// initializeParams returns a minimal InitializeParams for testing.
func initializeParams(rootURI uri.URI) *protocol.InitializeParams {
	return &protocol.InitializeParams{
		RootURI: rootURI,
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, d time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatal("timed out waiting for server to exit")
	}
}

// --- Tests ---

func TestInitialize_ReturnsExpectedCapabilities(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	sync, ok := result.Capabilities.TextDocumentSync.(*protocol.TextDocumentSyncOptions)
	if !ok {
		// TextDocumentSync may unmarshal as a map; re-unmarshal from JSON.
		raw, _ := json.Marshal(result.Capabilities.TextDocumentSync)
		var opts protocol.TextDocumentSyncOptions
		if err := json.Unmarshal(raw, &opts); err != nil {
			t.Fatalf("TextDocumentSync unmarshal: %v", err)
		}
		sync = &opts
	}
	if !sync.OpenClose {
		t.Errorf("initialize: OpenClose = %v, want true", sync.OpenClose)
	}
	if sync.Change != protocol.TextDocumentSyncKindIncremental {
		t.Errorf("initialize: Change = %v, want Incremental", sync.Change)
	}
	if sync.Save == nil || sync.Save.IncludeText {
		t.Errorf("initialize: Save.IncludeText = %v, want false", sync.Save != nil && sync.Save.IncludeText)
	}
	if result.ServerInfo == nil || result.ServerInfo.Name != "beancount-lsp" {
		t.Errorf("initialize: ServerInfo.Name = %q, want beancount-lsp", result.ServerInfo.Name)
	}

	caps := result.Capabilities
	if caps.HoverProvider != nil {
		t.Errorf("initialize: HoverProvider = %v, want nil", caps.HoverProvider)
	}
	if caps.DefinitionProvider != nil {
		t.Errorf("initialize: DefinitionProvider = %v, want nil", caps.DefinitionProvider)
	}
	if caps.CompletionProvider != nil {
		t.Errorf("initialize: CompletionProvider = %v, want nil", caps.CompletionProvider)
	}
	if ok, _ := caps.DocumentFormattingProvider.(bool); !ok {
		t.Errorf("initialize: DocumentFormattingProvider = %v, want true", caps.DocumentFormattingProvider)
	}
	if ok, _ := caps.DocumentRangeFormattingProvider.(bool); !ok {
		t.Errorf("initialize: DocumentRangeFormattingProvider = %v, want true", caps.DocumentRangeFormattingProvider)
	}
	if caps.Workspace != nil {
		t.Errorf("initialize: Workspace = %v, want nil", caps.Workspace)
	}
}

func TestInitialize_TwiceRejected(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result); err != nil {
		t.Fatalf("first initialize: %v", err)
	}
	err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &result)
	if err == nil {
		t.Fatal("second initialize should have returned an error")
	}
}

func TestLifecycle_Happy(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, done := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := client.notify(ctx, "initialized", &protocol.InitializedParams{}); err != nil {
		t.Fatalf("initialized: %v", err)
	}
	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := client.notify(ctx, "exit", nil); err != nil {
		// connection closing is expected on exit
		_ = err
	}

	waitFor(t, done, 3*time.Second)

	srv.mu.Lock()
	code := srv.exitCode
	srv.mu.Unlock()
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestExit_WithoutShutdown_ExitCode1(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, done := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_ = client.notify(ctx, "exit", nil)

	waitFor(t, done, 3*time.Second)

	srv.mu.Lock()
	code := srv.exitCode
	srv.mu.Unlock()
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestRequestAfterShutdown_InvalidRequest(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult)
	if err == nil {
		t.Fatal("request after shutdown should return an error")
	}
}

func TestDidOpen_CallsSetOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     docURI,
			Version: 1,
			Text:    "2024-01-01 open Assets:Bank USD\n",
		},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	stub.awaitSet(t, 3*time.Second)
}

func TestDidChange_CallsSetOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(
		WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }),
		WithDebounce(0),
	)
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI: docURI, Version: 1, Text: "initial\n",
		},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	stub.awaitSet(t, 3*time.Second)

	before := func() []string {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return append([]string(nil), stub.setCalls...)
	}()

	if err := client.notify(ctx, "textDocument/didChange", &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: docURI},
			Version:                2,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			{Text: "updated\n"},
		},
	}); err != nil {
		t.Fatalf("didChange: %v", err)
	}

	stub.awaitSet(t, 3*time.Second)

	stub.mu.Lock()
	after := stub.setCalls
	stub.mu.Unlock()

	if len(after) <= len(before) {
		t.Error("SetOverlay was not called for didChange")
	}
}

func TestDidClose_CallsClearOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "x\n"},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	stub.awaitSet(t, 3*time.Second)

	if err := client.notify(ctx, "textDocument/didClose", &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	}); err != nil {
		t.Fatalf("didClose: %v", err)
	}

	stub.awaitClear(t, 3*time.Second)
}

func TestDidSave_CallsClearOverlay(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "x\n"},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	stub.awaitSet(t, 3*time.Second)

	if err := client.notify(ctx, "textDocument/didSave", &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	}); err != nil {
		t.Fatalf("didSave: %v", err)
	}

	stub.awaitClear(t, 3*time.Second)
}

func TestRootResolution_WorkspaceFolders(t *testing.T) {
	var capturedRoot string
	srv := NewServer(WithSessionFactory(func(root string) (SessionAPI, error) {
		capturedRoot = root
		return newStub(), nil
	}))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	params := &protocol.InitializeParams{
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{URI: "file:///workspace", Name: "test"},
		},
		RootURI: "file:///fallback",
	}
	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", params, &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if capturedRoot == "" {
		t.Fatal("session factory was not called")
	}
	// WorkspaceFolders must win over RootURI.
	if !strings.Contains(capturedRoot, "workspace") {
		t.Errorf("capturedRoot = %q, want path containing \"workspace\" (WorkspaceFolders should win over RootURI)", capturedRoot)
	}
	if strings.Contains(capturedRoot, "fallback") {
		t.Errorf("capturedRoot = %q, RootURI fallback must not be used when WorkspaceFolders is set", capturedRoot)
	}
}

func TestRootResolution_RootURI(t *testing.T) {
	var capturedRoot string
	srv := NewServer(WithSessionFactory(func(root string) (SessionAPI, error) {
		capturedRoot = root
		return newStub(), nil
	}))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	params := &protocol.InitializeParams{
		RootURI: "file:///myproject",
	}
	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", params, &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if capturedRoot == "" {
		t.Fatal("session factory was not called for RootURI")
	}
	if !strings.Contains(capturedRoot, "myproject") {
		t.Errorf("capturedRoot = %q, want path containing \"myproject\"", capturedRoot)
	}
}

func TestRootResolution_FirstDidOpen(t *testing.T) {
	// When initialize has no workspace folders, RootURI, or RootPath, session
	// creation is deferred to the first textDocument/didOpen.
	var sessionRoots []string
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(root string) (SessionAPI, error) {
		sessionRoots = append(sessionRoots, root)
		return stub, nil
	}))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	// Empty params → no eager session
	var result protocol.InitializeResult
	if err := client.call(ctx, "initialize", &protocol.InitializeParams{}, &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Session factory must not have been called yet (lazy path)
	if len(sessionRoots) != 0 {
		t.Errorf("session created eagerly: %v", sessionRoots)
	}

	// First didOpen triggers lazy session creation
	docURI := uri.URI("file:///tmp/a.beancount")
	_ = client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "x\n"},
	})

	stub.awaitSet(t, 3*time.Second)

	if len(sessionRoots) == 0 {
		t.Error("session factory was not called on first didOpen")
	}
	// SetOverlay must have been called on the stub
	stub.mu.Lock()
	calls := stub.setCalls
	stub.mu.Unlock()
	if len(calls) == 0 {
		t.Error("SetOverlay not called after lazy session creation")
	}
}

func TestHandlerPanic_Recovered(t *testing.T) {
	stub := newStub()
	stub.panicOnSet = true

	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	// didOpen will panic in SetOverlay; server should survive
	_ = client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "x\n"},
	})

	// Server must still respond to subsequent requests
	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Errorf("server did not survive handler panic: %v", err)
	}
}

func TestHandlerPanic_RequestReturnsInternalError(t *testing.T) {
	// Session factory panics during initialize (a request), so panic recovery
	// must reply with jsonrpc2.ErrInternal.
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) {
		panic("factory panic")
	}))
	client, _ := newTestPair(t, srv)
	ctx := context.Background()

	var initResult protocol.InitializeResult
	err := client.call(ctx, "initialize", initializeParams("file:///tmp"), &initResult)
	if err == nil {
		t.Fatal("initialize should have returned an error due to handler panic")
	}
	var rpcErr *jsonrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error is not *jsonrpc2.Error: %T %v", err, err)
	}
	if rpcErr.Code != jsonrpc2.InternalError {
		t.Errorf("error code = %d, want %d (InternalError)", rpcErr.Code, jsonrpc2.InternalError)
	}
}
