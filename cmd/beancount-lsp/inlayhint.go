package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// inlayHintKindType is the LSP InlayHintKind for type-like annotations.
// The hand-rolled value matches LSP 3.17 (1 = Type); go.lsp.dev/protocol
// v0.12.0 predates inlay hints and provides no constant.
const inlayHintKindType = 1

// inlayHintParams is the request payload for textDocument/inlayHint.
// Hand-defined because go.lsp.dev/protocol v0.12.0 stops at LSP 3.16.
type inlayHintParams struct {
	TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
	Range        protocol.Range                  `json:"range"`
}

// inlayHint is the minimal LSP 3.17 InlayHint subset the server emits:
// a single inline label anchored at Position.
type inlayHint struct {
	Position     protocol.Position `json:"position"`
	Label        string            `json:"label"`
	Kind         int               `json:"kind,omitempty"`
	PaddingLeft  bool              `json:"paddingLeft,omitempty"`
	PaddingRight bool              `json:"paddingRight,omitempty"`
}

// handleInlayHint handles textDocument/inlayHint. It returns inline labels for
// implicit ledger information in the requested document: a commodity's display
// name and price at its directive date, an auto-posting's inferred amount, and
// a resolved cost for a posting whose cost spec omitted fields. Returns a null
// result when no session, source, or hints are available.
func (s *Server) handleInlayHint(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params inlayHintParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	docURI := uri.URI(params.TextDocument.URI)
	filename := docURI.Filename()
	src := s.sourceBytesFor(filename)
	if src == nil {
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
		s.logger.Printf("handleInlayHint: snapshot error: %v", err)
		return reply(ctx, nil, nil)
	}
	if ledger == nil {
		return reply(ctx, nil, nil)
	}

	absFile, err := filepath.Abs(filename)
	if err != nil {
		absFile = filename
	}

	lo := computeLineOffsets(src)
	hints := collectInlayHints(absFile, ledger, src, lo)
	hints = filterByRange(hints, params.Range)
	if len(hints) == 0 {
		return reply(ctx, nil, nil)
	}
	return reply(ctx, hints, nil)
}

// collectInlayHints assembles all inlay hints for absFile. booked is the
// booking-resolved ledger (auto amounts filled, cost specs resolved); src/lo
// are the current source bytes and line table. Currency tokens are annotated
// with their commodity's display name; postings are annotated with inferred
// amounts and resolved costs, detected by re-lowering src (unbooked) so the
// original nil-ness of amounts and cost-spec fields stays observable and
// correlating to booked postings by source span.
func collectInlayHints(absFile string, booked *ast.Ledger, src []byte, lo lineOffsets) []inlayHint {
	cst := syntax.Parse(string(src))
	omit := indexOmissions(ast.Lower(absFile, cst))

	hints := currencyNameHints(cst, commodityNameOverrides(booked), src, lo)
	hints = append(hints, postingHints(absFile, booked, omit, src, lo)...)
	return hints
}

// spanKey identifies a source span by file and byte range. Byte offsets are
// stable across the booked/unbooked correlation; booked siblings produced by
// multi-lot reduction share one key.
type spanKey struct {
	file       string
	start, end int
}

func keyOf(s ast.Span) spanKey {
	return spanKey{file: s.Start.Filename, start: s.Start.Offset, end: s.End.Offset}
}

// omission records which implicit values a source posting carries.
type omission struct {
	autoAmount  bool
	costOmitted bool
}

// indexOmissions records, per source posting span, whether the amount was
// elided and whether the cost spec omitted a resolvable field. Postings with
// neither are not indexed.
func indexOmissions(file *ast.File) map[spanKey]omission {
	m := make(map[spanKey]omission)
	for _, d := range file.Directives {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range txn.Postings {
			p := &txn.Postings[i]
			o := omission{
				autoAmount:  p.Amount == nil,
				costOmitted: costFieldsOmitted(p.Cost),
			}
			if o.autoAmount || o.costOmitted {
				m[keyOf(p.Span)] = o
			}
		}
	}
	return m
}

// costFieldsOmitted reports whether c is an unbooked cost spec missing a
// number, currency, or acquisition date — the fields booking always resolves
// to a meaningful value. A nil cost (no annotation) or a label-only omission
// returns false.
func costFieldsOmitted(c ast.CostHolder) bool {
	spec, ok := c.(*ast.CostSpec)
	if !ok || spec == nil {
		return false
	}
	if spec.PerUnit == nil && spec.Total == nil {
		return true
	}
	if spec.Currency == "" {
		return true
	}
	return spec.Date == nil
}

// commodityNameOverrides maps a currency to its commodity's display name, but
// only when a "name" metadata is set to something other than the currency code
// itself. Currencies without a meaningful name are omitted so that hints never
// redundantly repeat the code already on screen.
func commodityNameOverrides(ledger *ast.Ledger) map[string]string {
	m := make(map[string]string)
	for _, d := range ledger.All() {
		c, ok := d.(*ast.Commodity)
		if !ok {
			continue
		}
		if name := commodityDisplayName(c); name != c.Currency {
			m[c.Currency] = name
		}
	}
	return m
}

// currencyNameHints emits a display-name hint after every CURRENCY token whose
// commodity has a name override. Tokens inside a commodity directive are
// skipped: that declaration introduces the name and is not itself annotated.
func currencyNameHints(file *syntax.File, names map[string]string, src []byte, lo lineOffsets) []inlayHint {
	if len(names) == 0 || file == nil || file.Root == nil {
		return nil
	}
	var hints []inlayHint
	for _, child := range file.Root.Children {
		if child.Node == nil || child.Node.Kind == syntax.CommodityDirective {
			continue
		}
		collectCurrencyHints(child.Node, names, src, lo, &hints)
	}
	return hints
}

func collectCurrencyHints(n *syntax.Node, names map[string]string, src []byte, lo lineOffsets, out *[]inlayHint) {
	for _, c := range n.Children {
		switch {
		case c.Token != nil:
			if c.Token.Kind != syntax.CURRENCY {
				continue
			}
			name, ok := names[c.Token.Raw]
			if !ok {
				continue
			}
			*out = append(*out, inlayHint{
				Position:    byteOffsetToLSP(c.Token.End(), src, lo),
				Label:       name,
				Kind:        inlayHintKindType,
				PaddingLeft: true,
			})
		case c.Node != nil:
			collectCurrencyHints(c.Node, names, src, lo, out)
		}
	}
}

// postingHints emits inferred-amount and resolved-cost hints for postings whose
// source span is indexed in omit. Booked postings are grouped by span so that
// multi-lot reductions and multi-currency auto-postings collapse to one hint
// per source line.
func postingHints(absFile string, booked *ast.Ledger, omit map[spanKey]omission, src []byte, lo lineOffsets) []inlayHint {
	type group struct {
		span    ast.Span
		amounts []string
		costs   []string
	}
	groups := make(map[spanKey]*group)
	var order []spanKey

	for _, d := range booked.All() {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range txn.Postings {
			p := &txn.Postings[i]
			if p.Span.Start.Filename != absFile {
				continue
			}
			k := keyOf(p.Span)
			o, ok := omit[k]
			if !ok {
				continue
			}
			g := groups[k]
			if g == nil {
				g = &group{span: p.Span}
				groups[k] = g
				order = append(order, k)
			}
			if o.autoAmount && p.Amount != nil {
				g.amounts = append(g.amounts, fmtAmount(*p.Amount))
			}
			if o.costOmitted {
				if cost, ok := p.Cost.(*ast.Cost); ok && cost != nil {
					g.costs = append(g.costs, fmtCost(cost))
				}
			}
		}
	}

	var hints []inlayHint
	for _, k := range order {
		g := groups[k]
		pos := lineEndPosition(g.span, src, lo)
		if len(g.amounts) > 0 {
			hints = append(hints, inlayHint{
				Position:    pos,
				Label:       joinStripped(g.amounts, 2),
				Kind:        inlayHintKindType,
				PaddingLeft: true,
			})
		}
		if len(g.costs) > 0 {
			hints = append(hints, inlayHint{
				Position:    pos,
				Label:       joinStripped(g.costs, 2),
				Kind:        inlayHintKindType,
				PaddingLeft: true,
			})
		}
	}
	return hints
}

// lineEndPosition returns the LSP position at the end of the line containing
// span's start. Anchoring to the first line keeps the hint on the directive or
// posting header even when the span extends over trailing metadata lines.
func lineEndPosition(span ast.Span, src []byte, lo lineOffsets) protocol.Position {
	start := astPositionToLSP(span.Start, src, lo)
	lb := lineBytes(src, lo, int(start.Line))
	return protocol.Position{
		Line:      start.Line,
		Character: runeColToUTF16(lb, runeLen(lb)),
	}
}

// commodityDisplayName returns the commodity's "name" metadata value when
// present, otherwise the currency identifier itself.
func commodityDisplayName(c *ast.Commodity) string {
	if v, ok := c.Meta.Props["name"]; ok {
		if v.Kind == ast.MetaString {
			return v.String
		}
		return fmtMetaValue(v)
	}
	return c.Currency
}

// fmtAmount renders an amount as "<number> <currency>".
func fmtAmount(a ast.Amount) string {
	return a.Number.String() + " " + a.Currency
}

// fmtCost renders a booked cost as {number currency[, date][, "label"]}.
func fmtCost(c *ast.Cost) string {
	var sb strings.Builder
	sb.WriteByte('{')
	sb.WriteString(c.Number.String())
	if c.Currency != "" {
		sb.WriteByte(' ')
		sb.WriteString(c.Currency)
	}
	if !c.Date.IsZero() {
		sb.WriteString(", ")
		sb.WriteString(c.Date.Format("2006-01-02"))
	}
	if c.Label != "" {
		fmt.Fprintf(&sb, ", %q", c.Label)
	}
	sb.WriteByte('}')
	return sb.String()
}

// joinStripped joins up to k parts with ", "; any excess collapses into a
// "+N more" suffix so a single-line label stays short.
func joinStripped(parts []string, k int) string {
	if len(parts) <= k {
		return strings.Join(parts, ", ")
	}
	return strings.Join(parts[:k], ", ") + fmt.Sprintf(" +%d more", len(parts)-k)
}

// filterByRange drops hints whose line falls outside r. A zero-valued range
// (client sent no viewport) is treated as unconstrained.
func filterByRange(hints []inlayHint, r protocol.Range) []inlayHint {
	if r.Start == r.End {
		return hints
	}
	out := hints[:0]
	for _, h := range hints {
		if h.Position.Line >= r.Start.Line && h.Position.Line <= r.End.Line {
			out = append(out, h)
		}
	}
	return out
}
