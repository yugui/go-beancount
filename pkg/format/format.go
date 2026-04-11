package format

import (
	"strings"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// Format formats a beancount source string using the CST.
// It parses, applies formatting rules, and returns the formatted source.
func Format(src string, opts ...Option) string {
	file := syntax.Parse(src)
	o := formatopt.Resolve(opts)
	f := &formatter{opts: o}
	f.formatFile(file.Root)
	return file.Root.FullText()
}

// formatter applies formatting rules to a parsed beancount CST.
type formatter struct {
	opts formatopt.Options
}

// formatFile processes the top-level FileNode.
func (f *formatter) formatFile(root *syntax.Node) {
	if root == nil {
		return
	}

	// Track how many directives we've seen for blank-line normalization.
	directivesSeen := 0
	for i := range root.Children {
		c := &root.Children[i]
		if c.Node == nil {
			continue
		}
		if isDirective(c.Node.Kind) {
			if directivesSeen > 0 {
				f.normalizeBlankLinesBefore(c.Node, f.opts.BlankLinesBetweenDirectives)
			}
			directivesSeen++
		}
		f.formatDirective(c.Node)
	}
}

// isDirective returns true for top-level directive node kinds (including error nodes).
func isDirective(k syntax.NodeKind) bool {
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

// normalizeBlankLinesBefore adjusts the leading trivia of a node's first token
// so that there are exactly n blank lines (n+1 newlines) between it and the
// previous directive.
func (f *formatter) normalizeBlankLinesBefore(node *syntax.Node, n int) {
	tok := firstToken(node)
	if tok == nil {
		return
	}

	// Rebuild leading trivia: preserve everything up to and including the first
	// newline (which terminates the previous line), then insert exactly n blank
	// lines, then keep any non-newline trivia that was after the last newline
	// (i.e., indentation/comments on the current line).

	trivia := tok.LeadingTrivia

	// Find the last newline index -- everything after it is "current line" trivia.
	lastNL := -1
	for i, tr := range trivia {
		if tr.Kind == syntax.NewlineTrivia {
			lastNL = i
		}
	}

	// Current-line trivia (indent etc.) is everything after the last newline.
	var currentLine []syntax.Trivia
	if lastNL >= 0 {
		currentLine = trivia[lastNL+1:]
	} else {
		// No newlines at all -- unusual, but preserve everything as current-line.
		currentLine = trivia
	}

	// Build new trivia: one newline to end the previous line, then n more
	// newlines for blank lines, then current-line trivia.
	var newTrivia []syntax.Trivia
	for range n + 1 {
		newTrivia = append(newTrivia, syntax.Trivia{Kind: syntax.NewlineTrivia, Raw: "\n"})
	}
	newTrivia = append(newTrivia, currentLine...)
	tok.LeadingTrivia = newTrivia
}

// formatDirective dispatches formatting for a directive node.
func (f *formatter) formatDirective(node *syntax.Node) {
	switch node.Kind {
	case syntax.ErrorNode, syntax.UnrecognizedLineNode:
		// Pass through unchanged.
		return
	case syntax.TransactionDirective:
		f.formatTransaction(node)
	default:
		// For non-transaction directives, just fix metadata indent.
		f.formatMetadataLines(node, 1)
	}

	// Comma grouping applies to all directives.
	f.formatCommaGrouping(node)
}

// formatTransaction handles indentation, metadata, and amount alignment for
// a transaction directive.
func (f *formatter) formatTransaction(txn *syntax.Node) {
	indent := strings.Repeat(" ", f.opts.IndentWidth)
	doubleIndent := strings.Repeat(" ", f.opts.IndentWidth*2)

	for i := range txn.Children {
		c := &txn.Children[i]
		if c.Node == nil {
			continue
		}
		switch c.Node.Kind {
		case syntax.PostingNode:
			f.setIndent(c.Node, indent)
			// Metadata inside a posting gets double indent.
			for j := range c.Node.Children {
				cc := &c.Node.Children[j]
				if cc.Node != nil && cc.Node.Kind == syntax.MetadataLineNode {
					f.setIndent(cc.Node, doubleIndent)
				}
			}
		case syntax.MetadataLineNode:
			// Transaction-level metadata gets single indent.
			f.setIndent(c.Node, indent)
		}
	}

	if f.opts.AlignAmounts {
		f.alignPostingAmounts(txn)
	}
}

// formatMetadataLines fixes indentation of metadata lines that are direct
// children of a directive. level is the indent multiplier (1 for directive
// metadata).
func (f *formatter) formatMetadataLines(node *syntax.Node, level int) {
	indent := strings.Repeat(" ", f.opts.IndentWidth*level)
	for i := range node.Children {
		c := &node.Children[i]
		if c.Node != nil && c.Node.Kind == syntax.MetadataLineNode {
			f.setIndent(c.Node, indent)
		}
	}
}

// setIndent adjusts the leading trivia of a node's first token so that after
// the last newline, the whitespace matches the desired indent string.
func (f *formatter) setIndent(node *syntax.Node, indent string) {
	tok := firstToken(node)
	if tok == nil {
		return
	}

	trivia := tok.LeadingTrivia

	// Find the last newline.
	lastNL := -1
	for i, tr := range trivia {
		if tr.Kind == syntax.NewlineTrivia {
			lastNL = i
		}
	}

	if lastNL < 0 {
		// No newline found -- this token is on the same line as something
		// before it; don't touch it.
		return
	}

	// Keep everything up to and including the last newline, then replace
	// what follows with the correct whitespace indent.
	newTrivia := make([]syntax.Trivia, lastNL+1, lastNL+2)
	copy(newTrivia, trivia[:lastNL+1])
	if indent != "" {
		newTrivia = append(newTrivia, syntax.Trivia{Kind: syntax.WhitespaceTrivia, Raw: indent})
	}
	tok.LeadingTrivia = newTrivia
}

// alignPostingAmounts aligns posting amounts within a transaction so that each
// posting's currency token (from the direct AmountNode child) ends at
// AmountColumn.
func (f *formatter) alignPostingAmounts(txn *syntax.Node) {
	postings := txn.FindAllNodes(syntax.PostingNode)
	for _, p := range postings {
		f.alignPostingAmount(p)
	}
}

// alignPostingAmount aligns a single posting's amount.
func (f *formatter) alignPostingAmount(posting *syntax.Node) {
	// Find the ACCOUNT token.
	acctTok := posting.FindToken(syntax.ACCOUNT)
	if acctTok == nil {
		return
	}

	// Find the direct AmountNode child.
	amtNode := posting.FindNode(syntax.AmountNode)
	if amtNode == nil {
		return
	}

	// Compute the indent width (leading whitespace of the posting).
	indentWidth := f.postingIndentWidth(posting)

	// Compute account display width.
	acctWidth := formatopt.StringWidth(acctTok.Raw, f.opts.EastAsianAmbiguousWidth)

	// Account for posting flag if present.
	flagWidth := 0
	if len(posting.Children) > 0 && posting.Children[0].Token != nil {
		flagTok := posting.Children[0].Token
		if flagTok.Kind == syntax.STAR || flagTok.Kind == syntax.BANG {
			flagWidth = formatopt.StringWidth(flagTok.Raw, f.opts.EastAsianAmbiguousWidth) + 1 // +1 for space after flag
		}
	}

	// Compute the display width of the amount text (from the first token of
	// the amount through the currency token, inclusive, not counting the gap
	// between account and amount).
	amtWidth := f.amountDisplayWidth(amtNode)

	// Calculate needed padding.
	usedWidth := indentWidth + flagWidth + acctWidth + amtWidth
	padding := f.opts.AmountColumn - usedWidth
	if padding < 2 {
		padding = 2
	}

	// Set the whitespace between account and amount. The gap may be stored
	// either in the ACCOUNT token's trailing trivia or in the amount's first
	// token's leading trivia. We normalize it: clear ACCOUNT trailing
	// whitespace and set the amount's first token's leading whitespace.
	amtFirstTok := firstToken(amtNode)
	if amtFirstTok == nil {
		return
	}

	// Remove whitespace from ACCOUNT trailing trivia.
	var newTrailing []syntax.Trivia
	for _, tr := range acctTok.TrailingTrivia {
		if tr.Kind != syntax.WhitespaceTrivia {
			newTrailing = append(newTrailing, tr)
		}
	}
	acctTok.TrailingTrivia = newTrailing

	// Remove existing whitespace from amount's first token leading trivia
	// and prepend our padding.
	var newLeading []syntax.Trivia
	newLeading = append(newLeading, syntax.Trivia{Kind: syntax.WhitespaceTrivia, Raw: strings.Repeat(" ", padding)})
	for _, tr := range amtFirstTok.LeadingTrivia {
		if tr.Kind != syntax.WhitespaceTrivia {
			newLeading = append(newLeading, tr)
		}
	}
	amtFirstTok.LeadingTrivia = newLeading
}

// postingIndentWidth returns the display width of the posting's indent.
func (f *formatter) postingIndentWidth(posting *syntax.Node) int {
	tok := firstToken(posting)
	if tok == nil {
		return 0
	}
	// Find whitespace after the last newline in leading trivia.
	trivia := tok.LeadingTrivia
	for i := len(trivia) - 1; i >= 0; i-- {
		if trivia[i].Kind == syntax.NewlineTrivia {
			// Sum whitespace after this newline.
			w := 0
			for j := i + 1; j < len(trivia); j++ {
				if trivia[j].Kind == syntax.WhitespaceTrivia {
					w += formatopt.StringWidth(trivia[j].Raw, f.opts.EastAsianAmbiguousWidth)
				}
			}
			return w
		}
	}
	return 0
}

// amountDisplayWidth computes the display width of the text of an AmountNode,
// excluding the leading trivia of its first token (the gap between account and
// amount) and the trailing trivia of its last token (newline etc.).
func (f *formatter) amountDisplayWidth(amtNode *syntax.Node) int {
	// Collect all tokens to identify the last one.
	var tokens []*syntax.Token
	for tok := range amtNode.Tokens() {
		tokens = append(tokens, tok)
	}
	if len(tokens) == 0 {
		return 0
	}

	w := 0
	for i, tok := range tokens {
		if i > 0 {
			// Include leading trivia of non-first tokens.
			for _, tr := range tok.LeadingTrivia {
				w += formatopt.StringWidth(tr.Raw, f.opts.EastAsianAmbiguousWidth)
			}
		}
		w += formatopt.StringWidth(tok.Raw, f.opts.EastAsianAmbiguousWidth)
		if i < len(tokens)-1 {
			// Include trailing trivia of non-last tokens.
			for _, tr := range tok.TrailingTrivia {
				w += formatopt.StringWidth(tr.Raw, f.opts.EastAsianAmbiguousWidth)
			}
		}
	}
	return w
}

// formatCommaGrouping adds or removes commas from NUMBER tokens.
func (f *formatter) formatCommaGrouping(node *syntax.Node) {
	for tok := range node.Tokens() {
		if tok.Kind != syntax.NUMBER {
			continue
		}
		if f.opts.CommaGrouping {
			tok.Raw = formatopt.InsertCommas(tok.Raw)
		} else {
			tok.Raw = formatopt.StripCommas(tok.Raw)
		}
	}
}

// firstToken returns the first token in a node's subtree, or nil.
func firstToken(node *syntax.Node) *syntax.Token {
	for tok := range node.Tokens() {
		return tok
	}
	return nil
}
