package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleDocumentSymbol handles textDocument/documentSymbol requests.
// Returns a hierarchical []protocol.DocumentSymbol for every top-level
// directive in the matching file, or an empty array when the file is not
// found in the current ledger snapshot.
func (s *Server) handleDocumentSymbol(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.DocumentSymbolParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()

	if sess == nil {
		return reply(ctx, []protocol.DocumentSymbol{}, nil)
	}

	ledger, err := sess.Snapshot(ctx)
	if err != nil || ledger == nil {
		s.logger.Printf("handleDocumentSymbol: snapshot error: %v", err)
		return reply(ctx, []protocol.DocumentSymbol{}, nil)
	}

	docURI := uri.URI(params.TextDocument.URI)
	filename := docURI.Filename()

	var file *ast.File
	for _, f := range ledger.Files {
		if f.Filename == filename {
			file = f
			break
		}
	}
	if file == nil {
		s.logger.Printf("handleDocumentSymbol: no ledger file matches uri=%s", docURI)
		return reply(ctx, []protocol.DocumentSymbol{}, nil)
	}

	src := s.sourceBytesFor(filename)
	lo := computeLineOffsets(src)

	symbols := make([]protocol.DocumentSymbol, 0, len(file.Directives))
	for _, d := range file.Directives {
		sym := directiveToSymbol(d, src, lo)
		symbols = append(symbols, sym)
	}

	return reply(ctx, symbols, nil)
}

// directiveToSymbol converts an AST directive into a hierarchical
// DocumentSymbol. Unknown directive types produce an Object symbol with
// an empty name.
func directiveToSymbol(d ast.Directive, src []byte, lo lineOffsets) protocol.DocumentSymbol {
	span := d.DirSpan()
	r := astSpanToLSP(span, src, lo)

	name, kind := directiveNameAndKind(d)
	sym := protocol.DocumentSymbol{
		Name:           name,
		Kind:           kind,
		Range:          r,
		SelectionRange: r,
	}

	if txn, ok := d.(*ast.Transaction); ok {
		sym.Children = postingSymbols(txn, src, lo)
	}

	return sym
}

// directiveNameAndKind maps d to an LSP display name and SymbolKind.
// Unknown directive types map to SymbolKindObject with an empty name.
func directiveNameAndKind(d ast.Directive) (string, protocol.SymbolKind) {
	switch v := d.(type) {
	case *ast.Open:
		return string(v.Account), protocol.SymbolKindClass
	case *ast.Close:
		return string(v.Account), protocol.SymbolKindClass
	case *ast.Commodity:
		return v.Currency, protocol.SymbolKindConstant
	case *ast.Transaction:
		return txnName(v), protocol.SymbolKindEvent
	case *ast.Balance:
		return "balance " + string(v.Account), protocol.SymbolKindOperator
	case *ast.Pad:
		return "pad " + string(v.Account), protocol.SymbolKindOperator
	case *ast.Price:
		return fmt.Sprintf("%s → %s", v.Commodity, v.Amount.Currency), protocol.SymbolKindOperator
	case *ast.Include:
		return v.Path, protocol.SymbolKindFile
	case *ast.Option:
		return v.Key, protocol.SymbolKindProperty
	case *ast.Plugin:
		return v.Name, protocol.SymbolKindPackage
	case *ast.Event:
		return v.Name, protocol.SymbolKindEvent
	case *ast.Note:
		return string(v.Account), protocol.SymbolKindString
	case *ast.Document:
		return v.Path, protocol.SymbolKindFile
	case *ast.Custom:
		return v.TypeName, protocol.SymbolKindVariable
	case *ast.Query:
		return v.Name, protocol.SymbolKindFunction
	default:
		return "", protocol.SymbolKindObject
	}
}

// txnName returns the display name for a transaction symbol:
// narration, then payee if narration is empty, then "(transaction)".
func txnName(t *ast.Transaction) string {
	if t.Narration != "" {
		return t.Narration
	}
	if t.Payee != "" {
		return t.Payee
	}
	return "(transaction)"
}

// postingSymbols returns Field child symbols for each posting in txn,
// or nil when txn has no postings.
func postingSymbols(txn *ast.Transaction, src []byte, lo lineOffsets) []protocol.DocumentSymbol {
	if len(txn.Postings) == 0 {
		return nil
	}
	children := make([]protocol.DocumentSymbol, 0, len(txn.Postings))
	txnRange := astSpanToLSP(txn.Span, src, lo)
	for _, p := range txn.Postings {
		r := postingRange(p, src, lo, txnRange)
		children = append(children, protocol.DocumentSymbol{
			Name:           string(p.Account),
			Kind:           protocol.SymbolKindField,
			Range:          r,
			SelectionRange: r,
		})
	}
	return children
}

// postingRange returns the LSP Range for a posting. Uses the posting's own
// Span when non-zero, falling back to the transaction's range.
func postingRange(p ast.Posting, src []byte, lo lineOffsets, txnRange protocol.Range) protocol.Range {
	if p.Span.Start.Line > 0 {
		return astSpanToLSP(p.Span, src, lo)
	}
	return txnRange
}
