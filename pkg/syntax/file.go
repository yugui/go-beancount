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
