package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleHover handles textDocument/hover. Returns Markdown content describing
// the symbol at the cursor: account metadata + open date for ACCOUNT tokens;
// commodity metadata + latest price-on-or-before context-date for CURRENCY
// tokens. Returns nil result for other token kinds.
//
// TODO: extend account hover to include balance as of context date once
// pkg/inventory exposes a suitable accumulation API.
func (s *Server) handleHover(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.HoverParams
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

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return reply(ctx, nil, nil)
	}

	ledger, err := sess.Snapshot(ctx)
	if err != nil {
		s.logger.Printf("handleHover: snapshot error: %v", err)
		return reply(ctx, nil, nil)
	}
	if ledger == nil {
		return reply(ctx, nil, nil)
	}

	var markdown string
	switch loc.Token.Kind {
	case syntax.ACCOUNT:
		markdown = s.hoverAccount(loc.Token.Raw, ledger)
	case syntax.CURRENCY:
		markdown = s.hoverCurrency(loc.Token.Raw, ledger, s.contextDateFor(loc))
	default:
		return reply(ctx, nil, nil)
	}

	if markdown == "" {
		return reply(ctx, nil, nil)
	}

	return reply(ctx, &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
	}, nil)
}

// contextDateFor returns the directive's own date when it has one, otherwise
// the server clock.
func (s *Server) contextDateFor(loc Located) time.Time {
	return dateFromSyntaxNode(loc.Directive, s.clock)
}

// dateFromSyntaxNode extracts the leading date token from node. Returns
// fallback() for directives without a date (option, plugin, include).
func dateFromSyntaxNode(node *syntax.Node, fallback func() time.Time) time.Time {
	for _, c := range node.Children {
		if c.Token != nil && c.Token.Kind == syntax.DATE {
			t, err := time.Parse("2006-01-02", c.Token.Raw)
			if err == nil {
				return t
			}
		}
	}
	return fallback()
}

// hoverAccount builds hover Markdown for an account name, or returns "" if the
// account was never opened.
func (s *Server) hoverAccount(account string, ledger *ast.Ledger) string {
	var open *ast.Open
	for _, d := range ledger.All() {
		o, ok := d.(*ast.Open)
		if ok && string(o.Account) == account {
			open = o
			break
		}
	}
	if open == nil {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** (account)\n", account)
	fmt.Fprintf(&sb, "\n*Opened*: %s\n", open.Date.Format("2006-01-02"))

	if len(open.Currencies) > 0 {
		fmt.Fprintf(&sb, "\n*Currencies*: %s\n", strings.Join(open.Currencies, ", "))
	}

	if open.Booking != ast.BookingDefault {
		fmt.Fprintf(&sb, "\n*Booking*: %s\n", open.Booking.String())
	}

	if len(open.Meta.Props) > 0 {
		sb.WriteString("\n*Metadata*\n")
		for _, line := range fmtMetadata(open.Meta) {
			fmt.Fprintf(&sb, "- %s\n", line)
		}
	}

	return sb.String()
}

// hoverCurrency builds hover Markdown for a currency/commodity token.
// It always attempts a price lookup even when no Commodity directive exists.
func (s *Server) hoverCurrency(currency string, ledger *ast.Ledger, contextDate time.Time) string {
	var commodity *ast.Commodity
	var latestPrice *ast.Price

	for _, d := range ledger.All() {
		switch v := d.(type) {
		case *ast.Commodity:
			if v.Currency == currency && commodity == nil {
				commodity = v
			}
		case *ast.Price:
			if v.Commodity == currency && !v.Date.After(contextDate) {
				if latestPrice == nil || !v.Date.Before(latestPrice.Date) {
					latestPrice = v
				}
			}
		}
	}

	if commodity == nil && latestPrice == nil {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** (commodity)\n", currency)

	if latestPrice != nil {
		fmt.Fprintf(&sb, "\nAs of %s: %s %s  *(price from %s)*\n",
			contextDate.Format("2006-01-02"),
			latestPrice.Amount.Number.String(),
			latestPrice.Amount.Currency,
			latestPrice.Date.Format("2006-01-02"),
		)
	} else {
		fmt.Fprintf(&sb, "\nNo price recorded as of %s.\n", contextDate.Format("2006-01-02"))
	}

	if commodity != nil && len(commodity.Meta.Props) > 0 {
		sb.WriteString("\n*Metadata*\n")
		for _, line := range fmtMetadata(commodity.Meta) {
			fmt.Fprintf(&sb, "- %s\n", line)
		}
	}

	return sb.String()
}

// fmtMetadata returns sorted "key: value" lines for m's properties.
func fmtMetadata(meta ast.Metadata) []string {
	keys := make([]string, 0, len(meta.Props))
	for k := range meta.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		v := meta.Props[k]
		lines = append(lines, fmt.Sprintf("%s: %s", k, fmtMetaValue(v)))
	}
	return lines
}

// fmtMetaValue renders a MetaValue as a human-readable string for hover Markdown.
func fmtMetaValue(v ast.MetaValue) string {
	switch v.Kind {
	case ast.MetaString:
		return fmt.Sprintf("%q", v.String)
	case ast.MetaAccount, ast.MetaCurrency:
		return v.String
	case ast.MetaDate:
		return v.Date.Format("2006-01-02")
	case ast.MetaTag:
		return "#" + v.String
	case ast.MetaLink:
		return "^" + v.String
	case ast.MetaNumber:
		return v.Number.String()
	case ast.MetaAmount:
		return fmt.Sprintf("%s %s", v.Amount.Number.String(), v.Amount.Currency)
	case ast.MetaBool:
		if v.Bool {
			return "TRUE"
		}
		return "FALSE"
	default:
		return fmt.Sprintf("<%s>", v.Kind)
	}
}
