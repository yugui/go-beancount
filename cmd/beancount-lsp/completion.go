package main

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleCompletion handles textDocument/completion. Returns a CompletionList
// derived from classifyContext on the line prefix up to the cursor, drawing
// candidates from the current session Snapshot.
func (s *Server) handleCompletion(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.CompletionParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	docURI := uri.URI(params.TextDocument.URI)
	src := s.sourceBytesFor(docURI.Filename())
	if src == nil {
		return reply(ctx, &protocol.CompletionList{IsIncomplete: false}, nil)
	}

	lo := computeLineOffsets(src)
	cursorOffset := lspPositionToByte(params.Position, src, lo)

	// Extract line prefix: bytes from start of line up to cursor offset.
	line := int(params.Position.Line)
	lineStart := 0
	if line < len(lo) {
		lineStart = lo[line]
	}
	linePrefix := string(src[lineStart:cursorOffset])

	kind := classifyContext(linePrefix)

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()

	var ledger *ast.Ledger
	if sess != nil {
		var err error
		ledger, err = sess.Snapshot(ctx)
		if err != nil {
			s.logger.Printf("handleCompletion: snapshot error: %v", err)
		}
	}

	candidates := completionCandidates(kind, linePrefix, ledger)
	items := make([]protocol.CompletionItem, 0, len(candidates))
	compKind := completionItemKind(kind)
	for _, c := range candidates {
		items = append(items, protocol.CompletionItem{
			Label: c,
			Kind:  compKind,
		})
	}

	return reply(ctx, &protocol.CompletionList{IsIncomplete: false, Items: items}, nil)
}

// completionCandidates returns a sorted, deduplicated list of candidate labels
// for the given context kind, drawn from the snapshot ledger.
func completionCandidates(kind ContextKind, linePrefix string, ledger *ast.Ledger) []string {
	seen := make(map[string]struct{})

	switch kind {
	case ContextAccount:
		if ledger != nil {
			for _, d := range ledger.All() {
				if o, ok := d.(*ast.Open); ok {
					seen[string(o.Account)] = struct{}{}
				}
			}
		}

	case ContextCurrency:
		if ledger != nil {
			for _, d := range ledger.All() {
				switch v := d.(type) {
				case *ast.Commodity:
					seen[v.Currency] = struct{}{}
				case *ast.Open:
					for _, c := range v.Currencies {
						seen[c] = struct{}{}
					}
				}
			}
		}

	case ContextKeyword:
		if isDateFirstLine(linePrefix) {
			for k := range dateDirectiveKeywords {
				seen[k] = struct{}{}
			}
		} else {
			for k := range headerKeywords {
				seen[k] = struct{}{}
			}
		}

	case ContextFlag:
		return []string{"!", "*"}

	case ContextTag:
		if ledger != nil {
			for _, d := range ledger.All() {
				for _, tag := range tagsOf(d) {
					seen[tag] = struct{}{}
				}
			}
		}

	case ContextLink:
		if ledger != nil {
			for _, d := range ledger.All() {
				for _, link := range linksOf(d) {
					seen[link] = struct{}{}
				}
			}
		}

	case ContextInString, ContextUnknown:
		// no candidates
	}

	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// isDateFirstLine reports whether linePrefix starts with a date pattern,
// meaning candidates should be date-first directive keywords (open, close, …).
func isDateFirstLine(linePrefix string) bool {
	return reDatePrefix.MatchString(linePrefix)
}

func tagsOf(d ast.Directive) []string {
	switch v := d.(type) {
	case *ast.Transaction:
		return v.Tags
	case *ast.Note:
		return v.Tags
	case *ast.Document:
		return v.Tags
	}
	return nil
}

func linksOf(d ast.Directive) []string {
	switch v := d.(type) {
	case *ast.Transaction:
		return v.Links
	case *ast.Note:
		return v.Links
	case *ast.Document:
		return v.Links
	}
	return nil
}

// completionItemKind maps a ContextKind to the LSP CompletionItemKind.
func completionItemKind(kind ContextKind) protocol.CompletionItemKind {
	switch kind {
	case ContextAccount:
		return protocol.CompletionItemKindClass
	case ContextCurrency:
		return protocol.CompletionItemKindConstant
	case ContextKeyword:
		return protocol.CompletionItemKindKeyword
	case ContextFlag:
		return protocol.CompletionItemKindValue
	case ContextTag:
		return protocol.CompletionItemKindEnum
	case ContextLink:
		return protocol.CompletionItemKindReference
	default:
		return protocol.CompletionItemKindText
	}
}
