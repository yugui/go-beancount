package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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

	// A lone first string may be payee or narration; a following second string
	// makes it the payee.
	if kind == ContextPayee {
		lineEnd := len(src)
		if line+1 < len(lo) {
			lineEnd = lo[line+1]
		}
		suffix := strings.TrimRight(string(src[cursorOffset:lineEnd]), "\r\n")
		kind = disambiguateFirstString(suffix)
	}

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

	switch kind {
	case ContextPayee, ContextNarration, ContextPayeeOrNarration:
		filename := docURI.Filename()
		scope := completionScope{
			file:     filename,
			dir:      filepath.Dir(filename),
			accounts: enclosingPostingAccounts(ledger, filename, int(params.Position.Line)+1),
		}
		if kind == ContextNarration {
			scope.payee = firstQuotedString(linePrefix)
		}
		ranked := stringCompletionCandidates(kind, scope, ledger)
		items := make([]protocol.CompletionItem, 0, len(ranked))
		for i, c := range ranked {
			item := protocol.CompletionItem{
				Label:      c.label,
				Kind:       protocol.CompletionItemKindValue,
				InsertText: c.label,
				SortText:   fmt.Sprintf("%05d", i),
			}
			if d := roleDetail(c.roles); d != "" {
				item.Detail = d
			}
			items = append(items, item)
		}
		return reply(ctx, &protocol.CompletionList{IsIncomplete: false, Items: items}, nil)
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

// role is a bitmask marking whether a ranked candidate is known as a payee, a
// narration, or both. It populates the completion item Detail in the ambiguous
// first-string context.
type role uint8

const (
	rolePayee role = 1 << iota
	roleNarration
)

// rankedCandidate is a payee or narration completion candidate together with
// its priority group (0 = highest) and total occurrence count, used to order
// suggestions by contextual relevance then frequency.
type rankedCandidate struct {
	label string
	group int
	count int
	roles role
}

// completionScope captures the cursor's surroundings used to rank payee and
// narration candidates: the current file and its directory, the accounts of the
// enclosing transaction's postings (empty when none or unparsed), and the payee
// already present on the line (set only for narration completion).
type completionScope struct {
	file     string
	dir      string
	accounts map[ast.Account]struct{}
	payee    string
}

// stringCompletionCandidates returns payee and/or narration candidates ordered
// by priority group, then frequency-descending, then label. For
// ContextPayeeOrNarration both fields are merged into a shared
// file→directory→account-co-occurrence→rest grouping so the most contextually
// relevant strings surface first regardless of field.
func stringCompletionCandidates(kind ContextKind, scope completionScope, ledger *ast.Ledger) []rankedCandidate {
	if ledger == nil {
		return nil
	}
	var m map[string]*rankedCandidate
	switch kind {
	case ContextPayee:
		m = collectRanked(ledger, payeeOf, baseGroup(scope), rolePayee)
	case ContextNarration:
		m = collectRanked(ledger, narrationOf, narrationGroup(scope), roleNarration)
	case ContextPayeeOrNarration:
		m = collectRanked(ledger, payeeOf, baseGroup(scope), rolePayee)
		mergeRanked(m, collectRanked(ledger, narrationOf, baseGroup(scope), roleNarration))
	default:
		return nil
	}
	return sortRanked(m)
}

func payeeOf(tx *ast.Transaction) string     { return tx.Payee }
func narrationOf(tx *ast.Transaction) string { return tx.Narration }

// baseGroup ranks an occurrence by locality: same file (0), same directory (1),
// co-occurrence with a posting account already in the transaction (2), rest (3).
func baseGroup(scope completionScope) func(*ast.Transaction) int {
	return func(tx *ast.Transaction) int {
		switch {
		case tx.Span.Start.Filename == scope.file:
			return 0
		case filepath.Dir(tx.Span.Start.Filename) == scope.dir:
			return 1
		case txCooccurs(tx, scope.accounts):
			return 2
		default:
			return 3
		}
	}
}

// narrationGroup ranks a narration occurrence, prepending a top group for
// narrations used with the payee already typed on the line, then the baseGroup
// locality tiers shifted down by one.
func narrationGroup(scope completionScope) func(*ast.Transaction) int {
	return func(tx *ast.Transaction) int {
		if scope.payee != "" && tx.Payee == scope.payee {
			return 0
		}
		switch {
		case tx.Span.Start.Filename == scope.file:
			return 1
		case filepath.Dir(tx.Span.Start.Filename) == scope.dir:
			return 2
		case txCooccurs(tx, scope.accounts):
			return 3
		default:
			return 4
		}
	}
}

// txCooccurs reports whether any of tx's posting accounts is in accounts.
func txCooccurs(tx *ast.Transaction, accounts map[ast.Account]struct{}) bool {
	if len(accounts) == 0 {
		return false
	}
	for _, p := range tx.Postings {
		if _, ok := accounts[p.Account]; ok {
			return true
		}
	}
	return false
}

// collectRanked aggregates the non-empty strings returned by pick across all
// transactions, recording each label's best (lowest) group, occurrence count,
// and role.
func collectRanked(ledger *ast.Ledger, pick func(*ast.Transaction) string, groupOf func(*ast.Transaction) int, r role) map[string]*rankedCandidate {
	res := map[string]*rankedCandidate{}
	for _, d := range ledger.All() {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		s := pick(tx)
		if s == "" {
			continue
		}
		g := groupOf(tx)
		if c, ok := res[s]; ok {
			if g < c.group {
				c.group = g
			}
			c.count++
			c.roles |= r
		} else {
			res[s] = &rankedCandidate{label: s, group: g, count: 1, roles: r}
		}
	}
	return res
}

// mergeRanked folds src into dst, keeping the best group, summing counts, and
// unioning roles for labels present in both.
func mergeRanked(dst, src map[string]*rankedCandidate) {
	for label, c := range src {
		if e, ok := dst[label]; ok {
			if c.group < e.group {
				e.group = c.group
			}
			e.count += c.count
			e.roles |= c.roles
		} else {
			dst[label] = c
		}
	}
}

// sortRanked flattens m into a slice ordered by group ascending, then count
// descending, then label ascending.
func sortRanked(m map[string]*rankedCandidate) []rankedCandidate {
	if len(m) == 0 {
		return nil
	}
	out := make([]rankedCandidate, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.group != b.group {
			return a.group < b.group
		}
		if a.count != b.count {
			return a.count > b.count
		}
		return a.label < b.label
	})
	return out
}

// roleDetail renders the completion item Detail for a candidate's roles.
func roleDetail(r role) string {
	switch r {
	case rolePayee:
		return "payee"
	case roleNarration:
		return "narration"
	case rolePayee | roleNarration:
		return "payee, narration"
	default:
		return ""
	}
}

// enclosingPostingAccounts returns the set of accounts used by the postings of
// the transaction in file that spans line (1-based). It returns nil when no such
// transaction is found or it has no postings — the common case while a header
// string is still being typed and the transaction does not yet parse.
func enclosingPostingAccounts(ledger *ast.Ledger, file string, line int) map[ast.Account]struct{} {
	if ledger == nil {
		return nil
	}
	for _, d := range ledger.All() {
		tx, ok := d.(*ast.Transaction)
		if !ok || tx.Span.Start.Filename != file {
			continue
		}
		if line < tx.Span.Start.Line || line > tx.Span.End.Line {
			continue
		}
		if len(tx.Postings) == 0 {
			return nil
		}
		set := make(map[ast.Account]struct{}, len(tx.Postings))
		for _, p := range tx.Postings {
			set[p.Account] = struct{}{}
		}
		return set
	}
	return nil
}

// firstQuotedString returns the contents of the first double-quoted string in s,
// or "" when none is present. Escape sequences are not interpreted.
func firstQuotedString(s string) string {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], '"')
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
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
