package main

import (
	"context"
	"encoding/json"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// document holds the in-memory state of an open text document.
type document struct {
	version int32
	content []byte
}

// docStore is a concurrency-safe store of open documents keyed by URI.
type docStore struct {
	mu   sync.Mutex
	docs map[uri.URI]*document
}

func newDocStore() *docStore {
	return &docStore{docs: make(map[uri.URI]*document)}
}

func (ds *docStore) set(u uri.URI, ver int32, content []byte) {
	ds.mu.Lock()
	ds.docs[u] = &document{version: ver, content: content}
	ds.mu.Unlock()
}

func (ds *docStore) get(u uri.URI) ([]byte, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	d, ok := ds.docs[u]
	if !ok {
		return nil, false
	}
	return d.content, true
}

func (ds *docStore) delete(u uri.URI) {
	ds.mu.Lock()
	delete(ds.docs, u)
	ds.mu.Unlock()
}

// uris returns the URIs of all currently open documents.
func (ds *docStore) uris() []uri.URI {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	out := make([]uri.URI, 0, len(ds.docs))
	for u := range ds.docs {
		out = append(out, u)
	}
	return out
}

// handleDidOpen handles textDocument/didOpen. It stores the document content
// as an overlay and triggers lazy session creation if needed.
func (s *Server) handleDidOpen(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	defer func() { _ = reply(ctx, nil, nil) }()

	var params protocol.DidOpenTextDocumentParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}

	u := params.TextDocument.URI
	content := []byte(params.TextDocument.Text)
	s.docs.set(u, params.TextDocument.Version, content)

	s.ensureSession(u)

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()

	if sess == nil {
		return nil
	}
	if err := sess.SetOverlay(u.Filename(), content); err != nil {
		s.logger.Printf("didOpen SetOverlay: %v", err)
		return nil
	}
	go func() {
		if _, err := sess.Snapshot(context.Background()); err != nil {
			s.logger.Printf("didOpen Snapshot: %v", err)
		}
	}()
	return nil
}

// handleDidChange handles textDocument/didChange. Content is stored in docStore
// immediately; SetOverlay and Snapshot are debounced per document (default 100ms).
// When debounce is 0, SetOverlay is called synchronously.
func (s *Server) handleDidChange(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	defer func() { _ = reply(ctx, nil, nil) }()

	var params protocol.DidChangeTextDocumentParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}

	u := params.TextDocument.URI
	content := applyChanges(s.docs, u, params.ContentChanges)
	s.docs.set(u, params.TextDocument.Version, content)

	s.mu.Lock()
	sess := s.session
	d := s.debounce
	s.mu.Unlock()

	if sess == nil {
		return nil
	}

	if d == 0 {
		if err := sess.SetOverlay(u.Filename(), content); err != nil {
			s.logger.Printf("didChange SetOverlay: %v", err)
		}
		go func() {
			if _, err := sess.Snapshot(context.Background()); err != nil {
				s.logger.Printf("didChange Snapshot: %v", err)
			}
		}()
		return nil
	}

	s.timers.schedule(u, d, func() {
		latest, ok := s.docs.get(u)
		if !ok {
			return
		}
		if err := sess.SetOverlay(u.Filename(), latest); err != nil {
			s.logger.Printf("didChange SetOverlay (debounced): %v", err)
			return
		}
		go func() {
			if _, err := sess.Snapshot(context.Background()); err != nil {
				s.logger.Printf("didChange Snapshot (debounced): %v", err)
			}
		}()
	})
	return nil
}

// applyChanges returns the document content after applying all change events.
// Events with a Range field are treated as full-replace; proper UTF-16
// incremental application is not yet implemented.
func applyChanges(ds *docStore, u uri.URI, changes []protocol.TextDocumentContentChangeEvent) []byte {
	current, _ := ds.get(u)
	for _, ch := range changes {
		current = []byte(ch.Text)
	}
	return current
}

// handleDidClose handles textDocument/didClose. It removes the in-memory
// document state and clears the session overlay so disk content takes effect.
// An empty-array publishDiagnostics notification is sent immediately so the
// editor drops any stale error markers for the closed file.
func (s *Server) handleDidClose(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	defer func() { _ = reply(ctx, nil, nil) }()

	var params protocol.DidCloseTextDocumentParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}

	u := params.TextDocument.URI
	s.docs.delete(u)

	s.mu.Lock()
	sess := s.session
	conn := s.conn
	s.mu.Unlock()

	if conn != nil {
		s.sendPublish(ctx, conn, u, []protocol.Diagnostic{})
	}

	if sess == nil {
		return nil
	}
	if err := sess.ClearOverlay(u.Filename()); err != nil {
		s.logger.Printf("didClose ClearOverlay: %v", err)
	}
	return nil
}

// handleDidSave handles textDocument/didSave by clearing the file's overlay so
// subsequent loads see canonical disk content. This closes the window where an
// external editor or formatter could be shadowed by a stale overlay.
func (s *Server) handleDidSave(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	defer func() { _ = reply(ctx, nil, nil) }()

	var params protocol.DidSaveTextDocumentParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}

	u := params.TextDocument.URI

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()

	if sess == nil {
		return nil
	}
	if err := sess.ClearOverlay(u.Filename()); err != nil {
		s.logger.Printf("didSave ClearOverlay: %v", err)
	}
	return nil
}
