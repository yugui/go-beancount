package main

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// diagCollector is a jsonrpc2 handler that collects publishDiagnostics notifications.
type diagCollector struct {
	mu     sync.Mutex
	notifs []*protocol.PublishDiagnosticsParams
	ch     chan struct{} // signalled on each received notification
}

func newDiagCollector() *diagCollector {
	return &diagCollector{ch: make(chan struct{}, 100)}
}

func (dc *diagCollector) handle(_ context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	_ = reply(context.Background(), nil, nil)
	if req.Method() != "textDocument/publishDiagnostics" {
		return nil
	}
	var params protocol.PublishDiagnosticsParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return nil
	}
	dc.mu.Lock()
	dc.notifs = append(dc.notifs, &params)
	dc.mu.Unlock()
	select {
	case dc.ch <- struct{}{}:
	default:
	}
	return nil
}

func (dc *diagCollector) await(t *testing.T, n int, d time.Duration) []*protocol.PublishDiagnosticsParams {
	t.Helper()
	deadline := time.After(d)
	for {
		dc.mu.Lock()
		got := len(dc.notifs)
		dc.mu.Unlock()
		if got >= n {
			break
		}
		select {
		case <-dc.ch:
		case <-deadline:
			t.Fatalf("timed out waiting for %d publishDiagnostics (got %d)", n, got)
		}
	}
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return append([]*protocol.PublishDiagnosticsParams(nil), dc.notifs...)
}

// newDiagTestPair creates a server+client pair where the client collects
// publishDiagnostics notifications. Returns the client sender, collector, and
// a done channel closed when the server exits. A cleanup function issues
// shutdown+exit before closing the pipe to avoid subscriber goroutine leaks.
func newDiagTestPair(t *testing.T, srv *Server) (*lspClient, *diagCollector, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()

	dc := newDiagCollector()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(context.Background(), jsonrpc2.NewStream(serverConn))
	}()

	cc := jsonrpc2.NewConn(jsonrpc2.NewStream(clientConn))
	cc.Go(context.Background(), jsonrpc2.AsyncHandler(dc.handle))

	client := &lspClient{conn: cc}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = client.call(ctx, "shutdown", nil, nil)
		_ = client.notify(ctx, "exit", nil)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		cc.Close()
		serverConn.Close()
		clientConn.Close()
	})

	return client, dc, done
}

// sendLedger pushes a ledger to the stub session's subscriber channel.
func sendLedger(stub *stubSession, ledger *ast.Ledger) {
	stub.subCh <- ledger
}

func makeLedger(diags ...ast.Diagnostic) *ast.Ledger {
	return &ast.Ledger{Diagnostics: diags}
}

func makeFileURI(path string) uri.URI {
	return uri.File(path)
}

// --- Tests ---

func TestPublishDiagnostics_DeliveredOnSubscribe(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Send a ledger with one diagnostic.
	diag := ast.Diagnostic{
		Span:     ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}},
		Message:  "test error",
		Severity: ast.Error,
		Code:     "test-code",
	}
	sendLedger(stub, makeLedger(diag))

	notifs := dc.await(t, 1, 3*time.Second)
	if len(notifs) < 1 {
		t.Fatal("no publishDiagnostics received")
	}
	n := notifs[0]
	if n.URI != makeFileURI("/tmp/a.beancount") {
		t.Errorf("URI = %q, want %q", n.URI, makeFileURI("/tmp/a.beancount"))
	}
	if len(n.Diagnostics) != 1 {
		t.Fatalf("len(Diagnostics) = %d, want 1", len(n.Diagnostics))
	}
	d := n.Diagnostics[0]
	if d.Message != "test error" {
		t.Errorf("Message = %q, want %q", d.Message, "test error")
	}
	if d.Severity != protocol.DiagnosticSeverityError {
		t.Errorf("Severity = %v, want Error", d.Severity)
	}
	if d.Source != "beancount" {
		t.Errorf("Source = %q, want %q", d.Source, "beancount")
	}
	if d.Code != "test-code" {
		t.Errorf("Code = %v, want %q", d.Code, "test-code")
	}
}

func TestPublishDiagnostics_GroupedByFile(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	ledger := makeLedger(
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}}, Message: "e1"},
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/b.beancount", Line: 1, Column: 1}}, Message: "e2"},
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/c.beancount", Line: 1, Column: 1}}, Message: "e3"},
	)
	sendLedger(stub, ledger)

	notifs := dc.await(t, 3, 3*time.Second)
	if len(notifs) != 3 {
		t.Errorf("got %d notifications, want 3", len(notifs))
	}
}

func TestPublishDiagnostics_ClearsResolvedFile(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Ledger 1: two files with diagnostics.
	sendLedger(stub, makeLedger(
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}}, Message: "ea"},
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/b.beancount", Line: 1, Column: 1}}, Message: "eb"},
	))
	dc.await(t, 2, 3*time.Second)

	// Ledger 2: only file A has diagnostics; B should get an empty clear.
	sendLedger(stub, makeLedger(
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}}, Message: "ea"},
	))
	notifs := dc.await(t, 4, 3*time.Second) // 2 from first + 2 from second (A update + B clear)

	// Find the clear notification for b.beancount
	bURI := makeFileURI("/tmp/b.beancount")
	var clearNotif *protocol.PublishDiagnosticsParams
	for _, n := range notifs[2:] {
		if n.URI == bURI && len(n.Diagnostics) == 0 {
			clearNotif = n
			break
		}
	}
	if clearNotif == nil {
		t.Error("no empty-array clear notification for /tmp/b.beancount")
	}
}

func TestPublishDiagnostics_NoClearForUnseenFile(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Ledger 1: empty diagnostics.
	sendLedger(stub, makeLedger())
	// Sleep ensures the subscriber processes the empty ledger before the next
	// send; no synchronous signal for zero-diagnostic publish cycles.
	time.Sleep(50 * time.Millisecond)

	// Ledger 2: file A gains diagnostics.
	sendLedger(stub, makeLedger(
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}}, Message: "ea"},
	))
	notifs := dc.await(t, 1, 3*time.Second)

	// There must be exactly one notification (for A), no spurious clears.
	for _, n := range notifs {
		if len(n.Diagnostics) == 0 {
			t.Errorf("unexpected empty-array notification for %s", n.URI)
		}
	}
}

func TestPublishDiagnostics_EmptyFilenameDiagnosticDropped(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Diagnostic with no filename should be dropped; only the named one published.
	sendLedger(stub, makeLedger(
		ast.Diagnostic{Message: "no-file diagnostic"},
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}}, Message: "named"},
	))
	notifs := dc.await(t, 1, 3*time.Second)

	for _, n := range notifs {
		if n.URI == "" {
			t.Errorf("notification with empty URI received")
		}
	}
	// Exactly 1 notification (for the named file only).
	if len(notifs) != 1 {
		t.Errorf("got %d notifications, want 1", len(notifs))
	}
}

// TestPublishDiagnostics_MultibytePathUsesClientURI guards against publishing a
// multibyte-path document's diagnostics to a re-encoded URI. Editors send the
// document URI with lowercase percent-escapes (%e3); uri.File re-encodes with
// uppercase (%E3). Publishing to the uppercase URI would land on a phantom file
// the editor ignores, while the lowercase open document would be spuriously
// cleared. The diagnostic must reach the editor's original (lowercase) URI, and
// that URI must not also receive an empty-array clear in the same cycle.
func TestPublishDiagnostics_MultibytePathUsesClientURI(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// The editor opens the file with lowercase-hex escapes; uri.File yields
	// uppercase, so the two URI strings differ for the same path.
	path := "/tmp/ビーンカウント/test.beancount"
	clientURI := lowerHexEscapes(uri.File(path))
	if clientURI == uri.File(path) {
		t.Fatalf("expected lowercase URI to differ from uri.File(%q)", path)
	}
	if err := client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: clientURI, Version: 1, Text: "x\n"},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	stub.awaitSet(t, 3*time.Second)

	sendLedger(stub, makeLedger(
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: path, Line: 1, Column: 1}}, Message: "boom"},
	))
	notifs := dc.await(t, 1, 3*time.Second)

	var gotDiag bool
	for _, n := range notifs {
		if n.URI != clientURI {
			t.Errorf("notification URI = %q, want client URI %q", n.URI, clientURI)
		}
		if len(n.Diagnostics) > 0 {
			gotDiag = true
		} else {
			t.Errorf("unexpected empty-array clear for %q", n.URI)
		}
	}
	if !gotDiag {
		t.Error("no diagnostic delivered for the multibyte-path document")
	}
}

func TestDidChange_Debounced(t *testing.T) {
	stub := newStub()
	const debounce = 200 * time.Millisecond
	srv := NewServer(
		WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }),
		WithDebounce(debounce),
	)
	client, _, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	_ = client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "initial\n"},
	})
	stub.awaitSet(t, 3*time.Second)

	// First didChange
	_ = client.notify(ctx, "textDocument/didChange", &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: docURI}, Version: 2},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: "change1\n"}},
	})
	// Second didChange 20ms later (within the 200ms window)
	time.Sleep(20 * time.Millisecond)
	_ = client.notify(ctx, "textDocument/didChange", &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: docURI}, Version: 3},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: "change2\n"}},
	})

	// Wait for the debounce to fire (200ms + margin)
	stub.awaitSet(t, 3*time.Second)

	// Wait a bit more to see if a second SetOverlay arrives (it shouldn't).
	time.Sleep(debounce + 20*time.Millisecond)

	stub.mu.Lock()
	setCalls := len(stub.setCalls)
	stub.mu.Unlock()

	// Should be 2: one from didOpen, one from the debounced didChange.
	if setCalls != 2 {
		t.Errorf("SetOverlay called %d times, want 2 (didOpen + one debounced didChange)", setCalls)
	}
}

func TestDidChange_DebounceCancelOnClose(t *testing.T) {
	stub := newStub()
	const debounce = 200 * time.Millisecond
	srv := NewServer(
		WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }),
		WithDebounce(debounce),
	)
	client, _, done := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	docURI := uri.URI("file:///tmp/test.beancount")
	_ = client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "initial\n"},
	})
	stub.awaitSet(t, 3*time.Second)

	setCallsBefore := func() int {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return len(stub.setCalls)
	}()

	// Schedule a debounced change, then immediately exit before the timer fires.
	_ = client.notify(ctx, "textDocument/didChange", &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: docURI}, Version: 2},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: "changed\n"}},
	})

	// Exit immediately (timer window is 200ms, exit should arrive before it fires)
	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_ = client.notify(ctx, "exit", nil)
	waitFor(t, done, 3*time.Second)

	stub.mu.Lock()
	setCallsAfter := len(stub.setCalls)
	stub.mu.Unlock()

	// The debounced SetOverlay must not have fired after exit.
	if setCallsAfter != setCallsBefore {
		t.Errorf("SetOverlay fired after exit: before=%d after=%d", setCallsBefore, setCallsAfter)
	}
}

func TestSubscriberGoroutine_ExitsOnClose(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, _, done := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Ensure subscriber goroutine is running by confirming subscriberStarted.
	srv.mu.Lock()
	started := srv.subscriberStarted
	subDone := srv.subscriberDone
	srv.mu.Unlock()

	if !started {
		t.Fatal("subscriber goroutine was not started after initialize")
	}

	if err := client.call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_ = client.notify(ctx, "exit", nil)
	waitFor(t, done, 3*time.Second)

	if subDone != nil {
		select {
		case <-subDone:
		case <-time.After(3 * time.Second):
			t.Error("subscriber goroutine did not exit within deadline after session close")
		}
	}
}

func TestPublishDiagnostics_SeverityMapping(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	sendLedger(stub, makeLedger(
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 1, Column: 1}}, Message: "err", Severity: ast.Error},
		ast.Diagnostic{Span: ast.Span{Start: ast.Position{Filename: "/tmp/a.beancount", Line: 2, Column: 1}}, Message: "warn", Severity: ast.Warning},
	))

	notifs := dc.await(t, 1, 3*time.Second)
	if len(notifs) < 1 || len(notifs[0].Diagnostics) < 2 {
		t.Fatalf("expected 1 notification with 2 diagnostics, got %d notifs", len(notifs))
	}
	diags := notifs[0].Diagnostics
	var errSev, warnSev protocol.DiagnosticSeverity
	for _, d := range diags {
		switch d.Message {
		case "err":
			errSev = d.Severity
		case "warn":
			warnSev = d.Severity
		}
	}
	if errSev != protocol.DiagnosticSeverityError {
		t.Errorf("Error diagnostic severity = %v, want %v", errSev, protocol.DiagnosticSeverityError)
	}
	if warnSev != protocol.DiagnosticSeverityWarning {
		t.Errorf("Warning diagnostic severity = %v, want %v", warnSev, protocol.DiagnosticSeverityWarning)
	}
}

func TestPublishDiagnostics_UTF16Range(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	if err := client.call(ctx, "initialize", initializeParams("file:///tmp"), nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// File content with emoji (surrogate pair in UTF-16):
	// Line 1: "a😀b\n" — 'a'=1 unit, '😀'=2 units, 'b'=1 unit
	// Diagnostic starts at Column 2 (rune index 1 = '😀') → Character 1
	// Diagnostic ends at Column 3 (rune index 2 = 'b') → Character 3 (after surrogate pair)
	const docPath = "/tmp/emoji.beancount"
	content := "a\xf0\x9f\x98\x80b\n"
	docURI := uri.File(docPath)

	// Prime docStore so sourceBytesFor finds the content.
	_ = client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: content},
	})
	stub.awaitSet(t, 3*time.Second)

	sendLedger(stub, makeLedger(ast.Diagnostic{
		Span: ast.Span{
			Start: ast.Position{Filename: docPath, Line: 1, Column: 2},
			End:   ast.Position{Filename: docPath, Line: 1, Column: 3},
		},
		Message: "emoji span",
	}))

	notifs := dc.await(t, 1, 3*time.Second)
	// Find the notification for the emoji file
	var diagNotif *protocol.PublishDiagnosticsParams
	for _, n := range notifs {
		if n.URI == docURI {
			diagNotif = n
			break
		}
	}
	if diagNotif == nil || len(diagNotif.Diagnostics) == 0 {
		t.Fatal("no diagnostic for emoji file")
	}
	r := diagNotif.Diagnostics[0].Range
	if r.Start.Character != 1 {
		t.Errorf("Start.Character = %d, want 1", r.Start.Character)
	}
	if r.End.Character != 3 {
		t.Errorf("End.Character = %d, want 3", r.End.Character)
	}
}

func TestPublishDiagnostics_LazySessionPath(t *testing.T) {
	stub := newStub()
	srv := NewServer(WithSessionFactory(func(string) (SessionAPI, error) { return stub, nil }))
	client, dc, _ := newDiagTestPair(t, srv)
	ctx := context.Background()

	// Initialize with empty params → session not yet created (lazy path).
	if err := client.call(ctx, "initialize", &protocol.InitializeParams{}, nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	srv.mu.Lock()
	started := srv.subscriberStarted
	srv.mu.Unlock()
	if started {
		t.Fatal("subscriber goroutine started eagerly; should wait for first didOpen")
	}

	// didOpen triggers lazy session creation + subscriber start.
	docURI := uri.URI("file:///tmp/lazy.beancount")
	_ = client.notify(ctx, "textDocument/didOpen", &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: docURI, Version: 1, Text: "x\n"},
	})
	stub.awaitSet(t, 3*time.Second)

	// Push a ledger with a diagnostic to verify the subscriber is running.
	sendLedger(stub, makeLedger(
		ast.Diagnostic{
			Span:     ast.Span{Start: ast.Position{Filename: "/tmp/lazy.beancount", Line: 1, Column: 1}},
			Message:  "lazy error",
			Severity: ast.Error,
		},
	))

	notifs := dc.await(t, 1, 3*time.Second)
	if len(notifs) == 0 {
		t.Fatal("no publishDiagnostics received on lazy path")
	}
	if notifs[0].URI != docURI {
		t.Errorf("URI = %q, want %q", notifs[0].URI, docURI)
	}
}
