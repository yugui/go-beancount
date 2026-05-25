package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/session"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/uri"
)

// SessionAPI is the subset of session operations the server requires.
type SessionAPI interface {
	SetOverlay(absPath string, content []byte) error
	ClearOverlay(absPath string) error
	Snapshot(ctx context.Context) (*ast.Ledger, error)
	Subscribe() (<-chan *ast.Ledger, func())
	Close() error
}

// ServerOption configures a Server.
type ServerOption func(*serverConfig)

type serverConfig struct {
	clock          func() time.Time
	sessionFactory func(rootPath string) (SessionAPI, error)
	logger         *log.Logger
	debounce       time.Duration
}

// WithClock sets the clock used by the server. Defaults to time.Now.
// The clock is consulted for context-date decisions (e.g. hover in Step 10).
func WithClock(clock func() time.Time) ServerOption {
	return func(c *serverConfig) { c.clock = clock }
}

// WithSessionFactory sets the factory used to create a session for a given
// root path. Defaults to session.New. Primarily used in tests.
func WithSessionFactory(f func(rootPath string) (SessionAPI, error)) ServerOption {
	return func(c *serverConfig) { c.sessionFactory = f }
}

// WithLogger sets the logger for server-side messages. Defaults to a logger
// writing to os.Stderr with the "beancount-lsp: " prefix.
func WithLogger(l *log.Logger) ServerOption {
	return func(c *serverConfig) { c.logger = l }
}

// WithDebounce sets the per-document debounce delay for didChange events.
// Defaults to 100ms. A value of 0 disables debouncing: SetOverlay and Reload
// are called synchronously in the handler, which is useful for tests.
func WithDebounce(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.debounce = d }
}

// Server is an LSP server that handles the beancount-lsp protocol lifecycle
// and text-sync notifications. Create with NewServer; start with Run.
type Server struct {
	clock          func() time.Time
	sessionFactory func(rootPath string) (SessionAPI, error)
	logger         *log.Logger
	debounce       time.Duration

	mu                sync.Mutex
	initialized       bool
	shutdown          bool
	exitCode          int
	exited            bool
	session           SessionAPI
	rootPath          string        // resolved root directory; empty until initialize completes
	conn              jsonrpc2.Conn // set by Run; used by handleExit to close the connection
	subscriberStarted bool
	subscriberDone    chan struct{}
	subscriberCancel  func()

	docs   *docStore
	timers *debouncer
}

// NewServer creates a Server with the given options.
func NewServer(opts ...ServerOption) *Server {
	cfg := &serverConfig{
		clock:    time.Now,
		debounce: 100 * time.Millisecond,
		sessionFactory: func(root string) (SessionAPI, error) {
			return session.New(root)
		},
		logger: log.New(os.Stderr, "beancount-lsp: ", log.LstdFlags|log.Lmsgprefix),
	}
	for _, o := range opts {
		o(cfg)
	}
	return &Server{
		clock:          cfg.clock,
		sessionFactory: cfg.sessionFactory,
		logger:         cfg.logger,
		debounce:       cfg.debounce,
		docs:           newDocStore(),
		timers:         newDebouncer(),
		exitCode:       1, // default until clean shutdown
	}
}

// Run starts the JSON-RPC 2.0 server on stream and blocks until the connection
// ends. It returns the process exit code: 0 for a clean shutdown+exit
// sequence, 1 otherwise.
func (s *Server) Run(ctx context.Context, stream jsonrpc2.Stream) int {
	conn := jsonrpc2.NewConn(stream)
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	conn.Go(ctx, jsonrpc2.AsyncHandler(s.handler()))
	<-conn.Done()
	return s.exitCode
}

// handler returns the jsonrpc2.Handler that dispatches incoming messages.
func (s *Server) handler() jsonrpc2.Handler {
	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return safeHandle(ctx, s, reply, req)
	}
}

// ensureSession creates a session rooted at the directory of the given file
// URI if no session exists yet. It is idempotent; concurrent callers are safe.
func (s *Server) ensureSession(fileURI uri.URI) {
	s.mu.Lock()
	if s.session != nil {
		s.mu.Unlock()
		return
	}
	dir := filepath.Dir(fileURI.Filename())
	root := resolveRootFile(dir)
	s.rootPath = root
	factory := s.sessionFactory
	s.mu.Unlock()

	sess, err := factory(root)
	if err != nil {
		s.logger.Printf("session init error for %s: %v", root, err)
		return
	}

	s.mu.Lock()
	if s.session == nil {
		s.session = sess
		s.logger.Printf("session created (lazy) root=%s", root)
		s.mu.Unlock()
		s.startSubscriber()
	} else {
		// another goroutine beat us; discard
		_ = sess.Close()
		s.mu.Unlock()
	}
}

// resolveRootFile picks the first *.beancount file in dir, falling back to
// the directory itself (which will surface a loader error on Snapshot).
func resolveRootFile(dir string) string {
	matches, err := filepath.Glob(filepath.Join(dir, "*.beancount"))
	if err != nil || len(matches) == 0 {
		return dir
	}
	return matches[0]
}
