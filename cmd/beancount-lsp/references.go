package main

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleReferences returns every source-written occurrence of the account,
// commodity, tag, or link under the cursor across all ledger files. For
// accounts and commodities ReferenceContext.IncludeDeclaration controls whether
// the declaring open/commodity directive token is included; for tags and links
// it is a no-op. Only occurrences backed by a real source position are returned.
// The result is a sorted, deduplicated []protocol.Location; it is never null
// and never a JSON-RPC error for a well-formed request. An empty slice means no
// occurrences.
func (s *Server) handleReferences(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.ReferenceParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	docURI := uri.URI(params.TextDocument.URI)
	src := s.sourceBytesFor(docURI.Filename())
	if src == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}

	lo := computeLineOffsets(src)
	offset := lspPositionToByte(params.Position, src, lo)

	file := syntax.Parse(string(src))
	loc := LocateAt(file, offset)
	if loc.Token == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}

	kind, name, ok := renameTarget(loc)
	if !ok {
		return reply(ctx, []protocol.Location{}, nil)
	}

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}
	ledger, err := sess.Snapshot(ctx)
	if err != nil {
		s.logger.Printf("handleReferences: snapshot error: %v", err)
		return reply(ctx, []protocol.Location{}, nil)
	}
	if ledger == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}

	files := s.ledgerFileSet(ctx, docURI.Filename())

	var locations []protocol.Location
	switch kind {
	case syntax.ACCOUNT, syntax.CURRENCY:
		locations = s.referencesForToken(kind, name, files, params.Context.IncludeDeclaration)
	case syntax.TAG, syntax.LINK:
		locations = s.referencesForTagLink(kind, name, files, ledger)
	}

	return reply(ctx, sortDedupLocations(locations), nil)
}

// referencesForToken finds every account or commodity occurrence whose token
// text equals name exactly, re-walking the concrete syntax of each file. When
// includeDecl is false the matching token inside the declaring directive
// (OpenDirective for accounts, CommodityDirective for commodities) is dropped.
// Files with no readable source are skipped rather than aborting.
func (s *Server) referencesForToken(kind syntax.TokenKind, name string, files []string, includeDecl bool) []protocol.Location {
	declKind := syntax.OpenDirective
	if kind == syntax.CURRENCY {
		declKind = syntax.CommodityDirective
	}

	var locations []protocol.Location
	for _, fname := range files {
		fsrc := s.sourceBytesFor(fname)
		if fsrc == nil {
			continue
		}
		lo := computeLineOffsets(fsrc)
		file := syntax.Parse(string(fsrc))
		fileURI := uri.File(fname)

		for _, child := range file.Root.Children {
			node := child.Node
			if node == nil {
				continue
			}
			isDecl := node.Kind == declKind
			for tok := range node.Tokens() {
				if tok.Kind != kind || tok.Raw != name {
					continue
				}
				if isDecl && !includeDecl {
					continue
				}
				locations = append(locations, protocol.Location{
					URI:   fileURI,
					Range: tokenRange(tok, fsrc, lo),
				})
			}
		}
	}
	return locations
}

// referencesForTagLink finds every tag or link occurrence named name (without
// the #/^ sigil). It combines two disjoint sources: (A) one location at the
// first-line range of each *ast.Transaction, *ast.Note, or *ast.Document whose
// .Tags (for TAG) or .Links (for LINK) contains name, emitted only when the
// directive has a real source span backed by readable bytes; and (B) one
// location per TAG/LINK token inside a PushtagDirective or PoptagDirective node,
// found by re-walking the concrete syntax. IncludeDeclaration does not apply to
// tags or links.
func (s *Server) referencesForTagLink(kind syntax.TokenKind, name string, files []string, ledger *ast.Ledger) []protocol.Location {
	var locations []protocol.Location

	for _, dir := range ledger.All() {
		var names []string
		var span ast.Span
		switch d := dir.(type) {
		case *ast.Transaction:
			names, span = tagsOrLinks(kind, d.Tags, d.Links), d.Span
		case *ast.Note:
			names, span = tagsOrLinks(kind, d.Tags, d.Links), d.Span
		case *ast.Document:
			names, span = tagsOrLinks(kind, d.Tags, d.Links), d.Span
		default:
			continue
		}
		if !containsString(names, name) {
			continue
		}
		fname := span.Start.Filename
		if fname == "" {
			continue
		}
		fsrc := s.sourceBytesFor(fname)
		if fsrc == nil {
			continue
		}
		locations = append(locations, protocol.Location{
			URI:   uri.File(fname),
			Range: directiveHeaderRange(span, fsrc),
		})
	}

	for _, fname := range files {
		fsrc := s.sourceBytesFor(fname)
		if fsrc == nil {
			continue
		}
		lo := computeLineOffsets(fsrc)
		file := syntax.Parse(string(fsrc))
		fileURI := uri.File(fname)

		for _, child := range file.Root.Children {
			node := child.Node
			if node == nil || (node.Kind != syntax.PushtagDirective && node.Kind != syntax.PoptagDirective) {
				continue
			}
			for tok := range node.Tokens() {
				if tok.Kind != kind || tok.Raw[1:] != name {
					continue
				}
				locations = append(locations, protocol.Location{
					URI:   fileURI,
					Range: tokenRange(tok, fsrc, lo),
				})
			}
		}
	}

	return locations
}

// tagsOrLinks returns tags when kind is TAG, links otherwise.
func tagsOrLinks(kind syntax.TokenKind, tags, links []string) []string {
	if kind == syntax.TAG {
		return tags
	}
	return links
}

// containsString reports whether want appears in ss.
func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// tokenRange is the LSP range covering tok exactly (Pos..End).
func tokenRange(tok *syntax.Token, src []byte, lo lineOffsets) protocol.Range {
	return protocol.Range{
		Start: byteOffsetToLSP(tok.Pos, src, lo),
		End:   byteOffsetToLSP(tok.End(), src, lo),
	}
}

// directiveHeaderRange is the LSP range of the directive's first source line:
// from span.Start.Offset to the end of that line.
func directiveHeaderRange(span ast.Span, src []byte) protocol.Range {
	lo := computeLineOffsets(src)
	start := byteOffsetToLSP(span.Start.Offset, src, lo)
	endByte := lo[start.Line] + len(lineBytes(src, lo, int(start.Line)))
	return protocol.Range{
		Start: start,
		End:   byteOffsetToLSP(endByte, src, lo),
	}
}

// sortDedupLocations sorts locations by (filename, start byte-equivalent
// position) and removes exact (filename, range) duplicates, yielding a
// non-nil deterministic slice.
func sortDedupLocations(locs []protocol.Location) []protocol.Location {
	sort.SliceStable(locs, func(i, j int) bool {
		a, b := locs[i], locs[j]
		if a.URI != b.URI {
			return a.URI < b.URI
		}
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		return a.Range.Start.Character < b.Range.Start.Character
	})

	out := make([]protocol.Location, 0, len(locs))
	var prev protocol.Location
	for i, l := range locs {
		if i > 0 && l.URI == prev.URI && l.Range == prev.Range {
			continue
		}
		out = append(out, l)
		prev = l
	}
	return out
}
