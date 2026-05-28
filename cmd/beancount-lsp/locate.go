package main

import (
	"github.com/yugui/go-beancount/pkg/syntax"
)

// Located identifies the token under the cursor and the directive it belongs to.
// When Token is non-nil, Directive is always non-nil.
type Located struct {
	Token     *syntax.Token // innermost token containing the offset
	Directive *syntax.Node  // top-level directive containing the token; nil when Token is nil
}

// LocateAt finds the token in file at the given byte offset, plus its
// containing top-level directive. The offset is inclusive on both ends:
// a token is considered to contain offset when Pos <= offset <= End.
// Returns Located{} when no token is found (e.g. whitespace, past EOF).
func LocateAt(file *syntax.File, offset int) Located {
	if file == nil || file.Root == nil {
		return Located{}
	}
	for _, child := range file.Root.Children {
		if child.Node == nil {
			continue
		}
		directive := child.Node
		tok := findToken(directive, offset)
		if tok != nil {
			return Located{Token: tok, Directive: directive}
		}
	}
	return Located{}
}

// findToken recursively searches n for the deepest token containing offset.
func findToken(n *syntax.Node, offset int) *syntax.Token {
	for _, c := range n.Children {
		if c.Token != nil {
			t := c.Token
			if t.Pos <= offset && offset <= t.End() {
				return t
			}
		} else if c.Node != nil {
			if tok := findToken(c.Node, offset); tok != nil {
				return tok
			}
		}
	}
	return nil
}
