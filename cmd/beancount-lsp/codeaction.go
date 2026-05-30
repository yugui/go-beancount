package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
	"github.com/yugui/go-beancount/pkg/printer"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handleCodeAction handles textDocument/codeAction. Two refactor.rewrite
// actions are offered per matching posting:
//
//   - "Expand auto-balanced amount" when the source posting omits Amount and
//     booking inferred one.
//   - "Expand cost specification" when the source posting carries an
//     abbreviated *ast.CostSpec that booking resolved to a concrete *ast.Cost.
//
// Multi-lot reductions and other 1→N booking expansions are emitted as
// consecutive lines sharing the source posting's indent.
//
// Returns an empty array when no action applies (no session, no snapshot,
// posting outside any known transaction, or booking failed for the
// transaction).
func (s *Server) handleCodeAction(ctx context.Context, reply jsonrpc2.Replier, raw json.RawMessage) error {
	var params protocol.CodeActionParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return reply(ctx, nil, jsonrpc2.ErrInvalidRequest)
	}

	if !codeActionKindRequested(params.Context.Only, protocol.RefactorRewrite) {
		return reply(ctx, []protocol.CodeAction{}, nil)
	}

	docURI := uri.URI(params.TextDocument.URI)
	filename := docURI.Filename()
	src := s.sourceBytesFor(filename)
	if src == nil {
		return reply(ctx, []protocol.CodeAction{}, nil)
	}
	lo := computeLineOffsets(src)
	reqStart := lspPositionToByte(params.Range.Start, src, lo)
	reqEnd := lspPositionToByte(params.Range.End, src, lo)

	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		return reply(ctx, []protocol.CodeAction{}, nil)
	}

	ledger, err := sess.Snapshot(ctx)
	if err != nil || ledger == nil {
		return reply(ctx, []protocol.CodeAction{}, nil)
	}

	file := findFile(ledger, filename)
	if file == nil {
		return reply(ctx, []protocol.CodeAction{}, nil)
	}

	bookedBySpan := collectBookedPostingsByOffset(ledger, filename)

	var actions []protocol.CodeAction
	for _, d := range file.Directives {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range txn.Postings {
			p := &txn.Postings[i]
			if !spanOverlapsByteRange(p.Span, reqStart, reqEnd) {
				continue
			}

			wantAuto := p.Amount == nil
			wantCost := isAbbreviatedCostSpec(p.Cost)
			if !wantAuto && !wantCost {
				continue
			}

			booked := bookedBySpan[p.Span.Start.Offset]
			if !bookingSucceeded(booked) {
				continue
			}

			lineStart, lineEnd := lineSpanForByteRange(src, lo, p.Span.Start.Offset, p.Span.End.Offset)
			indent := extractIndent(src, lineStart)
			replaceRange := protocol.Range{
				Start: byteOffsetToLSP(lineStart, src, lo),
				End:   byteOffsetToLSP(lineEnd, src, lo),
			}

			fmtOpts := codeActionPrinterOptions(ledger, indent)

			if wantAuto {
				if edit := buildPostingEdit(docURI, replaceRange, booked, txn, false, fmtOpts); edit != nil {
					actions = append(actions, protocol.CodeAction{
						Title: "Expand auto-balanced amount",
						Kind:  protocol.RefactorRewrite,
						Edit:  edit,
					})
				}
			}
			if wantCost {
				if edit := buildPostingEdit(docURI, replaceRange, booked, txn, true, fmtOpts); edit != nil {
					actions = append(actions, protocol.CodeAction{
						Title: "Expand cost specification",
						Kind:  protocol.RefactorRewrite,
						Edit:  edit,
					})
				}
			}
		}
	}

	if actions == nil {
		return reply(ctx, []protocol.CodeAction{}, nil)
	}
	return reply(ctx, actions, nil)
}

// codeActionKindRequested reports whether the client filter allows
// returning a code action of kind want. An empty Only list means "any kind"
// per the LSP spec.
func codeActionKindRequested(only []protocol.CodeActionKind, want protocol.CodeActionKind) bool {
	if len(only) == 0 {
		return true
	}
	for _, k := range only {
		if k == want || k == protocol.Refactor {
			return true
		}
	}
	return false
}

// findFile returns the *ast.File in ledger whose Filename equals filename,
// or nil when none match.
func findFile(ledger *ast.Ledger, filename string) *ast.File {
	for _, f := range ledger.Files {
		if f.Filename == filename {
			return f
		}
	}
	return nil
}

// collectBookedPostingsByOffset builds a map from a posting's source-byte
// start offset to the slice of booked postings sharing it. Only postings in
// transactions whose Span.Start.Filename equals filename are included.
// Multi-lot expansion produces multiple booked postings for one source
// offset; the slice preserves the reducer's child order.
func collectBookedPostingsByOffset(ledger *ast.Ledger, filename string) map[int][]*ast.Posting {
	out := map[int][]*ast.Posting{}
	for _, d := range ledger.All() {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range txn.Postings {
			p := &txn.Postings[i]
			if p.Span.Start.Filename != filename {
				continue
			}
			out[p.Span.Start.Offset] = append(out[p.Span.Start.Offset], p)
		}
	}
	return out
}

// bookingSucceeded reports whether the booked-side postings carry a fully
// resolved Amount on every entry. A still-nil Amount means the booking pass
// could not resolve the residual for this posting; the code action declines
// to expand it.
func bookingSucceeded(booked []*ast.Posting) bool {
	if len(booked) == 0 {
		return false
	}
	for _, p := range booked {
		if p.Amount == nil {
			return false
		}
	}
	return true
}

// isAbbreviatedCostSpec reports whether c is a parse-tier CostSpec that
// booking would normally fill in. Already-booked *ast.Cost values return
// false (nothing left to expand); a complete CostSpec with both number
// (PerUnit or Total) and Date also returns false.
func isAbbreviatedCostSpec(h ast.CostHolder) bool {
	c, ok := h.(*ast.CostSpec)
	if !ok || c == nil {
		return false
	}
	if c.PerUnit == nil && c.Total == nil {
		return true
	}
	if c.Date == nil {
		return true
	}
	return false
}

// spanOverlapsByteRange reports whether [span.Start.Offset, span.End.Offset)
// intersects [reqStart, reqEnd]. The interval is treated as inclusive on
// reqEnd so a zero-width cursor exactly at span.End still triggers.
func spanOverlapsByteRange(span ast.Span, reqStart, reqEnd int) bool {
	if span.Start.Offset == 0 && span.End.Offset == 0 {
		return false
	}
	return span.End.Offset >= reqStart && span.Start.Offset <= reqEnd
}

// lineSpanForByteRange expands [startOff, endOff] outward to the surrounding
// line boundaries: from the byte just after the preceding '\n' (or 0) to
// the byte just before the trailing '\n' (or len(src)). The returned range
// excludes the line terminator on both ends.
func lineSpanForByteRange(src []byte, lo lineOffsets, startOff, endOff int) (int, int) {
	startLine := lineForOffset(lo, startOff)
	endLine := lineForOffset(lo, endOff)

	start := lo[startLine]
	var end int
	if endLine+1 < len(lo) {
		end = lo[endLine+1] - 1
		if end > 0 && end <= len(src) && src[end-1] == '\r' {
			end--
		}
	} else {
		end = len(src)
	}
	return start, end
}

// lineForOffset returns the 0-based line index containing the given byte
// offset, clamping to the last line when offset is past EOF.
func lineForOffset(lo lineOffsets, offset int) int {
	for i := 0; i < len(lo)-1; i++ {
		if lo[i+1] > offset {
			return i
		}
	}
	return len(lo) - 1
}

// extractIndent returns the leading whitespace of the line starting at
// lineStart, copied as a fresh string.
func extractIndent(src []byte, lineStart int) string {
	end := lineStart
	for end < len(src) {
		c := src[end]
		if c != ' ' && c != '\t' {
			break
		}
		end++
	}
	return string(src[lineStart:end])
}

// codeActionPrinterOptions builds the format.Option slice the printer
// should use when rendering posting expansion text: indent width derived
// from the surrounding source, alignment preserved, comma grouping
// following the ledger's render_commas option, and display-context
// quantization from the ledger's precision profile.
func codeActionPrinterOptions(ledger *ast.Ledger, indent string) []format.Option {
	opts := []format.Option{
		format.WithIndentWidth(len(indent)),
		format.WithCommaGrouping(ledger.Options.Bool("render_commas")),
	}
	if ledger.PrecisionProfile != nil {
		opts = append(opts, format.WithDisplayContext(ledger.PrecisionProfile))
	}
	return opts
}

// buildPostingEdit assembles the WorkspaceEdit that replaces the source
// posting's line(s) with the rendering of booked. expandCost selects
// between the two action variants: when true, each booked posting's
// already-booked *ast.Cost is rewritten to a *ast.CostSpec with PerUnit
// set to the resolved Number so the printer emits the full
// {N CUR, DATE, "label"} form. When false, Cost is preserved verbatim;
// the action's purpose is purely to surface the inferred Amount.
//
// Returns nil if the printer fails on any posting; the caller skips offering
// the action rather than replacing the user's source with partial output.
func buildPostingEdit(
	docURI uri.URI,
	rng protocol.Range,
	booked []*ast.Posting,
	txn *ast.Transaction,
	expandCost bool,
	opts []format.Option,
) *protocol.WorkspaceEdit {
	var buf bytes.Buffer
	for _, b := range booked {
		render := *b
		if expandCost {
			render.Cost = explicitCostSpec(b.Cost)
		}
		if err := printer.FprintPosting(&buf, render, txn, opts...); err != nil {
			return nil
		}
	}
	text := strings.TrimRight(buf.String(), "\n")
	return &protocol.WorkspaceEdit{
		Changes: map[uri.URI][]protocol.TextEdit{
			docURI: {{Range: rng, NewText: text}},
		},
	}
}

// explicitCostSpec converts a booked *ast.Cost into a *ast.CostSpec whose
// PerUnit / Date / Label carry the resolved values, so the printer emits
// the full {N CUR, DATE, "label"} form via [formatCostHolder]. Non-booked
// inputs are returned unchanged: cash augmentations (nil Cost) and any
// *ast.CostSpec that slipped through pass back as-is.
func explicitCostSpec(h ast.CostHolder) ast.CostHolder {
	c, ok := h.(*ast.Cost)
	if !ok || c == nil {
		return h
	}
	spec := &ast.CostSpec{
		PerUnit:  ast.CloneDecimal(&c.Number),
		Currency: c.Currency,
		Label:    c.Label,
	}
	if !c.Date.IsZero() {
		d := c.Date
		spec.Date = &d
	}
	return spec
}
