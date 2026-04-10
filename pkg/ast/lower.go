package ast

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// Lower converts a CST file into an AST file.
// Directives that contain syntax errors are skipped and recorded as diagnostics.
// Include resolution is not performed; Include directives appear as AST nodes.
func Lower(filename string, cst *syntax.File) *File {
	l := &lowerer{
		filename: filename,
		file:     &File{Filename: filename},
	}
	// Convert CST-level errors to diagnostics.
	for _, e := range cst.Errors {
		l.file.Diagnostics = append(l.file.Diagnostics, Diagnostic{
			Span:     l.spanFromOffset(e.Pos),
			Message:  e.Msg,
			Severity: Error,
		})
	}
	// Walk top-level children.
	if cst.Root != nil {
		for _, child := range cst.Root.Children {
			if child.Node != nil {
				l.lowerDirective(child.Node)
			}
			// Top-level tokens (like EOF) are ignored.
		}
	}
	return l.file
}

type lowerer struct {
	filename string
	file     *File
}

func (l *lowerer) lowerDirective(n *syntax.Node) {
	switch n.Kind {
	case syntax.ErrorNode, syntax.UnrecognizedLineNode:
		l.addDiagnostic(n, "syntax error")
	case syntax.OptionDirective:
		l.lowerOption(n)
	case syntax.PluginDirective:
		l.lowerPlugin(n)
	case syntax.IncludeDirective:
		l.lowerInclude(n)
	case syntax.PushtagDirective:
		// TODO: step 14
	case syntax.PoptagDirective:
		// TODO: step 14
	case syntax.OpenDirective:
		// TODO: step 4
	case syntax.CloseDirective:
		// TODO: step 4
	case syntax.CommodityDirective:
		// TODO: step 5
	case syntax.BalanceDirective:
		// TODO: step 6
	case syntax.PadDirective:
		// TODO: step 7
	case syntax.NoteDirective:
		// TODO: step 8
	case syntax.DocumentDirective:
		// TODO: step 9
	case syntax.PriceDirective:
		// TODO: step 10
	case syntax.EventDirective:
		// TODO: step 11
	case syntax.QueryDirective:
		// TODO: step 11
	case syntax.CustomDirective:
		// TODO: step 15
	case syntax.TransactionDirective:
		// TODO: step 12
	default:
		l.addDiagnostic(n, fmt.Sprintf("unknown directive kind: %s", n.Kind))
	}
}

// spanFromNode computes a Span covering the entire subtree of n,
// from the first token's position to the end of the last token.
func (l *lowerer) spanFromNode(n *syntax.Node) Span {
	var first, last *syntax.Token
	for t := range n.Tokens() {
		if first == nil {
			first = t
		}
		last = t
	}
	if first == nil {
		return Span{}
	}
	return Span{
		Start: l.posFromToken(first),
		End: Position{
			Filename: l.filename,
			Offset:   last.Pos + len(last.Raw),
		},
	}
}

// spanFromOffset creates a zero-width Span from a byte offset.
// Line and Column are left as zero; they may be populated in future steps.
func (l *lowerer) spanFromOffset(offset int) Span {
	pos := Position{
		Filename: l.filename,
		Offset:   offset,
	}
	return Span{Start: pos, End: pos}
}

// posFromToken creates a Position from a token.
func (l *lowerer) posFromToken(t *syntax.Token) Position {
	return Position{
		Filename: l.filename,
		Offset:   t.Pos,
	}
}

// addDiagnostic records an Error-severity diagnostic for the given node.
func (l *lowerer) addDiagnostic(n *syntax.Node, msg string) {
	l.file.Diagnostics = append(l.file.Diagnostics, Diagnostic{
		Span:     l.spanFromNode(n),
		Message:  msg,
		Severity: Error,
	})
}

// addDirective appends a directive to the file.
func (l *lowerer) addDirective(d Directive) {
	l.file.Directives = append(l.file.Directives, d)
}

// findTokens returns all direct child tokens with the given kind.
func findTokens(n *syntax.Node, kind syntax.TokenKind) []*syntax.Token {
	var result []*syntax.Token
	for _, c := range n.Children {
		if c.Token != nil && c.Token.Kind == kind {
			result = append(result, c.Token)
		}
	}
	return result
}

// unquoteString strips surrounding double quotes from a STRING token's Raw value.
// The caller must ensure t is a valid STRING token; if not, the raw value is returned unchanged.
func unquoteString(t *syntax.Token) string {
	s := t.Raw
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// lowerOption converts an OptionDirective CST node into an Option AST directive.
func (l *lowerer) lowerOption(n *syntax.Node) {
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 2 {
		l.addDiagnostic(n, "option directive requires two string arguments")
		return
	}
	l.addDirective(&Option{
		Span:  l.spanFromNode(n),
		Key:   unquoteString(strTokens[0]),
		Value: unquoteString(strTokens[1]),
	})
}

// lowerPlugin converts a PluginDirective CST node into a Plugin AST directive.
func (l *lowerer) lowerPlugin(n *syntax.Node) {
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 1 {
		l.addDiagnostic(n, "plugin directive requires a string argument")
		return
	}
	p := &Plugin{
		Span: l.spanFromNode(n),
		Name: unquoteString(strTokens[0]),
	}
	if len(strTokens) >= 2 {
		p.Config = unquoteString(strTokens[1])
	}
	l.addDirective(p)
}

// lowerInclude converts an IncludeDirective CST node into an Include AST directive.
func (l *lowerer) lowerInclude(n *syntax.Node) {
	strTokens := findTokens(n, syntax.STRING)
	if len(strTokens) < 1 {
		l.addDiagnostic(n, "include directive requires a string argument")
		return
	}
	l.addDirective(&Include{
		Span: l.spanFromNode(n),
		Path: unquoteString(strTokens[0]),
	})
}
