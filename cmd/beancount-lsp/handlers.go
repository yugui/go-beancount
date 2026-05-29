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

// serverCapabilitiesExt extends protocol.ServerCapabilities with
// inlayHintProvider, which go.lsp.dev/protocol v0.12.0 (LSP 3.16) omits.
type serverCapabilitiesExt struct {
	protocol.ServerCapabilities
	InlayHintProvider bool `json:"inlayHintProvider,omitempty"`
}

// initializeResultExt mirrors protocol.InitializeResult but carries the
// extended capabilities so inlayHintProvider reaches the wire.
type initializeResultExt struct {
	Capabilities serverCapabilitiesExt `json:"capabilities"`
	ServerInfo   *protocol.ServerInfo  `json:"serverInfo,omitempty"`
}

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

	caps := params.Capabilities
	s.mu.Lock()
	s.initialized = true
	s.session = sess
	s.rootPath = root
	s.clientCaps = &caps
	s.mu.Unlock()

	s.logger.Printf("initialized root=%s", root)
	s.startSubscriber()

	result := &initializeResultExt{
		ServerInfo: &protocol.ServerInfo{
			Name:    "beancount-lsp",
			Version: "0.0.0",
		},
		Capabilities: serverCapabilitiesExt{
			// inlayHintProvider has no field in go.lsp.dev/protocol v0.12.0
			// (LSP 3.16); the wrapper marshals it alongside the typed
			// capabilities. See serverCapabilitiesExt.
			InlayHintProvider: true,
			ServerCapabilities: protocol.ServerCapabilities{
				TextDocumentSync: &protocol.TextDocumentSyncOptions{
					OpenClose: true,
					Change:    protocol.TextDocumentSyncKindIncremental,
					Save:      &protocol.SaveOptions{IncludeText: false},
				},
				HoverProvider:                   true,
				DocumentFormattingProvider:      true,
				DocumentRangeFormattingProvider: true,
				DocumentSymbolProvider:          true,
				DefinitionProvider:              true,
				RenameProvider:                  &protocol.RenameOptions{PrepareProvider: true},
				CompletionProvider: &protocol.CompletionOptions{
					// Trigger characters cover lexical positions where the user
					// has just committed to a specific completion context and
					// will not otherwise hit a word-boundary that the client
					// auto-triggers on:
					//   `:` — account path separator
					//   `#` — tag introducer
					//   `^` — link introducer
					//   `"` — start of payee/narration or metadata string
					// `*` and `!` (transaction flags) remain excluded: they
					// open no string scope, and triggering on them would
					// misfire whenever they appear inside arithmetic or
					// number-flag mixes. classifyContext returns no
					// candidates for ContextInString / ContextUnknown, so a
					// stray `"` (e.g. inside option/plugin strings) silently
					// produces an empty list rather than wrong suggestions.
					TriggerCharacters: []string{":", "#", "^", "\""},
				},
			},
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

// handleInitialized handles the LSP initialized notification. If the client
// declared dynamic registration for workspace/didChangeWatchedFiles, it
// immediately sends a client/registerCapability request to watch *.beancount.
// Failure to register file-watcher capability is logged and non-fatal.
func (s *Server) handleInitialized(ctx context.Context, reply jsonrpc2.Replier, _ json.RawMessage) error {
	_ = reply(ctx, nil, nil) // notification: release the AsyncHandler chain first

	s.mu.Lock()
	caps := s.clientCaps
	conn := s.conn
	s.mu.Unlock()

	if caps == nil || caps.Workspace == nil ||
		caps.Workspace.DidChangeWatchedFiles == nil ||
		!caps.Workspace.DidChangeWatchedFiles.DynamicRegistration {
		return nil
	}

	params := &protocol.RegistrationParams{
		Registrations: []protocol.Registration{
			{
				ID:     "beancount-file-watcher",
				Method: "workspace/didChangeWatchedFiles",
				RegisterOptions: &protocol.DidChangeWatchedFilesRegistrationOptions{
					Watchers: []protocol.FileSystemWatcher{
						{GlobPattern: "**/*.beancount"},
					},
				},
			},
		},
	}
	var nullReply json.RawMessage
	if _, err := conn.Call(ctx, "client/registerCapability", params, &nullReply); err != nil {
		s.logger.Printf("registerCapability: %v", err)
	}
	return nil
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
