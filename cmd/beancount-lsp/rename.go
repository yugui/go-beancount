package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handlePrepareRename handles textDocument/prepareRename. It returns the
// editable range for the symbol under the cursor, or null when the cursor is
// not on a renamable entity (tag, link, account, commodity). For tags and
// links the range covers only the name, excluding the leading sigil, so the
// sigil stays fixed during rename.
func (s *Server) handlePrepareRename(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Position     protocol.Position               `json:"position"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	docURI := uri.URI(params.TextDocument.URI)
	src := s.sourceBytesFor(docURI.Filename())
	if src == nil {
		return reply(ctx, nil, nil)
	}

	lo := computeLineOffsets(src)
	offset := lspPositionToByte(params.Position, src, lo)

	file := syntax.Parse(string(src))
	loc := LocateAt(file, offset)
	if loc.Token == nil {
		return reply(ctx, nil, nil)
	}

	if _, _, ok := renameTarget(loc); !ok {
		return reply(ctx, nil, nil)
	}

	r := renameEditRange(loc.Token, src, lo)
	return reply(ctx, &r, nil)
}

// handleRename handles textDocument/rename. It rewrites every occurrence of the
// tag, link, account, or commodity under the cursor across all files in the
// ledger and returns a WorkspaceEdit. Account renames are hierarchical: a
// rename of Assets:Bank also rewrites the matching prefix of descendant
// accounts such as Assets:Bank:Checking. Returns null when the cursor is not on
// a renamable entity, and a request error when newName is not a valid name for
// that entity kind.
func (s *Server) handleRename(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Position     protocol.Position               `json:"position"`
		NewName      string                          `json:"newName"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	docURI := uri.URI(params.TextDocument.URI)
	src := s.sourceBytesFor(docURI.Filename())
	if src == nil {
		return reply(ctx, nil, nil)
	}

	lo := computeLineOffsets(src)
	offset := lspPositionToByte(params.Position, src, lo)

	file := syntax.Parse(string(src))
	loc := LocateAt(file, offset)
	if loc.Token == nil {
		return reply(ctx, nil, nil)
	}

	kind, oldName, ok := renameTarget(loc)
	if !ok {
		return reply(ctx, nil, nil)
	}
	if !validNewName(kind, params.NewName) {
		return reply(ctx, nil, fmt.Errorf("invalid %s name %q: %w", renameKindLabel(kind), params.NewName, jsonrpc2.ErrInvalidParams))
	}

	changes := map[uri.URI][]protocol.TextEdit{}
	for _, fname := range s.renameFileSet(ctx, docURI.Filename()) {
		fsrc := s.sourceBytesFor(fname)
		if fsrc == nil {
			continue
		}
		edits := collectRenameEdits(fsrc, kind, oldName, params.NewName)
		if len(edits) > 0 {
			changes[uri.File(fname)] = edits
		}
	}

	return reply(ctx, &protocol.WorkspaceEdit{Changes: changes}, nil)
}

// renameTarget reports the renamable entity under the cursor. oldName is the
// identity used for matching: for tags and links it excludes the leading sigil;
// for accounts and commodities it is the verbatim token text. ok is false when
// the token is not one of the four renamable kinds.
func renameTarget(loc Located) (kind syntax.TokenKind, oldName string, ok bool) {
	tok := loc.Token
	switch tok.Kind {
	case syntax.TAG, syntax.LINK:
		return tok.Kind, tok.Raw[1:], true
	case syntax.ACCOUNT, syntax.CURRENCY:
		return tok.Kind, tok.Raw, true
	}
	return 0, "", false
}

// renameEditRange is the editable range for tok: the name only for tags and
// links (excluding the one-byte sigil), and the whole token otherwise.
func renameEditRange(tok *syntax.Token, src []byte, lo lineOffsets) protocol.Range {
	start := tok.Pos
	if tok.Kind == syntax.TAG || tok.Kind == syntax.LINK {
		start++
	}
	return protocol.Range{
		Start: byteOffsetToLSP(start, src, lo),
		End:   byteOffsetToLSP(tok.End(), src, lo),
	}
}

// collectRenameEdits returns the edits that rename every occurrence of the
// (kind, oldName) entity in src. Account matching is hierarchical: a token
// matches when it equals oldName or has oldName plus ':' as a prefix, and only
// the matched prefix is rewritten. Returned edits are in source order and never
// overlap, since each derives from a distinct token.
func collectRenameEdits(src []byte, kind syntax.TokenKind, oldName, newName string) []protocol.TextEdit {
	file := syntax.Parse(string(src))
	lo := computeLineOffsets(src)

	var edits []protocol.TextEdit
	for tok := range file.Root.Tokens() {
		if tok.Kind != kind {
			continue
		}
		var newText string
		switch kind {
		case syntax.TAG, syntax.LINK:
			if tok.Raw[1:] != oldName {
				continue
			}
			newText = newName
		case syntax.CURRENCY:
			if tok.Raw != oldName {
				continue
			}
			newText = newName
		case syntax.ACCOUNT:
			if tok.Raw != oldName && !strings.HasPrefix(tok.Raw, oldName+":") {
				continue
			}
			newText = newName + tok.Raw[len(oldName):]
		}
		edits = append(edits, protocol.TextEdit{
			Range:   renameEditRange(tok, src, lo),
			NewText: newText,
		})
	}
	return edits
}

// renameFileSet returns the absolute paths of every file the rename must scan:
// all files in the current ledger snapshot plus current, deduplicated. current
// guarantees the edited document is covered even when it is not (yet) part of
// the ledger.
func (s *Server) renameFileSet(ctx context.Context, current string) []string {
	seen := map[string]bool{}
	var files []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		files = append(files, name)
	}

	add(current)

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess != nil {
		if ledger, err := sess.Snapshot(ctx); err != nil {
			s.logger.Printf("handleRename: snapshot error: %v", err)
		} else if ledger != nil {
			for _, f := range ledger.Files {
				add(f.Filename)
			}
		}
	}
	return files
}

// validNewName reports whether newName is a syntactically valid name for the
// given entity kind, deferring to the canonical grammar in pkg/ast and
// pkg/syntax: accounts use [ast.Account.IsValid], commodities [syntax.IsCurrency],
// and tags and links [syntax.IsTagLinkName] (the name excludes the #/^ sigil).
func validNewName(kind syntax.TokenKind, newName string) bool {
	switch kind {
	case syntax.ACCOUNT:
		return ast.Account(newName).IsValid()
	case syntax.CURRENCY:
		return syntax.IsCurrency(newName)
	case syntax.TAG, syntax.LINK:
		return syntax.IsTagLinkName(newName)
	}
	return false
}

// renameKindLabel returns the human-readable label for kind used in error messages.
func renameKindLabel(kind syntax.TokenKind) string {
	switch kind {
	case syntax.TAG:
		return "tag"
	case syntax.LINK:
		return "link"
	case syntax.ACCOUNT:
		return "account"
	case syntax.CURRENCY:
		return "commodity"
	}
	return "symbol"
}
