// Command beancount-lsp is a Language Server Protocol (LSP 3.17) server for
// beancount plain-text accounting files. It communicates over stdio using the
// LSP framing protocol (Content-Length headers).
//
// Usage:
//
//	beancount-lsp
//
// No positional arguments. Unknown flags cause exit 2.
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

	// register the bundled std/sprout postproc plugins
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/sprout"
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/std"
)

func main() {
	flag.Parse()
	if flag.NArg() > 0 {
		log.Println("beancount-lsp: unexpected arguments")
		os.Exit(2)
	}

	os.Exit(run(context.Background(), os.Stdin, os.Stdout, os.Stderr))
}

// run starts the LSP server on the given stdio streams and blocks until the
// connection closes. It returns the process exit code (0 for clean shutdown
// after shutdown+exit, 1 for exit without prior shutdown or top-level failure).
func run(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	logger := log.New(stderr, "beancount-lsp: ", log.LstdFlags|log.Lmsgprefix)

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
