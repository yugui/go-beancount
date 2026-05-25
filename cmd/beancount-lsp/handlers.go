package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleInitialize handles the LSP initialize request.
func (s *Server) handleInitialize(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}
	s.mu.Unlock()

	var params protocol.InitializeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	root, sess, err := s.initializeSession(&params)
	if err != nil {
		s.logger.Printf("initialize: session error: %v", err)
		// session stays nil; text-sync handlers will no-op
	}

	s.mu.Lock()
	s.initialized = true
	s.session = sess
	s.rootPath = root
	s.mu.Unlock()

	s.logger.Printf("initialized root=%s", root)
	s.startSubscriber()

	result := &protocol.InitializeResult{
		ServerInfo: &protocol.ServerInfo{
			Name:    "beancount-lsp",
			Version: "0.0.0",
		},
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.TextDocumentSyncKindIncremental,
				Save:      &protocol.SaveOptions{IncludeText: false},
			},
			DocumentFormattingProvider:      true,
			DocumentRangeFormattingProvider: true,
			DocumentSymbolProvider:          true,
			DefinitionProvider:              true,
		},
	}
	return reply(ctx, result, nil)
}

// initializeSession resolves the root directory from params and creates a
// session. Returns the resolved root path and the session (which may be nil on
// error or when root resolution is deferred to the first didOpen).
func (s *Server) initializeSession(params *protocol.InitializeParams) (root string, sess SessionAPI, err error) {
	root = resolveRoot(params)
	if root == "" {
		// lazy path: session deferred to first textDocument/didOpen
		return "", nil, nil
	}

	rootFile := resolveRootFile(root)
	sess, err = s.sessionFactory(rootFile)
	if err != nil {
		return rootFile, nil, fmt.Errorf("session.New(%q): %w", rootFile, err)
	}
	return rootFile, sess, nil
}

// resolveRoot returns the workspace root directory from initialize params.
// Precedence: WorkspaceFolders[0] > RootURI > RootPath.
// Returns empty string when none of the above is set; the caller defers
// session creation to the first textDocument/didOpen.
//
// The OS working-directory fallback is intentionally omitted: the lazy
// first-didOpen path covers all realistic cases where no folder info is
// provided.
func resolveRoot(params *protocol.InitializeParams) string {
	if len(params.WorkspaceFolders) > 0 {
		u := uri.URI(params.WorkspaceFolders[0].URI)
		return u.Filename()
	}
	// LSP deprecated RootURI/RootPath in favor of WorkspaceFolders, but a
	// server must still honor them for clients that send only the legacy fields.
	//lint:ignore SA1019 legacy-client compatibility
	rootURI, rootPath := params.RootURI, params.RootPath
	if rootURI != "" {
		return rootURI.Filename()
	}
	if rootPath != "" {
		abs, err := filepath.Abs(rootPath)
		if err == nil {
			return abs
		}
		return rootPath
	}
	return ""
}

// handleInitialized handles the LSP initialized notification.
func (s *Server) handleInitialized(ctx context.Context, reply jsonrpc2.Replier, _ json.RawMessage) error {
	return reply(ctx, nil, nil)
}

// handleShutdown handles the LSP shutdown request.
func (s *Server) handleShutdown(ctx context.Context, reply jsonrpc2.Replier, _ json.RawMessage) error {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	return reply(ctx, nil, nil)
}

// handleExit handles the LSP exit notification. Terminates the connection with
// exit code 0 if shutdown was received first, 1 otherwise.
func (s *Server) handleExit(ctx context.Context, reply jsonrpc2.Replier, _ json.RawMessage) error {
	s.mu.Lock()
	clean := s.shutdown
	sess := s.session
	conn := s.conn
	subCancel := s.subscriberCancel
	subDone := s.subscriberDone
	if clean {
		s.exitCode = 0
	} else {
		s.exitCode = 1
	}
	s.exited = true
	s.mu.Unlock()

	// reply before closing (no-op for notification, but releases AsyncHandler chain).
	_ = reply(ctx, nil, nil)

	// Stop pending debounce timers before closing the session to prevent late
	// Reload calls on a closed session.
	s.timers.stopAll()

	// Cancel the subscriber goroutine's context, then close the session (which
	// closes the subscribe channel), then join to avoid conn.Notify after conn.Close.
	if subCancel != nil {
		subCancel()
	}
	if sess != nil {
		if err := sess.Close(); err != nil {
			s.logger.Printf("session close: %v", err)
		}
	}
	if subDone != nil {
		<-subDone
	}

	if conn != nil {
		_ = conn.Close()
	}
	return nil
}
