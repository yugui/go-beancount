package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleDefinition handles textDocument/definition. Resolves the symbol
// at the cursor (include path, account name, currency identifier) to a
// Location. Returns an empty slice when no definition is found.
func (s *Server) handleDefinition(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.DefinitionParams
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

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}

	ledger, err := sess.Snapshot(ctx)
	if err != nil {
		s.logger.Printf("handleDefinition: snapshot error: %v", err)
		return reply(ctx, []protocol.Location{}, nil)
	}
	if ledger == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}

	locations := s.resolveDefinition(docURI, loc, ledger)
	return reply(ctx, locations, nil)
}

// resolveDefinition returns definition locations for the symbol at loc,
// or an empty slice when loc has no resolvable definition.
func (s *Server) resolveDefinition(docURI uri.URI, loc Located, ledger *ast.Ledger) []protocol.Location {
	tok := loc.Token
	dir := loc.Directive

	switch {
	case dir != nil && dir.Kind == syntax.IncludeDirective && tok.Kind == syntax.STRING:
		return s.definitionForInclude(docURI, tok)
	case tok.Kind == syntax.ACCOUNT:
		return s.definitionForAccount(tok.Raw, ledger)
	case tok.Kind == syntax.CURRENCY:
		return s.definitionForCurrency(tok.Raw, ledger)
	}
	return []protocol.Location{}
}

// definitionForInclude resolves an include string token to the absolute path of
// the included file. Returns empty when the path contains a glob wildcard or is empty.
func (s *Server) definitionForInclude(docURI uri.URI, tok *syntax.Token) []protocol.Location {
	raw := tok.Raw
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return []protocol.Location{}
	}
	pathArg := raw[1 : len(raw)-1]

	if pathArg == "" {
		return []protocol.Location{}
	}
	if strings.ContainsAny(pathArg, "*?[") {
		return []protocol.Location{}
	}

	var absPath string
	if filepath.IsAbs(pathArg) {
		absPath = pathArg
	} else {
		absPath = filepath.Join(filepath.Dir(docURI.Filename()), pathArg)
	}

	return []protocol.Location{{
		URI:   uri.File(absPath),
		Range: protocol.Range{},
	}}
}

func (s *Server) definitionForAccount(accountName string, ledger *ast.Ledger) []protocol.Location {
	for _, dir := range ledger.All() {
		open, ok := dir.(*ast.Open)
		if !ok || string(open.Account) != accountName {
			continue
		}
		filename := open.Span.Start.Filename
		src := s.sourceBytesFor(filename)
		lo := computeLineOffsets(src)
		return []protocol.Location{{
			URI:   uri.File(filename),
			Range: astSpanToLSP(open.Span, src, lo),
		}}
	}
	return []protocol.Location{}
}

func (s *Server) definitionForCurrency(currency string, ledger *ast.Ledger) []protocol.Location {
	for _, dir := range ledger.All() {
		commodity, ok := dir.(*ast.Commodity)
		if !ok || commodity.Currency != currency {
			continue
		}
		filename := commodity.Span.Start.Filename
		src := s.sourceBytesFor(filename)
		lo := computeLineOffsets(src)
		return []protocol.Location{{
			URI:   uri.File(filename),
			Range: astSpanToLSP(commodity.Span, src, lo),
		}}
	}
	return []protocol.Location{}
}
