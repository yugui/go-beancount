// Package syntax provides a concrete syntax tree (CST) representation of
// beancount source along with the lexer and parser that build it. The CST
// preserves every byte of input — comments, whitespace, and even malformed
// regions — so the same tree can drive both formatting and downstream
// semantic analysis.
package syntax

// File is the root of the concrete syntax tree.
type File struct {
	Root   *Node   // FileNode containing all directives
	Errors []Error // parse errors encountered
}

// FullText reconstructs the complete source text from the CST.
func (f *File) FullText() string {
	if f.Root == nil {
		return ""
	}
	return f.Root.FullText()
}
