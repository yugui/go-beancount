// Command beancount-lsp is a Language Server Protocol (LSP 3.17) server for
// beancount plain-text accounting files. It communicates over stdio using the
// LSP framing protocol (Content-Length headers).
//
// Usage:
//
//	beancount-lsp [-plugin PATH ...]
//
// Out-of-tree postprocessors are loaded from goplug .so files named by the
// repeatable -plugin flag and by the BEANCOUNT_PLUGINS environment variable
// (a path-list-separated list, like PATH). Because editors launch the server
// rather than a shell, the environment variable is usually the more
// convenient channel. Loading their postprocessors lets the server surface
// the diagnostics they emit, keep the directives they generate, and stop
// reporting plugin-not-registered for plugin directives that name them.
//
// No positional arguments. Unknown flags cause exit 2. A plugin that fails to
// load is logged to stderr and skipped; the server keeps running.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"go.lsp.dev/jsonrpc2"

	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/ext/goplug/goplugflag"

	// register the bundled std/sprout postproc plugins
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/sprout"
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/std"
)

func main() {
	pluginPaths := goplugflag.Var(flag.CommandLine)
	flag.Parse()
	if flag.NArg() > 0 {
		log.Println("beancount-lsp: unexpected arguments")
		os.Exit(2)
	}

	os.Exit(run(context.Background(), *pluginPaths, os.Stdin, os.Stdout, os.Stderr))
}

// run starts the LSP server on the given stdio streams and blocks until the
// connection closes. It returns the process exit code (0 for clean shutdown
// after shutdown+exit, 1 for exit without prior shutdown or top-level failure).
//
// pluginPaths are the resolved plugin paths from goplugflag.Var (the
// BEANCOUNT_PLUGINS environment variable followed by the -plugin flags). They
// are loaded before the server starts, since plugin registration must complete
// before any document is parsed. A plugin that fails to load is reported and
// skipped rather than aborting startup, so a stale path in an editor's
// configuration cannot leave the user with a dead server.
func run(ctx context.Context, pluginPaths []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	logger := log.New(stderr, "beancount-lsp: ", log.LstdFlags|log.Lmsgprefix)

	loadPlugins(pluginPaths, logger)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	srv := NewServer(WithLogger(logger))

	rwc := struct {
		io.Reader
		io.Writer
		io.Closer
	}{stdin, stdout, io.NopCloser(stdin)}
	stream := jsonrpc2.NewStream(rwc)

	logger.Println("started")
	code := srv.Run(ctx, stream)
	logger.Printf("exiting with code %d", code)
	return code
}

// loadPlugins loads the goplug postprocessors named by pluginPaths, keeping
// the server running when one fails (see run).
func loadPlugins(pluginPaths []string, logger *log.Logger) {
	if err := goplug.LoadAll(pluginPaths); err != nil {
		logger.Printf("continuing without the plugin(s): %v", err)
	}
}
