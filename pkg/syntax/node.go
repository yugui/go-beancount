package syntax

import (
	"iter"
	"strings"
)

// Node represents a non-terminal in the concrete syntax tree.
type Node struct {
	Kind     NodeKind
	Children []Child
}

// Child is a union: exactly one of Token or Node is non-nil.
type Child struct {
	Token *Token
	Node  *Node
}

// AddToken appends a token as a child.
func (n *Node) AddToken(t *Token) {
	n.Children = append(n.Children, Child{Token: t})
}

// AddNode appends a sub-node as a child.
func (n *Node) AddNode(child *Node) {
	n.Children = append(n.Children, Child{Node: child})
}

// Tokens returns an iterator over all tokens in the subtree, in source order.
// This does a depth-first traversal.
func (n *Node) Tokens() iter.Seq[*Token] {
	return func(yield func(*Token) bool) {
		n.walkTokens(yield)
	}
}

func (n *Node) walkTokens(yield func(*Token) bool) bool {
	for _, c := range n.Children {
		if c.Token != nil {
			if !yield(c.Token) {
				return false
			}
		} else if c.Node != nil {
			if !c.Node.walkTokens(yield) {
				return false
			}
		}
	}
	return true
}

// FindToken returns the first direct child token with the given kind, or nil.
func (n *Node) FindToken(kind TokenKind) *Token {
	for _, c := range n.Children {
		if c.Token != nil && c.Token.Kind == kind {
			return c.Token
		}
	}
	return nil
}

// FindNode returns the first direct child node with the given kind, or nil.
func (n *Node) FindNode(kind NodeKind) *Node {
	for _, c := range n.Children {
		if c.Node != nil && c.Node.Kind == kind {
			return c.Node
		}
	}
	return nil
}

// FindAllNodes returns all direct child nodes with the given kind.
func (n *Node) FindAllNodes(kind NodeKind) []*Node {
	var result []*Node
	for _, c := range n.Children {
		if c.Node != nil && c.Node.Kind == kind {
			result = append(result, c.Node)
		}
	}
	return result
}

// FullText reconstructs the source text for this node's subtree
// by concatenating all trivia and raw text of all tokens.
func (n *Node) FullText() string {
	var b strings.Builder
	for t := range n.Tokens() {
		for _, tr := range t.LeadingTrivia {
			b.WriteString(tr.Raw)
		}
		b.WriteString(t.Raw)
		for _, tr := range t.TrailingTrivia {
			b.WriteString(tr.Raw)
		}
	}
	return b.String()
}
