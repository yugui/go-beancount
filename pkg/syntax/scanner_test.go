package syntax

import (
	"strings"
	"testing"
)

// collectTokens scans all tokens from input until EOF (inclusive).
func collectTokens(input string) []Token {
	s := newScanner(input)
	var tokens []Token
	for {
		tok := s.scan()
		tokens = append(tokens, tok)
		if tok.Kind == EOF {
			break
		}
	}
	return tokens
}

// roundTrip reconstructs the source from tokens by concatenating all trivia and raw text.
func roundTrip(tokens []Token) string {
	var b strings.Builder
	for _, tok := range tokens {
		for _, tr := range tok.LeadingTrivia {
			b.WriteString(tr.Raw)
		}
		b.WriteString(tok.Raw)
		for _, tr := range tok.TrailingTrivia {
			b.WriteString(tr.Raw)
		}
	}
	return b.String()
}

func TestEmptyInput(t *testing.T) {
	tokens := collectTokens("")
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token (EOF), got %d", len(tokens))
	}
	if tokens[0].Kind != EOF {
		t.Errorf("expected EOF, got %s", tokens[0].Kind)
	}
	if tokens[0].Pos != 0 {
		t.Errorf("expected Pos=0, got %d", tokens[0].Pos)
	}
}

func TestSingleOperators(t *testing.T) {
	tests := []struct {
		input string
		kind  TokenKind
	}{
		{"+", PLUS},
		{"-", MINUS},
		{"*", STAR},
		{"/", SLASH},
		{"(", LPAREN},
		{")", RPAREN},
		{"{", LBRACE},
		{"}", RBRACE},
		{"@", AT},
		{"~", TILDE},
		{",", COMMA},
		{":", COLON},
		{"!", BANG},
		{"#", HASH},
		{"^", CARET},
	}
	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("expected 2 tokens (op + EOF), got %d", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != tt.kind {
				t.Errorf("expected kind %s, got %s", tt.kind, tok.Kind)
			}
			if tok.Raw != tt.input {
				t.Errorf("expected Raw=%q, got %q", tt.input, tok.Raw)
			}
			if tok.Pos != 0 {
				t.Errorf("expected Pos=0, got %d", tok.Pos)
			}
		})
	}
}

func TestMultiCharOperators(t *testing.T) {
	tests := []struct {
		input    string
		expected []TokenKind
		raws     []string
	}{
		{"{{", []TokenKind{LBRACE2, EOF}, []string{"{{", ""}},
		{"}}", []TokenKind{RBRACE2, EOF}, []string{"}}", ""}},
		{"@@", []TokenKind{ATAT, EOF}, []string{"@@", ""}},
		// Single char when not doubled
		{"{", []TokenKind{LBRACE, EOF}, []string{"{", ""}},
		{"}", []TokenKind{RBRACE, EOF}, []string{"}", ""}},
		{"@", []TokenKind{AT, EOF}, []string{"@", ""}},
		// Mixed: {{ followed by }
		{"{{}", []TokenKind{LBRACE2, RBRACE, EOF}, []string{"{{", "}", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != len(tt.expected) {
				t.Fatalf("expected %d tokens, got %d", len(tt.expected), len(tokens))
			}
			for i, tok := range tokens {
				if tok.Kind != tt.expected[i] {
					t.Errorf("token[%d]: expected kind %s, got %s", i, tt.expected[i], tok.Kind)
				}
				if tok.Raw != tt.raws[i] {
					t.Errorf("token[%d]: expected Raw=%q, got %q", i, tt.raws[i], tok.Raw)
				}
			}
		})
	}
}

func TestLeadingWhitespaceTrivia(t *testing.T) {
	tokens := collectTokens("  +")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != PLUS {
		t.Errorf("expected PLUS, got %s", tok.Kind)
	}
	if len(tok.LeadingTrivia) != 1 {
		t.Fatalf("expected 1 leading trivia, got %d", len(tok.LeadingTrivia))
	}
	if tok.LeadingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("expected WhitespaceTrivia, got %s", tok.LeadingTrivia[0].Kind)
	}
	if tok.LeadingTrivia[0].Raw != "  " {
		t.Errorf("expected Raw=%q, got %q", "  ", tok.LeadingTrivia[0].Raw)
	}
	if tok.Pos != 2 {
		t.Errorf("expected Pos=2, got %d", tok.Pos)
	}
}

func TestLeadingCommentAndNewline(t *testing.T) {
	tokens := collectTokens("; comment\n+")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != PLUS {
		t.Errorf("expected PLUS, got %s", tok.Kind)
	}
	if len(tok.LeadingTrivia) != 2 {
		t.Fatalf("expected 2 leading trivia, got %d", len(tok.LeadingTrivia))
	}
	if tok.LeadingTrivia[0].Kind != CommentTrivia {
		t.Errorf("trivia[0]: expected CommentTrivia, got %s", tok.LeadingTrivia[0].Kind)
	}
	if tok.LeadingTrivia[0].Raw != "; comment" {
		t.Errorf("trivia[0]: expected Raw=%q, got %q", "; comment", tok.LeadingTrivia[0].Raw)
	}
	if tok.LeadingTrivia[1].Kind != NewlineTrivia {
		t.Errorf("trivia[1]: expected NewlineTrivia, got %s", tok.LeadingTrivia[1].Kind)
	}
	if tok.LeadingTrivia[1].Raw != "\n" {
		t.Errorf("trivia[1]: expected Raw=%q, got %q", "\n", tok.LeadingTrivia[1].Raw)
	}
}

func TestTrailingTrivia(t *testing.T) {
	tokens := collectTokens("+  ; comment")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != PLUS {
		t.Errorf("expected PLUS, got %s", tok.Kind)
	}
	if len(tok.TrailingTrivia) != 2 {
		t.Fatalf("expected 2 trailing trivia, got %d", len(tok.TrailingTrivia))
	}
	if tok.TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("trailing[0]: expected WhitespaceTrivia, got %s", tok.TrailingTrivia[0].Kind)
	}
	if tok.TrailingTrivia[0].Raw != "  " {
		t.Errorf("trailing[0]: expected Raw=%q, got %q", "  ", tok.TrailingTrivia[0].Raw)
	}
	if tok.TrailingTrivia[1].Kind != CommentTrivia {
		t.Errorf("trailing[1]: expected CommentTrivia, got %s", tok.TrailingTrivia[1].Kind)
	}
	if tok.TrailingTrivia[1].Raw != "; comment" {
		t.Errorf("trailing[1]: expected Raw=%q, got %q", "; comment", tok.TrailingTrivia[1].Raw)
	}
}

func TestNewlineIsLeadingTriviaOfNextToken(t *testing.T) {
	tokens := collectTokens("+\n")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	// PLUS should have NO trailing trivia (newline goes to next token)
	plus := tokens[0]
	if plus.Kind != PLUS {
		t.Errorf("expected PLUS, got %s", plus.Kind)
	}
	if len(plus.TrailingTrivia) != 0 {
		t.Errorf("PLUS should have no trailing trivia, got %d", len(plus.TrailingTrivia))
	}
	// EOF should have the newline as leading trivia
	eof := tokens[1]
	if eof.Kind != EOF {
		t.Errorf("expected EOF, got %s", eof.Kind)
	}
	if len(eof.LeadingTrivia) != 1 {
		t.Fatalf("EOF should have 1 leading trivia, got %d", len(eof.LeadingTrivia))
	}
	if eof.LeadingTrivia[0].Kind != NewlineTrivia {
		t.Errorf("expected NewlineTrivia, got %s", eof.LeadingTrivia[0].Kind)
	}
}

func TestMultipleTokens(t *testing.T) {
	tokens := collectTokens("+ - *")
	if len(tokens) != 4 { // PLUS, MINUS, STAR, EOF
		t.Fatalf("expected 4 tokens, got %d", len(tokens))
	}

	// PLUS with trailing whitespace
	if tokens[0].Kind != PLUS {
		t.Errorf("token[0]: expected PLUS, got %s", tokens[0].Kind)
	}
	if len(tokens[0].TrailingTrivia) != 1 || tokens[0].TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("token[0]: expected trailing whitespace trivia")
	}

	// MINUS with trailing whitespace
	if tokens[1].Kind != MINUS {
		t.Errorf("token[1]: expected MINUS, got %s", tokens[1].Kind)
	}
	if len(tokens[1].TrailingTrivia) != 1 || tokens[1].TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("token[1]: expected trailing whitespace trivia")
	}

	// STAR with no trivia
	if tokens[2].Kind != STAR {
		t.Errorf("token[2]: expected STAR, got %s", tokens[2].Kind)
	}
	if len(tokens[2].LeadingTrivia) != 0 {
		t.Errorf("token[2]: expected no leading trivia, got %d", len(tokens[2].LeadingTrivia))
	}
	if len(tokens[2].TrailingTrivia) != 0 {
		t.Errorf("token[2]: expected no trailing trivia, got %d", len(tokens[2].TrailingTrivia))
	}
}

func TestRoundTrip(t *testing.T) {
	inputs := []string{
		"",
		"+",
		"  +",
		"+  ; comment",
		"+\n",
		"; comment\n+",
		"+ - *",
		"{{ }} @@",
		"  ; leading comment\n+ - ; trailing\n* /\n",
		"# ^ ! ~ , : ( ) { } @ + - * /",
		"+\r\n-",
		";\n+\n;\n",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			tokens := collectTokens(input)
			got := roundTrip(tokens)
			if got != input {
				t.Errorf("round-trip mismatch:\n  input: %q\n  got:   %q", input, got)
			}
		})
	}
}

func TestBareHashAndCaret(t *testing.T) {
	tokens := collectTokens("#")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Kind != HASH {
		t.Errorf("expected HASH, got %s", tokens[0].Kind)
	}
	if tokens[0].Raw != "#" {
		t.Errorf("expected Raw=%q, got %q", "#", tokens[0].Raw)
	}

	tokens = collectTokens("^")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Kind != CARET {
		t.Errorf("expected CARET, got %s", tokens[0].Kind)
	}
	if tokens[0].Raw != "^" {
		t.Errorf("expected Raw=%q, got %q", "^", tokens[0].Raw)
	}
}

func TestIllegalCharacter(t *testing.T) {
	tokens := collectTokens("a")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Kind != ILLEGAL {
		t.Errorf("expected ILLEGAL, got %s", tokens[0].Kind)
	}
	if tokens[0].Raw != "a" {
		t.Errorf("expected Raw=%q, got %q", "a", tokens[0].Raw)
	}
}

func TestCRLFNewline(t *testing.T) {
	tokens := collectTokens("+\r\n-")
	if len(tokens) != 3 { // PLUS, MINUS, EOF
		t.Fatalf("expected 3 tokens, got %d", len(tokens))
	}
	// PLUS has no trailing trivia
	if len(tokens[0].TrailingTrivia) != 0 {
		t.Errorf("PLUS should have no trailing trivia")
	}
	// MINUS has \r\n as leading trivia (single newline trivia)
	if len(tokens[1].LeadingTrivia) != 1 {
		t.Fatalf("MINUS should have 1 leading trivia, got %d", len(tokens[1].LeadingTrivia))
	}
	if tokens[1].LeadingTrivia[0].Kind != NewlineTrivia {
		t.Errorf("expected NewlineTrivia, got %s", tokens[1].LeadingTrivia[0].Kind)
	}
	if tokens[1].LeadingTrivia[0].Raw != "\r\n" {
		t.Errorf("expected Raw=%q, got %q", "\r\n", tokens[1].LeadingTrivia[0].Raw)
	}
}

func TestTrailingWhitespaceBeforeNewline(t *testing.T) {
	// "+ \n-" — PLUS gets trailing whitespace, newline goes to MINUS as leading
	tokens := collectTokens("+ \n-")
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(tokens))
	}
	if len(tokens[0].TrailingTrivia) != 1 {
		t.Fatalf("PLUS should have 1 trailing trivia, got %d", len(tokens[0].TrailingTrivia))
	}
	if tokens[0].TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("expected WhitespaceTrivia, got %s", tokens[0].TrailingTrivia[0].Kind)
	}
	if len(tokens[1].LeadingTrivia) != 1 {
		t.Fatalf("MINUS should have 1 leading trivia, got %d", len(tokens[1].LeadingTrivia))
	}
	if tokens[1].LeadingTrivia[0].Kind != NewlineTrivia {
		t.Errorf("expected NewlineTrivia, got %s", tokens[1].LeadingTrivia[0].Kind)
	}
}

func TestDateTokens(t *testing.T) {
	tests := []struct {
		input string
		raw   string
	}{
		{"2024-01-15", "2024-01-15"},
		{"2024/01/15", "2024/01/15"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("expected 2 tokens, got %d", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != DATE {
				t.Errorf("expected DATE, got %s", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("expected Raw=%q, got %q", tt.raw, tok.Raw)
			}
		})
	}
}

func TestNumberTokens(t *testing.T) {
	tests := []struct {
		input string
		raw   string
	}{
		{"1234", "1234"},
		{"1234.56", "1234.56"},
		{"1,234.56", "1,234.56"},
		{".56", ".56"},
		{"1234.", "1234."},
		{"0", "0"},
		{"1,234,567.89", "1,234,567.89"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("expected 2 tokens, got %d", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != NUMBER {
				t.Errorf("expected NUMBER, got %s", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("expected Raw=%q, got %q", tt.raw, tok.Raw)
			}
		})
	}
}

func TestStringTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		raw   string
	}{
		{"simple", `"hello"`, `"hello"`},
		{"multiline", "\"multi\nline\"", "\"multi\nline\""},
		{"escaped_quote", `"escaped \"quote\""`, `"escaped \"quote\""`},
		{"empty_string", `""`, `""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("expected 2 tokens, got %d", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != STRING {
				t.Errorf("expected STRING, got %s", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("expected Raw=%q, got %q", tt.raw, tok.Raw)
			}
		})
	}
}

func TestDateVsNumberDisambiguation(t *testing.T) {
	// "2024" alone is a NUMBER (no separator follows)
	tokens := collectTokens("2024")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Kind != NUMBER {
		t.Errorf("expected NUMBER for '2024', got %s", tokens[0].Kind)
	}
	if tokens[0].Raw != "2024" {
		t.Errorf("expected Raw=%q, got %q", "2024", tokens[0].Raw)
	}

	// "2024-01-15 1234" → DATE then NUMBER
	tokens = collectTokens("2024-01-15 1234")
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(tokens))
	}
	if tokens[0].Kind != DATE {
		t.Errorf("expected DATE, got %s", tokens[0].Kind)
	}
	if tokens[0].Raw != "2024-01-15" {
		t.Errorf("expected Raw=%q, got %q", "2024-01-15", tokens[0].Raw)
	}
	if tokens[1].Kind != NUMBER {
		t.Errorf("expected NUMBER, got %s", tokens[1].Kind)
	}
	if tokens[1].Raw != "1234" {
		t.Errorf("expected Raw=%q, got %q", "1234", tokens[1].Raw)
	}
}

func TestStringAtEOF(t *testing.T) {
	tokens := collectTokens(`"unclosed`)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != STRING {
		t.Errorf("expected STRING, got %s", tok.Kind)
	}
	if tok.Raw != `"unclosed` {
		t.Errorf("expected Raw=%q, got %q", `"unclosed`, tok.Raw)
	}
}

func TestRoundTripDatesNumbersStrings(t *testing.T) {
	inputs := []string{
		"2024-01-15",
		"2024/01/15",
		"1234",
		"1234.56",
		"1,234.56",
		".56",
		"1234.",
		`"hello"`,
		`""`,
		"\"multi\nline\"",
		`"escaped \"quote\""`,
		`"unclosed`,
		"2024-01-15 1,234.56 \"hello\"",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			tokens := collectTokens(input)
			got := roundTrip(tokens)
			if got != input {
				t.Errorf("round-trip mismatch:\n  input: %q\n  got:   %q", input, got)
			}
		})
	}
}

func TestMixedDateNumberString(t *testing.T) {
	tokens := collectTokens("2024-01-15 1,234.56 \"hello\"")
	if len(tokens) != 4 { // DATE, NUMBER, STRING, EOF
		t.Fatalf("expected 4 tokens, got %d", len(tokens))
	}

	if tokens[0].Kind != DATE {
		t.Errorf("token[0]: expected DATE, got %s", tokens[0].Kind)
	}
	if tokens[0].Raw != "2024-01-15" {
		t.Errorf("token[0]: expected Raw=%q, got %q", "2024-01-15", tokens[0].Raw)
	}

	if tokens[1].Kind != NUMBER {
		t.Errorf("token[1]: expected NUMBER, got %s", tokens[1].Kind)
	}
	if tokens[1].Raw != "1,234.56" {
		t.Errorf("token[1]: expected Raw=%q, got %q", "1,234.56", tokens[1].Raw)
	}

	if tokens[2].Kind != STRING {
		t.Errorf("token[2]: expected STRING, got %s", tokens[2].Kind)
	}
	if tokens[2].Raw != "\"hello\"" {
		t.Errorf("token[2]: expected Raw=%q, got %q", "\"hello\"", tokens[2].Raw)
	}
}
