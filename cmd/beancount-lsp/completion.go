package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

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

	inString := false
	if kind == ContextMetaValue {
		if m := reMetaValue.FindStringSubmatch(linePrefix); len(m) > 0 {
			inString = strings.Count(m[2], `"`)%2 == 1
		}
	}

	// Account labels contain ':', which LSP clients treat as a word boundary.
	// An explicit TextEdit spanning the whole account token makes the client
	// both prefix-filter and replace it, instead of inserting after the colon.
	var acctRange *protocol.Range
	if kind == ContextAccount {
		trimmed := strings.TrimRight(linePrefix, " \t")
		if tok := trailingAccountToken(trimmed); tok != "" {
			startByte := lineStart + len(trimmed) - len(tok)
			acctRange = &protocol.Range{
				Start: byteOffsetToLSP(startByte, src, lo),
				End:   params.Position,
			}
		}
	}

	for _, c := range candidates {
		item := protocol.CompletionItem{Label: c, Kind: compKind}
		switch {
		case kind == ContextPayee || kind == ContextNarration:
			item.InsertText = c
		case kind == ContextMetaValue && inString && strings.HasPrefix(c, `"`) && strings.HasSuffix(c, `"`) && len(c) >= 2:
			// strip surrounding quotes: user already typed the opening "
			item.InsertText = c[1 : len(c)-1]
		case kind == ContextAccount && acctRange != nil:
			item.TextEdit = &protocol.TextEdit{Range: *acctRange, NewText: c}
			item.FilterText = c
		}
		items = append(items, item)
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
			// Transaction-flag shorthands are valid at the same position as
			// the "txn" keyword, so a manually-invoked completion at
			// "DATE " surfaces them alongside the directive list. They are
			// filtered out by the client's prefix match the moment the
			// user starts typing a letter.
			seen["*"] = struct{}{}
			seen["!"] = struct{}{}
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

	case ContextPayee:
		if ledger != nil {
			for _, d := range ledger.All() {
				if tx, ok := d.(*ast.Transaction); ok && tx.Payee != "" {
					seen[tx.Payee] = struct{}{}
				}
			}
		}

	case ContextNarration:
		// TODO: planned narration priority — Group 1 (same Payee via findEnclosingTransaction),
		// Group 2 (same Account), Group 3 (same File). Currently returns all distinct
		// narrations unfiltered. SortText prefixes "0"/"1"/"2" pending.
		if ledger != nil {
			for _, d := range ledger.All() {
				if tx, ok := d.(*ast.Transaction); ok && tx.Narration != "" {
					seen[tx.Narration] = struct{}{}
				}
			}
		}

	case ContextMetaKey:
		return collectMetadataKeys(ledger)

	case ContextMetaValue:
		currentKey := metaKeyFromLine(linePrefix)
		return collectMetadataValues(ledger, currentKey)

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
	sort.Strings(out)
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
	case ContextPayee, ContextNarration:
		return protocol.CompletionItemKindValue
	case ContextMetaKey:
		return protocol.CompletionItemKindProperty
	case ContextMetaValue:
		return protocol.CompletionItemKindValue
	default:
		return protocol.CompletionItemKindText
	}
}

// collectMetadataKeys collects metadata key names across all directives
// (including transaction postings) in the ledger, sorted by
// frequency-descending with alphabetical tiebreak.
func collectMetadataKeys(ledger *ast.Ledger) []string {
	if ledger == nil {
		return nil
	}
	counts := map[string]int{}
	for _, d := range ledger.All() {
		for k := range d.DirMeta().Props {
			counts[k]++
		}
		if tx, ok := d.(*ast.Transaction); ok {
			for _, p := range tx.Postings {
				for k := range p.Meta.Props {
					counts[k]++
				}
			}
		}
	}
	return sortedByFreqDescThenName(counts)
}

// collectMetadataValues collects formatted values for currentKey across all
// directives (including transaction postings) in the ledger, sorted by
// frequency-descending with alphabetical tiebreak. Numeric, date, and boolean
// values are excluded; string, account, currency, tag, and link values are
// included.
func collectMetadataValues(ledger *ast.Ledger, currentKey string) []string {
	if ledger == nil || currentKey == "" {
		return nil
	}
	counts := map[string]int{}
	collect := func(meta ast.Metadata) {
		if mv, ok := meta.Props[currentKey]; ok {
			if s := formatMetaValueForCompletion(mv); s != "" {
				counts[s]++
			}
		}
	}
	for _, d := range ledger.All() {
		collect(d.DirMeta())
		if tx, ok := d.(*ast.Transaction); ok {
			for _, p := range tx.Postings {
				collect(p.Meta)
			}
		}
	}
	return sortedByFreqDescThenName(counts)
}

// formatMetaValueForCompletion formats mv for use as a completion label.
// MetaString values are wrapped in double quotes. MetaAccount, MetaCurrency,
// MetaTag, and MetaLink values are returned bare. MetaNumber, MetaAmount,
// MetaDate, and MetaBool return "" (excluded from completion).
func formatMetaValueForCompletion(mv ast.MetaValue) string {
	switch mv.Kind {
	case ast.MetaString:
		return `"` + mv.String + `"`
	case ast.MetaAccount, ast.MetaCurrency, ast.MetaTag, ast.MetaLink:
		return mv.String
	default:
		// MetaNumber, MetaAmount, MetaDate, MetaBool: excluded by design.
		return ""
	}
}

// sortedByFreqDescThenName returns the keys of counts sorted by
// frequency-descending with alphabetical tiebreak.
func sortedByFreqDescThenName(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
