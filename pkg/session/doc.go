// Package session provides a long-lived, concurrency-safe ledger session
// intended for use by editors and LSP servers that need to keep a loaded
// beancount ledger in sync with editor buffers.
//
// # Lifecycle
//
// Create a session with [New], which performs an eager synchronous initial
// load. During the session's lifetime, call [Session.Snapshot] to obtain
// the current ledger (reloading automatically when the cache is invalid),
// [Session.SetOverlay] and [Session.ClearOverlay] to inject editor-buffer
// contents, and [Session.Reload] to force an unconditional rebuild. When
// the session is no longer needed, call [Session.Close].
//
//	s, err := session.New("main.beancount")
//	if err != nil { ... }
//	defer s.Close()
//
//	ledger, err := s.Snapshot(ctx)
//
// # Concurrency
//
// All methods are safe for concurrent use from multiple goroutines. At most
// one loader call runs at a time; concurrent invalidation-triggering
// [Session.Snapshot] calls and concurrent [Session.Reload] calls coalesce
// into a single underlying load, and all callers receive the same
// [ast.Ledger].
//
// Context cancellation is honored during the load phase. A canceled caller
// receives (nil, ctx.Err()) immediately; other concurrent waiters are
// unaffected. A canceled load leaves the cache invalid so the next caller
// retries.
//
// Close is idempotent; thereafter all state-changing methods return ErrSessionClosed.
//
// # Subscriptions
//
// [Session.Subscribe] returns a capacity-1 channel that receives the latest
// [ast.Ledger] after each successful reload. Delivery uses latest-wins
// semantics: if the channel already holds an unread value from a prior reload,
// it is replaced. The returned cancel function unsubscribes and closes the
// channel; it is safe to call any number of times from any goroutine.
// [Session.Close] closes all live subscriber channels.
package session
