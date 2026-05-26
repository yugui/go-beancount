// File watching in Phase 11 is delegated to the LSP client via
// workspace/didChangeWatchedFiles. Phase 10 (bean-daemon) reuses the
// same SessionAPI (SetOverlay/ClearOverlay/Reload) with an fsnotify
// adapter; this dispatch logic does not change.
package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yugui/go-beancount/pkg/session"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleDidChangeWatchedFiles handles workspace/didChangeWatchedFiles.
//
// Created/Changed events on files without an overlay trigger a session Reload
// (disk changed, no editor buffer shadowing it). Changed events for files that
// have an open overlay are ignored (overlay is authoritative). Deleted events
// clear any overlay for the file and trigger a Reload.
//
// A single Reload is fired after processing all events, only when at least one
// event produced a reload trigger. Malformed or unparseable params are silently ignored.
func (s *Server) handleDidChangeWatchedFiles(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	defer func() { _ = reply(ctx, nil, nil) }()

	var params protocol.DidChangeWatchedFilesParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()

	if sess == nil {
		return nil
	}

	needReload := false
	for _, ev := range params.Changes {
		u := uri.URI(ev.URI)
		absPath := u.Filename()

		switch ev.Type {
		case protocol.FileChangeTypeDeleted:
			_, hasOverlay := s.docs.get(u)
			if hasOverlay {
				s.docs.delete(u)
				if err := sess.ClearOverlay(absPath); err != nil {
					s.logger.Printf("didChangeWatchedFiles ClearOverlay: %v", err)
				}
			}
			needReload = true

		case protocol.FileChangeTypeCreated, protocol.FileChangeTypeChanged:
			if _, hasOverlay := s.docs.get(u); hasOverlay {
				// overlay is authoritative
				continue
			}
			needReload = true
		}
	}

	if needReload {
		go func() {
			if _, err := sess.Reload(context.Background()); err != nil && !errors.Is(err, session.ErrSessionClosed) {
				s.logger.Printf("watch reload: %v", err)
			}
		}()
	}
	return nil
}
