package main

import (
	"context"
	"os"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// startSubscriber starts the goroutine that reads ledger updates from the
// session and publishes diagnostics. It is idempotent: repeated calls are
// no-ops. The goroutine is joined during handleExit via subscriberDone.
func (s *Server) startSubscriber() {
	s.mu.Lock()
	if s.subscriberStarted {
		s.mu.Unlock()
		return
	}
	sess := s.session
	if sess == nil {
		s.mu.Unlock()
		return
	}
	s.subscriberStarted = true
	s.mu.Unlock()

	gctx, gcancel := context.WithCancel(context.Background())

	sub, subCancel := sess.Subscribe()
	done := make(chan struct{})
	s.mu.Lock()
	s.subscriberDone = done
	s.subscriberCancel = func() { gcancel(); subCancel() }
	s.mu.Unlock()

	go func() {
		defer close(done)
		defer gcancel()
		pub := &diagPublisher{prev: make(map[uri.URI]struct{})}
		for ledger := range sub {
			pub.publish(gctx, s, ledger)
		}
	}()
}

// diagPublisher tracks which files had diagnostics on the previous publish
// cycle so it can send empty-array clears for resolved files.
type diagPublisher struct {
	prev map[uri.URI]struct{}
}

// publish groups ledger diagnostics by file and emits publishDiagnostics
// notifications. Files that had diagnostics in the previous cycle but not the
// current one receive an empty-array notification (LSP clear).
func (p *diagPublisher) publish(ctx context.Context, s *Server, ledger *ast.Ledger) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return
	}

	byFile := make(map[string][]ast.Diagnostic)
	droppedEmpty := false
	for _, d := range ledger.Diagnostics {
		f := d.Span.Start.Filename
		if f == "" {
			if !droppedEmpty {
				s.logger.Printf("publishDiagnostics: dropping diagnostic with empty filename: %s", d.Message)
				droppedEmpty = true
			}
			continue
		}
		byFile[f] = append(byFile[f], d)
	}

	current := make(map[uri.URI]struct{}, len(byFile))

	for filename, diags := range byFile {
		u := uri.File(filename)
		current[u] = struct{}{}

		src := s.sourceBytesFor(filename)
		lo := computeLineOffsets(src)

		lspDiags := make([]protocol.Diagnostic, 0, len(diags))
		for _, d := range diags {
			lspDiags = append(lspDiags, convertDiagnostic(d, src, lo, s))
		}

		s.sendPublish(ctx, conn, u, lspDiags)
	}

	for u := range p.prev {
		if _, ok := current[u]; !ok {
			s.sendPublish(ctx, conn, u, []protocol.Diagnostic{})
		}
	}

	p.prev = current
}

// convertDiagnostic converts an ast.Diagnostic to an LSP Diagnostic.
// d.Code is included only when non-empty, preserving LSP null semantics.
func convertDiagnostic(d ast.Diagnostic, src []byte, lo lineOffsets, s *Server) protocol.Diagnostic {
	diag := protocol.Diagnostic{
		Range:    astSpanToLSP(d.Span, src, lo),
		Severity: severityToLSP(d.Severity, s),
		Source:   "beancount",
		Message:  d.Message,
	}
	if d.Code != "" {
		diag.Code = d.Code
	}
	return diag
}

// severityToLSP maps ast.Severity to LSP DiagnosticSeverity.
func severityToLSP(sev ast.Severity, s *Server) protocol.DiagnosticSeverity {
	switch sev {
	case ast.Error:
		return protocol.DiagnosticSeverityError
	case ast.Warning:
		return protocol.DiagnosticSeverityWarning
	default:
		s.logger.Printf("publishDiagnostics: unknown severity %d, treating as error", sev)
		return protocol.DiagnosticSeverityError
	}
}

// sendPublish emits a textDocument/publishDiagnostics notification.
func (s *Server) sendPublish(ctx context.Context, conn interface {
	Notify(ctx context.Context, method string, params interface{}) error
}, u uri.URI, diags []protocol.Diagnostic) {
	params := &protocol.PublishDiagnosticsParams{
		URI:         u,
		Diagnostics: diags,
	}
	if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
		s.logger.Printf("publishDiagnostics notify %s: %v", u, err)
	}
}

// sourceBytesFor returns the source bytes for filename: docStore first, then
// disk. Returns nil on disk read error (diagnostic will have zero Range but
// will not be dropped).
func (s *Server) sourceBytesFor(filename string) []byte {
	u := uri.File(filename)
	if content, ok := s.docs.get(u); ok {
		return content
	}
	b, err := os.ReadFile(filename)
	if err != nil {
		s.logger.Printf("sourceBytesFor %s: %v", filename, err)
		return nil
	}
	return b
}
