package main

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sync"

	"go.lsp.dev/jsonrpc2"
)

// safeHandle dispatches req with panic recovery. Panics in request handlers
// return jsonrpc2.ErrInternal; panics in notification handlers are logged and
// swallowed. reply is always called exactly once.
func safeHandle(ctx context.Context, s *Server, reply jsonrpc2.Replier, req jsonrpc2.Request) (retErr error) {
	_, isCall := req.(*jsonrpc2.Call)

	// at-most-once reply
	var once sync.Once
	safeReply := func(ctx context.Context, result any, err error) error {
		var rerr error
		once.Do(func() { rerr = reply(ctx, result, err) })
		return rerr
	}

	defer func() {
		if r := recover(); r != nil {
			s.logger.Printf("panic in handler %q: %v\n%s", req.Method(), r, debug.Stack())
			if isCall {
				retErr = safeReply(ctx, nil, jsonrpc2.ErrInternal)
			} else {
				_ = safeReply(ctx, nil, nil) // unlock async-handler chain
			}
		}
	}()

	return dispatch(ctx, s, safeReply, req)
}

// dispatch routes the request to the correct handler. Every code path calls
// reply exactly once; for notifications, this is a no-op on the wire but
// required to release the AsyncHandler serialization lock.
func dispatch(ctx context.Context, s *Server, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	s.mu.Lock()
	sd := s.shutdown
	s.mu.Unlock()

	// After shutdown, reject all non-exit requests; drop notifications.
	if sd && req.Method() != "exit" {
		if _, isCall := req.(*jsonrpc2.Call); isCall {
			return reply(ctx, nil, fmt.Errorf("server is shut down: %w", jsonrpc2.ErrInvalidRequest))
		}
		return reply(ctx, nil, nil)
	}

	raw := json.RawMessage(req.Params())

	switch req.Method() {
	case "initialize":
		return s.handleInitialize(ctx, reply, raw)
	case "initialized":
		return s.handleInitialized(ctx, reply, raw)
	case "shutdown":
		return s.handleShutdown(ctx, reply, raw)
	case "exit":
		return s.handleExit(ctx, reply, raw)
	case "textDocument/didOpen":
		return s.handleDidOpen(ctx, reply, raw)
	case "textDocument/didChange":
		return s.handleDidChange(ctx, reply, raw)
	case "textDocument/didClose":
		return s.handleDidClose(ctx, reply, raw)
	case "textDocument/didSave":
		return s.handleDidSave(ctx, reply, raw)
	default:
		if _, isCall := req.(*jsonrpc2.Call); isCall {
			return reply(ctx, nil, fmt.Errorf("%q: %w", req.Method(), jsonrpc2.ErrMethodNotFound))
		}
		// unknown notifications are silently ignored
		return reply(ctx, nil, nil)
	}
}
