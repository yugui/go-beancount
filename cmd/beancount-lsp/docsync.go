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
	}
	return nil
}

// handleDidChange handles textDocument/didChange. Content changes are applied
// by treating the last event as a full document replacement; proper UTF-16
// incremental application is not yet implemented.
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
	s.mu.Unlock()

	if sess == nil {
		return nil
	}
	if err := sess.SetOverlay(u.Filename(), content); err != nil {
		s.logger.Printf("didChange SetOverlay: %v", err)
	}
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
	s.mu.Unlock()

	if sess == nil {
		return nil
	}
	if err := sess.ClearOverlay(u.Filename()); err != nil {
		s.logger.Printf("didClose ClearOverlay: %v", err)
	}
	return nil
}

// handleDidSave handles textDocument/didSave. After save, the client buffer
// equals disk; clearing the overlay ensures the session reads the canonical
// on-disk content, closing the divergence window between edits.
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
