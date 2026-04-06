package syntax

// Token represents a single lexical token with attached trivia.
type Token struct {
	Kind           TokenKind // the type of token
	Pos            int       // byte offset of Raw in the source
	Raw            string    // exact source text (substring of input)
	LeadingTrivia  []Trivia  // whitespace/comments before this token
	TrailingTrivia []Trivia  // same-line whitespace/comment after, before newline
}

// Text returns the raw source text of the token.
func (t Token) Text() string {
	return t.Raw
}

// End returns the byte offset past the token's raw text, NOT including trivia.
func (t Token) End() int {
	return t.Pos + len(t.Raw)
}

// FullPos returns the byte offset including leading trivia.
// If there is leading trivia, this is the offset of the first leading trivia piece.
// Otherwise it returns the token's own Pos.
func (t Token) FullPos() int {
	if len(t.LeadingTrivia) == 0 {
		return t.Pos
	}
	total := 0
	for _, tr := range t.LeadingTrivia {
		total += len(tr.Raw)
	}
	return t.Pos - total
}

// FullEnd returns the byte offset past trailing trivia.
// If there is no trailing trivia, it returns End().
func (t Token) FullEnd() int {
	end := t.Pos + len(t.Raw)
	for _, tr := range t.TrailingTrivia {
		end += len(tr.Raw)
	}
	return end
}
