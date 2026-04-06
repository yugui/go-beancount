package syntax

// TriviaKind represents the type of trivia (non-significant syntax).
type TriviaKind uint8

const (
	WhitespaceTrivia TriviaKind = iota // spaces and tabs
	CommentTrivia                      // ; to end of line (not including the newline)
	NewlineTrivia                      // \n or \r\n
)

var triviaKindNames = [...]string{
	WhitespaceTrivia: "WhitespaceTrivia",
	CommentTrivia:    "CommentTrivia",
	NewlineTrivia:    "NewlineTrivia",
}

// String returns the name of the trivia kind.
func (k TriviaKind) String() string {
	if int(k) < len(triviaKindNames) {
		return triviaKindNames[k]
	}
	return "UnknownTrivia"
}

// Trivia represents whitespace or comment attached to a token.
type Trivia struct {
	Kind TriviaKind
	Raw  string // exact source text
}
