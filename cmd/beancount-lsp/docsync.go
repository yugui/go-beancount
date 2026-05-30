package main

import (
	"context"
	"encoding/json"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// document holds the in-memory state of an open text document. uri is the
// original client URI, retained so outbound notifications echo it verbatim.
type document struct {
	uri     uri.URI
	version int32
	content []byte
}

// docStore is a concurrency-safe store of open documents keyed by the document's
// decoded filesystem path (uri.URI.Filename()). Keying by path rather than the
// raw URI string makes lookups insensitive to percent-encoding differences
// (e.g. lowercase vs uppercase hex) between the client's URI and a URI
// re-encoded from a path.
type docStore struct {
	mu   sync.Mutex
	docs map[string]*document
}

func newDocStore() *docStore {
	return &docStore{docs: make(map[string]*document)}
}

func (ds *docStore) set(u uri.URI, ver int32, content []byte) {
	ds.mu.Lock()
	ds.docs[u.Filename()] = &document{uri: u, version: ver, content: content}
	ds.mu.Unlock()
}

// get returns the content stored for u, looked up by u.Filename().
func (ds *docStore) get(u uri.URI) ([]byte, bool) {
	return ds.getByPath(u.Filename())
}

// getByPath returns the content stored for the given decoded filesystem path.
func (ds *docStore) getByPath(filename string) ([]byte, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	d, ok := ds.docs[filename]
	if !ok {
		return nil, false
	}
	return d.content, true
}

func (ds *docStore) delete(u uri.URI) {
	ds.mu.Lock()
	delete(ds.docs, u.Filename())
	ds.mu.Unlock()
}

// uris returns the original client URIs of all currently open documents.
func (ds *docStore) uris() []uri.URI {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	out := make([]uri.URI, 0, len(ds.docs))
	for _, d := range ds.docs {
		out = append(out, d.uri)
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

	var params struct {
		TextDocument   protocol.VersionedTextDocumentIdentifier `json:"textDocument"`
		ContentChanges []rawContentChange                       `json:"contentChanges"`
	}
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

// rawContentChange mirrors protocol.TextDocumentContentChangeEvent but uses
// a pointer-typed Range so the JSON decoder can distinguish "range omitted"
// (TextDocumentSyncKindFull: full-document replace) from "range present and
// equal to (0,0)-(0,0)" (incremental insert at start of file). The protocol
// library's value-typed Range conflates the two cases.
type rawContentChange struct {
	Range *protocol.Range `json:"range,omitempty"`
	Text  string          `json:"text"`
}

// applyChanges returns the document content after applying all change events
// in order. Each event is either a full-document replace (Range nil) or an
// incremental splice over a UTF-16 character range. Per LSP spec, events are
// applied sequentially against the buffer produced by the preceding event.
func applyChanges(ds *docStore, u uri.URI, changes []rawContentChange) []byte {
	current, _ := ds.get(u)
	for _, ch := range changes {
		current = applyChange(current, ch)
	}
	return current
}

// applyChange returns buf with ch applied. For full-document changes (Range
// nil), the new content replaces buf entirely. For incremental changes, the
// LSP Range is resolved against buf's UTF-16 layout via lspPositionToByte and
// the resulting byte span is spliced with ch.Text. A reversed range (Start
// after End) is normalized so the spec's "empty range" insert at any position
// always behaves as an insertion at that position.
func applyChange(buf []byte, ch rawContentChange) []byte {
	if ch.Range == nil {
		return []byte(ch.Text)
	}
	lo := computeLineOffsets(buf)
	start := lspPositionToByte(ch.Range.Start, buf, lo)
	end := lspPositionToByte(ch.Range.End, buf, lo)
	if start > end {
		start, end = end, start
	}
	out := make([]byte, 0, len(buf)-(end-start)+len(ch.Text))
	out = append(out, buf[:start]...)
	out = append(out, ch.Text...)
	out = append(out, buf[end:]...)
	return out
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
