package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

// ErrSessionClosed is returned by state-changing methods after [Session.Close].
var ErrSessionClosed = errors.New("session: closed")

// ErrOverlayKeyNotAbsolute is returned by [Session.SetOverlay] when absPath
// does not satisfy [filepath.IsAbs].
var ErrOverlayKeyNotAbsolute = errors.New("session: overlay key must be an absolute path")

// Session holds a cached ledger for a beancount root file and exposes
// operations to keep it in sync with in-memory editor overlays.
//
// Create a Session with [New]; use [Session.Close] to release it. See the
// package documentation for the full lifecycle and concurrency contract.
type Session struct {
	rootPath string
	opts     []loader.Option
	loadFunc func(ctx context.Context, path string, opts ...loader.Option) (*ast.Ledger, error)

	mu      sync.Mutex
	overlay map[string][]byte
	cached  *ast.Ledger
	valid   bool
	closed  bool

	// coalescing state: at most one reload runs at a time.
	reloading  bool
	done       chan struct{}
	lastResult *ast.Ledger
	lastErr    error
}

// New creates a Session rooted at rootPath. opts are captured and reused on
// every reload for the session's lifetime; callers must not mutate state
// retained inside opts.
//
// Any loader.WithOverlay option in opts is silently overridden by the
// session's own overlay on every reload (the session appends its own
// loader.WithOverlay last, relying on last-wins semantics).
//
// New performs an eager synchronous load with context.Background(). On
// loader I/O failure, New returns (nil, err). Ledger diagnostics are not
// failures and do not prevent New from succeeding.
func New(rootPath string, opts ...loader.Option) (*Session, error) {
	if rootPath == "" {
		return nil, fmt.Errorf("session: rootPath is empty")
	}
	s := &Session{
		rootPath: rootPath,
		opts:     opts,
		loadFunc: loader.LoadFile,
		overlay:  make(map[string][]byte),
	}
	ledger, err := s.load(context.Background(), loader.WithOverlay(s.overlay))
	if err != nil {
		return nil, err
	}
	s.cached = ledger
	s.valid = true
	return s, nil
}

// Snapshot returns the cached ledger, reloading if the cache is invalid.
// Concurrent invalidation-triggered calls coalesce into a single reload.
//
// ctx is threaded into the loader on reload. A canceled ctx returns
// (nil, ctx.Err()); the cache remains invalid so the next caller retries.
// After Close, Snapshot returns (nil, ErrSessionClosed).
func (s *Session) Snapshot(ctx context.Context) (*ast.Ledger, error) {
	return s.reload(ctx, false)
}

// Reload forces an unconditional cache rebuild. Concurrent Reload calls
// coalesce into a single loader invocation; all callers receive the same
// result. ctx semantics are identical to [Session.Snapshot].
// After Close, Reload returns (nil, ErrSessionClosed).
func (s *Session) Reload(ctx context.Context) (*ast.Ledger, error) {
	return s.reload(ctx, true)
}

// SetOverlay stores content as the in-memory source for absPath, overriding
// on-disk content on the next reload. absPath must satisfy [filepath.IsAbs];
// an empty string also fails this check.
//
// content is borrowed: the caller must not mutate the backing array until the
// next SetOverlay for the same key, [Session.ClearOverlay], or
// [Session.Close].
//
// SetOverlay invalidates the cache; the next [Session.Snapshot] or
// [Session.Reload] will rebuild. After Close, SetOverlay returns
// ErrSessionClosed.
func (s *Session) SetOverlay(absPath string, content []byte) error {
	if !filepath.IsAbs(absPath) {
		return ErrOverlayKeyNotAbsolute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	s.overlay[absPath] = content
	s.valid = false
	return nil
}

// ClearOverlay removes the overlay entry for absPath. If no entry exists,
// ClearOverlay returns nil without invalidating the cache. After Close,
// ClearOverlay returns ErrSessionClosed.
func (s *Session) ClearOverlay(absPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if _, ok := s.overlay[absPath]; ok {
		delete(s.overlay, absPath)
		s.valid = false
	}
	return nil
}

// Overlays returns a shallow copy of the current overlay map. The caller may
// freely add or remove entries in the returned map, but must not mutate the
// []byte backing arrays (the session still borrows them).
//
// Overlays is the only method that does not return ErrSessionClosed; it
// returns an empty map after Close rather than an error because it is a
// pure inspector with no state-changing side effects.
func (s *Session) Overlays() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.overlay))
	for k, v := range s.overlay {
		out[k] = v
	}
	return out
}

// Close marks the session as closed. All subsequent calls to Snapshot,
// SetOverlay, ClearOverlay, and Reload return ErrSessionClosed. Overlays
// continues to work. Close does not wait for any in-flight reload; a
// late-finishing reload's cache update is a no-op. Close is idempotent:
// subsequent calls return nil.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	// TODO: broadcast close to subscribers
	return nil
}

// reload is the shared implementation for Snapshot and Reload.
// When force is false (Snapshot), it returns the cached ledger if valid.
// When force is true (Reload), it always triggers a new load.
// At most one underlying load runs at a time; concurrent callers wait on
// the same done channel and receive the same result.
func (s *Session) reload(ctx context.Context, force bool) (*ast.Ledger, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSessionClosed
	}
	if !force && s.valid {
		cached := s.cached
		s.mu.Unlock()
		return cached, nil
	}
	if s.reloading {
		done := s.done
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.Lock()
			result, err := s.lastResult, s.lastErr
			s.mu.Unlock()
			return result, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	done := make(chan struct{})
	s.reloading = true
	s.done = done
	// avoid race with concurrent SetOverlay
	overlaySnap := make(map[string][]byte, len(s.overlay))
	for k, v := range s.overlay {
		overlaySnap[k] = v
	}
	s.mu.Unlock()

	ledger, err := s.load(ctx, loader.WithOverlay(overlaySnap))

	s.mu.Lock()
	s.reloading = false
	s.lastResult = ledger
	s.lastErr = err
	if err == nil && !s.closed {
		s.cached = ledger
		s.valid = true
		// TODO: broadcast to subscribers
	}
	s.mu.Unlock()
	close(done)

	return ledger, err
}

// load invokes loadFunc with s.rootPath, s.opts, and any extra options.
func (s *Session) load(ctx context.Context, extra ...loader.Option) (*ast.Ledger, error) {
	opts := make([]loader.Option, 0, len(s.opts)+len(extra))
	opts = append(opts, s.opts...)
	opts = append(opts, extra...)
	return s.loadFunc(ctx, s.rootPath, opts...)
}
