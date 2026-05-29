package main

import (
	"context"
	"encoding/json"

	"github.com/yugui/go-beancount/pkg/format"
	"github.com/yugui/go-beancount/pkg/syntax"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleFormatting handles textDocument/formatting. Returns a single TextEdit
// replacing the entire document with the formatted output, or an empty array
// when the document is already formatted.
func (s *Server) handleFormatting(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.DocumentFormattingParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	u := uri.URI(params.TextDocument.URI)
	src, ok := s.docs.get(u)
	if !ok {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}

	current := string(src)
	formatted := format.Format(current, format.WithCommaGrouping(s.commaGrouping(ctx)))
	if formatted == current {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}

	lo := computeLineOffsets(src)
	eofPos := byteOffsetToLSP(len(src), src, lo)

	edits := []protocol.TextEdit{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   eofPos,
			},
			NewText: formatted,
		},
	}
	return reply(ctx, edits, nil)
}

// handleRangeFormatting handles textDocument/rangeFormatting. The client's
// requested range is expanded to the union of all top-level directive spans
// that overlap it, so the returned TextEdit may extend past the client's
// stated range to maintain whole-directive boundaries. Returns at most one
// TextEdit, or an empty array when no directives overlap the range or the
// covered text is already formatted.
func (s *Server) handleRangeFormatting(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.DocumentRangeFormattingParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	u := uri.URI(params.TextDocument.URI)
	src, ok := s.docs.get(u)
	if !ok {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}

	lo := computeLineOffsets(src)
	rangeStart := lspPositionToByte(params.Range.Start, src, lo)
	rangeEnd := lspPositionToByte(params.Range.End, src, lo)

	file := syntax.Parse(string(src))
	directives := topLevelDirectives(file)

	// Find directives overlapping [rangeStart, rangeEnd).
	first, last := -1, -1
	for i, d := range directives {
		dStart, dEnd := nodeByteRange(d)
		if dEnd > rangeStart && dStart < rangeEnd {
			if first < 0 {
				first = i
			}
			last = i
		}
	}

	if first < 0 {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}

	editStart, _ := nodeByteRange(directives[first])
	_, editEnd := nodeByteRange(directives[last])

	substring := string(src[editStart:editEnd])
	formatted := format.Format(substring, format.WithCommaGrouping(s.commaGrouping(ctx)))
	if formatted == substring {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}

	startPos := byteOffsetToLSP(editStart, src, lo)
	endPos := byteOffsetToLSP(editEnd, src, lo)

	edits := []protocol.TextEdit{
		{
			Range:   protocol.Range{Start: startPos, End: endPos},
			NewText: formatted,
		},
	}
	return reply(ctx, edits, nil)
}

// commaGrouping reports whether the active ledger enables render_commas.
// Returns false when no session or snapshot is available, preserving the
// default (no thousands separators).
func (s *Server) commaGrouping(ctx context.Context) bool {
	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return false
	}
	ledger, err := sess.Snapshot(ctx)
	if err != nil || ledger == nil {
		return false
	}
	return ledger.Options.Bool("render_commas")
}

// topLevelDirectives returns the direct child nodes of file.Root that are
// top-level beancount directives (including error/unrecognized nodes).
func topLevelDirectives(file *syntax.File) []*syntax.Node {
	if file.Root == nil {
		return nil
	}
	var out []*syntax.Node
	for _, c := range file.Root.Children {
		if c.Node != nil && isTopLevelDirective(c.Node.Kind) {
			out = append(out, c.Node)
		}
	}
	return out
}

// isTopLevelDirective reports whether k is a directive kind that appears as a
// direct child of FileNode.
func isTopLevelDirective(k syntax.NodeKind) bool {
	switch k {
	case syntax.TransactionDirective,
		syntax.OpenDirective,
		syntax.CloseDirective,
		syntax.CommodityDirective,
		syntax.BalanceDirective,
		syntax.PadDirective,
		syntax.NoteDirective,
		syntax.DocumentDirective,
		syntax.PriceDirective,
		syntax.EventDirective,
		syntax.QueryDirective,
		syntax.CustomDirective,
		syntax.OptionDirective,
		syntax.PluginDirective,
		syntax.IncludeDirective,
		syntax.PushtagDirective,
		syntax.PoptagDirective,
		syntax.ErrorNode,
		syntax.UnrecognizedLineNode:
		return true
	}
	return false
}

// nodeByteRange returns the [start, end) byte offsets of node's first and last
// tokens (excluding trivia). Returns (0, 0) for a node with no tokens.
func nodeByteRange(node *syntax.Node) (start, end int) {
	var first, last *syntax.Token
	for tok := range node.Tokens() {
		if first == nil {
			first = tok
		}
		last = tok
	}
	if first == nil {
		return 0, 0
	}
	return first.Pos, last.End()
}
