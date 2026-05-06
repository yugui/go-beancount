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
		t.Fatalf("scan: got %d tokens, want 1", len(tokens))
	}
	if tokens[0].Kind != EOF {
		t.Errorf("scan: Kind = %s, want EOF", tokens[0].Kind)
	}
	if tokens[0].Pos != 0 {
		t.Errorf("scan: Pos = %d, want 0", tokens[0].Pos)
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
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != tt.kind {
				t.Errorf("scan: Kind = %s, want %s", tok.Kind, tt.kind)
			}
			if tok.Raw != tt.input {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.input)
			}
			if tok.Pos != 0 {
				t.Errorf("scan: Pos = %d, want 0", tok.Pos)
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
				t.Fatalf("scan: got %d tokens, want %d", len(tokens), len(tt.expected))
			}
			for i, tok := range tokens {
				if tok.Kind != tt.expected[i] {
					t.Errorf("scan: token[%d] Kind = %s, want %s", i, tok.Kind, tt.expected[i])
				}
				if tok.Raw != tt.raws[i] {
					t.Errorf("scan: token[%d] Raw = %q, want %q", i, tok.Raw, tt.raws[i])
				}
			}
		})
	}
}

func TestLeadingWhitespaceTrivia(t *testing.T) {
	tokens := collectTokens("  +")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != PLUS {
		t.Errorf("scan: Kind = %s, want PLUS", tok.Kind)
	}
	if len(tok.LeadingTrivia) != 1 {
		t.Fatalf("scan: got %d leading trivia, want 1", len(tok.LeadingTrivia))
	}
	if tok.LeadingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("scan: trivia Kind = %s, want WhitespaceTrivia", tok.LeadingTrivia[0].Kind)
	}
	if tok.LeadingTrivia[0].Raw != "  " {
		t.Errorf("scan: Raw = %q, want %q", tok.LeadingTrivia[0].Raw, "  ")
	}
	if tok.Pos != 2 {
		t.Errorf("scan: Pos = %d, want 2", tok.Pos)
	}
}

func TestLeadingCommentAndNewline(t *testing.T) {
	tokens := collectTokens("; comment\n+")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != PLUS {
		t.Errorf("scan: Kind = %s, want PLUS", tok.Kind)
	}
	if len(tok.LeadingTrivia) != 2 {
		t.Fatalf("scan: got %d leading trivia, want 2", len(tok.LeadingTrivia))
	}
	if tok.LeadingTrivia[0].Kind != CommentTrivia {
		t.Errorf("scan: trivia[0] Kind = %s, want CommentTrivia", tok.LeadingTrivia[0].Kind)
	}
	if tok.LeadingTrivia[0].Raw != "; comment" {
		t.Errorf("scan: trivia[0] Raw = %q, want %q", tok.LeadingTrivia[0].Raw, "; comment")
	}
	if tok.LeadingTrivia[1].Kind != NewlineTrivia {
		t.Errorf("scan: trivia[1] Kind = %s, want NewlineTrivia", tok.LeadingTrivia[1].Kind)
	}
	if tok.LeadingTrivia[1].Raw != "\n" {
		t.Errorf("scan: trivia[1] Raw = %q, want %q", tok.LeadingTrivia[1].Raw, "\n")
	}
}

func TestTrailingTrivia(t *testing.T) {
	tokens := collectTokens("+  ; comment")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != PLUS {
		t.Errorf("scan: Kind = %s, want PLUS", tok.Kind)
	}
	if len(tok.TrailingTrivia) != 2 {
		t.Fatalf("scan: got %d trailing trivia, want 2", len(tok.TrailingTrivia))
	}
	if tok.TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("scan: trailing[0] Kind = %s, want WhitespaceTrivia", tok.TrailingTrivia[0].Kind)
	}
	if tok.TrailingTrivia[0].Raw != "  " {
		t.Errorf("scan: trailing[0] Raw = %q, want %q", tok.TrailingTrivia[0].Raw, "  ")
	}
	if tok.TrailingTrivia[1].Kind != CommentTrivia {
		t.Errorf("scan: trailing[1] Kind = %s, want CommentTrivia", tok.TrailingTrivia[1].Kind)
	}
	if tok.TrailingTrivia[1].Raw != "; comment" {
		t.Errorf("scan: trailing[1] Raw = %q, want %q", tok.TrailingTrivia[1].Raw, "; comment")
	}
}

func TestNewlineIsLeadingTriviaOfNextToken(t *testing.T) {
	tokens := collectTokens("+\n")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	// PLUS should have NO trailing trivia (newline goes to next token)
	plus := tokens[0]
	if plus.Kind != PLUS {
		t.Errorf("scan: Kind = %s, want PLUS", plus.Kind)
	}
	if len(plus.TrailingTrivia) != 0 {
		t.Errorf("scan: PLUS trailing trivia count = %d, want 0", len(plus.TrailingTrivia))
	}
	// EOF should have the newline as leading trivia
	eof := tokens[1]
	if eof.Kind != EOF {
		t.Errorf("scan: Kind = %s, want EOF", eof.Kind)
	}
	if len(eof.LeadingTrivia) != 1 {
		t.Fatalf("scan: got %d leading trivia, want 1", len(eof.LeadingTrivia))
	}
	if eof.LeadingTrivia[0].Kind != NewlineTrivia {
		t.Errorf("scan: trivia Kind = %s, want NewlineTrivia", eof.LeadingTrivia[0].Kind)
	}
}

func TestMultipleTokens(t *testing.T) {
	tokens := collectTokens("+ - *")
	if len(tokens) != 4 { // PLUS, MINUS, STAR, EOF
		t.Fatalf("scan: got %d tokens, want 4", len(tokens))
	}

	// PLUS with trailing whitespace
	if tokens[0].Kind != PLUS {
		t.Errorf("scan: token[0] Kind = %s, want PLUS", tokens[0].Kind)
	}
	if len(tokens[0].TrailingTrivia) != 1 || tokens[0].TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("scan: token[0] TrailingTrivia = %v, want [WhitespaceTrivia]", tokens[0].TrailingTrivia)
	}

	// MINUS with trailing whitespace
	if tokens[1].Kind != MINUS {
		t.Errorf("scan: token[1] Kind = %s, want MINUS", tokens[1].Kind)
	}
	if len(tokens[1].TrailingTrivia) != 1 || tokens[1].TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("scan: token[1] TrailingTrivia = %v, want [WhitespaceTrivia]", tokens[1].TrailingTrivia)
	}

	// STAR with no trivia
	if tokens[2].Kind != STAR {
		t.Errorf("scan: token[2] Kind = %s, want STAR", tokens[2].Kind)
	}
	if len(tokens[2].LeadingTrivia) != 0 {
		t.Errorf("scan: leading trivia count = %d, want 0", len(tokens[2].LeadingTrivia))
	}
	if len(tokens[2].TrailingTrivia) != 0 {
		t.Errorf("scan: trailing trivia count = %d, want 0", len(tokens[2].TrailingTrivia))
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
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != HASH {
		t.Errorf("scan: Kind = %s, want HASH", tokens[0].Kind)
	}
	if tokens[0].Raw != "#" {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, "#")
	}

	tokens = collectTokens("^")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != CARET {
		t.Errorf("scan: Kind = %s, want CARET", tokens[0].Kind)
	}
	if tokens[0].Raw != "^" {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, "^")
	}
}

func TestIllegalCharacter(t *testing.T) {
	tokens := collectTokens("$")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != ILLEGAL {
		t.Errorf("scan: Kind = %s, want ILLEGAL", tokens[0].Kind)
	}
	if tokens[0].Raw != "$" {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, "$")
	}
}

// TestIllegalMultiByteCharacter pins the contract that an unrecognised
// non-ASCII character emits a single ILLEGAL token whose Raw covers the
// full UTF-8 rune, not one ILLEGAL per byte.
func TestIllegalMultiByteCharacter(t *testing.T) {
	// '※' (U+203B REFERENCE MARK) is in category Po and is not part of
	// any account/currency/identifier alphabet.
	const ref = "※"
	tokens := collectTokens(ref)
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != ILLEGAL {
		t.Errorf("scan: Kind = %s, want ILLEGAL", tokens[0].Kind)
	}
	if tokens[0].Raw != ref {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, ref)
	}
}

func TestCRLFNewline(t *testing.T) {
	tokens := collectTokens("+\r\n-")
	if len(tokens) != 3 { // PLUS, MINUS, EOF
		t.Fatalf("scan: got %d tokens, want 3", len(tokens))
	}
	// PLUS has no trailing trivia
	if len(tokens[0].TrailingTrivia) != 0 {
		t.Errorf("scan: PLUS trailing trivia count = %d, want 0", len(tokens[0].TrailingTrivia))
	}
	// MINUS has \r\n as leading trivia (single newline trivia)
	if len(tokens[1].LeadingTrivia) != 1 {
		t.Fatalf("scan: got %d leading trivia, want 1", len(tokens[1].LeadingTrivia))
	}
	if tokens[1].LeadingTrivia[0].Kind != NewlineTrivia {
		t.Errorf("scan: trivia Kind = %s, want NewlineTrivia", tokens[1].LeadingTrivia[0].Kind)
	}
	if tokens[1].LeadingTrivia[0].Raw != "\r\n" {
		t.Errorf("scan: Raw = %q, want %q", tokens[1].LeadingTrivia[0].Raw, "\r\n")
	}
}

func TestTrailingWhitespaceBeforeNewline(t *testing.T) {
	// "+ \n-" — PLUS gets trailing whitespace, newline goes to MINUS as leading
	tokens := collectTokens("+ \n-")
	if len(tokens) != 3 {
		t.Fatalf("scan: got %d tokens, want 3", len(tokens))
	}
	if len(tokens[0].TrailingTrivia) != 1 {
		t.Fatalf("scan: got %d trailing trivia, want 1", len(tokens[0].TrailingTrivia))
	}
	if tokens[0].TrailingTrivia[0].Kind != WhitespaceTrivia {
		t.Errorf("scan: trivia Kind = %s, want WhitespaceTrivia", tokens[0].TrailingTrivia[0].Kind)
	}
	if len(tokens[1].LeadingTrivia) != 1 {
		t.Fatalf("scan: got %d leading trivia, want 1", len(tokens[1].LeadingTrivia))
	}
	if tokens[1].LeadingTrivia[0].Kind != NewlineTrivia {
		t.Errorf("scan: trivia Kind = %s, want NewlineTrivia", tokens[1].LeadingTrivia[0].Kind)
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
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != DATE {
				t.Errorf("scan: Kind = %s, want DATE", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
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
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != NUMBER {
				t.Errorf("scan: Kind = %s, want NUMBER", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
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
		// Escape sequences (literal backslash in source, not Go escapes)
		{"escape_n", `"line\nbreak"`, `"line\nbreak"`},
		{"escape_t", `"col\tcol"`, `"col\tcol"`},
		{"escape_r", `"before\rafter"`, `"before\rafter"`},
		{"escape_f", `"page\fbreak"`, `"page\fbreak"`},
		{"escape_b", `"back\bspace"`, `"back\bspace"`},
		{"escape_backslash", `"path\\to"`, `"path\\to"`},
		{"escape_backslash_quote", `"end\\\"more"`, `"end\\\"more"`},
		{"escape_unrecognized", `"test\xval"`, `"test\xval"`},
		// Literal special characters (actual characters, not escapes)
		{"literal_tab", "\"before\tafter\"", "\"before\tafter\""},
		{"literal_cr", "\"before\rafter\"", "\"before\rafter\""},
		// Unicode
		{"accented", `"café résumé"`, `"café résumé"`},
		{"combining", "\"e\u0301\"", "\"e\u0301\""},
		{"cjk", `"日本語"`, `"日本語"`},
		{"emoji", `"🎉 party"`, `"🎉 party"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != STRING {
				t.Errorf("scan: Kind = %s, want STRING", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
			}
		})
	}
}

func TestDateVsNumberDisambiguation(t *testing.T) {
	// "2024" alone is a NUMBER (no separator follows)
	tokens := collectTokens("2024")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != NUMBER {
		t.Errorf("scan: Kind = %s, want NUMBER", tokens[0].Kind)
	}
	if tokens[0].Raw != "2024" {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, "2024")
	}

	// "2024-01-15 1234" → DATE then NUMBER
	tokens = collectTokens("2024-01-15 1234")
	if len(tokens) != 3 {
		t.Fatalf("scan: got %d tokens, want 3", len(tokens))
	}
	if tokens[0].Kind != DATE {
		t.Errorf("scan: Kind = %s, want DATE", tokens[0].Kind)
	}
	if tokens[0].Raw != "2024-01-15" {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, "2024-01-15")
	}
	if tokens[1].Kind != NUMBER {
		t.Errorf("scan: Kind = %s, want NUMBER", tokens[1].Kind)
	}
	if tokens[1].Raw != "1234" {
		t.Errorf("scan: Raw = %q, want %q", tokens[1].Raw, "1234")
	}
}

func TestStringAtEOF(t *testing.T) {
	tokens := collectTokens(`"unclosed`)
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != STRING {
		t.Errorf("scan: Kind = %s, want STRING", tok.Kind)
	}
	if tok.Raw != `"unclosed` {
		t.Errorf("scan: Raw = %q, want %q", tok.Raw, `"unclosed`)
	}
}

func TestStringBackslashAtEOF(t *testing.T) {
	tokens := collectTokens(`"test\`)
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	tok := tokens[0]
	if tok.Kind != STRING {
		t.Errorf("scan: Kind = %s, want STRING", tok.Kind)
	}
	if tok.Raw != `"test\` {
		t.Errorf("scan: Raw = %q, want %q", tok.Raw, `"test\`)
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
		`"line\nbreak"`,
		`"col\tcol"`,
		`"path\\to"`,
		`"end\\\"more"`,
		`"café résumé"`,
		`"日本語"`,
		`"🎉 party"`,
		"\"before\tafter\"",
		"\"e\u0301\"",
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
		t.Fatalf("scan: got %d tokens, want 4", len(tokens))
	}

	if tokens[0].Kind != DATE {
		t.Errorf("scan: token[0] Kind = %s, want DATE", tokens[0].Kind)
	}
	if tokens[0].Raw != "2024-01-15" {
		t.Errorf("scan: token[0] Raw = %q, want %q", tokens[0].Raw, "2024-01-15")
	}

	if tokens[1].Kind != NUMBER {
		t.Errorf("scan: token[1] Kind = %s, want NUMBER", tokens[1].Kind)
	}
	if tokens[1].Raw != "1,234.56" {
		t.Errorf("scan: token[1] Raw = %q, want %q", tokens[1].Raw, "1,234.56")
	}

	if tokens[2].Kind != STRING {
		t.Errorf("scan: token[2] Kind = %s, want STRING", tokens[2].Kind)
	}
	if tokens[2].Raw != "\"hello\"" {
		t.Errorf("scan: token[2] Raw = %q, want %q", tokens[2].Raw, "\"hello\"")
	}
}

func TestAccountTokens(t *testing.T) {
	tests := []struct {
		input string
		raw   string
	}{
		{"Assets:Bank:Checking", "Assets:Bank:Checking"},
		{"Expenses:Food", "Expenses:Food"},
		{"Liabilities:CreditCard", "Liabilities:CreditCard"},
		{"Income:Salary", "Income:Salary"},
		{"Equity:Opening-Balances", "Equity:Opening-Balances"},
		{"Assets:US:BofA:Checking", "Assets:US:BofA:Checking"},
		{"Income:Salary:Base", "Income:Salary:Base"},
		{"Assets:Bank:1stAccount", "Assets:Bank:1stAccount"},
		// Account components composed of CJK letters.
		{"Expenses:食費", "Expenses:食費"},
		// U+30FB KATAKANA MIDDLE DOT is in Other_ID_Continue and is the
		// conventional separator inside Japanese compound names; it must
		// be recognised mid-component rather than terminating the account.
		{"Expenses:Communication:宅配便・運送", "Expenses:Communication:宅配便・運送"},
		// U+00B7 MIDDLE DOT (also Other_ID_Continue).
		{"Expenses:Cat·alan", "Expenses:Cat·alan"},
		// scanUpperWord runs the account branch before isCurrency, so the
		// removal of the 24-char cap on isCurrency must not perturb account
		// recognition for components longer than 24 characters.
		{"Assets:FakeAccountLongComponentName", "Assets:FakeAccountLongComponentName"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != ACCOUNT {
				t.Errorf("scan: Kind = %s, want ACCOUNT", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
			}
		})
	}
}

func TestCurrencyTokens(t *testing.T) {
	tests := []struct {
		input string
		raw   string
	}{
		{"USD", "USD"},
		{"HOOL", "HOOL"},
		{"VWCE.DE", "VWCE.DE"},
		{"NT.TO", "NT.TO"},
		{"IVV", "IVV"},
		{"A", "A"},
		{"GLD", "GLD"},
		// Length cap removed: 25-char currency must lex as CURRENCY, not IDENT.
		{"LONG_CURRENCY_NAME_EXAM25", "LONG_CURRENCY_NAME_EXAM25"},
		// Much longer fictional ticker to demonstrate no upper bound.
		{"EXTRA_LONG_TICKER_FOR_TEST_50CHARS_AAAAAAAAAAAAAAAA", "EXTRA_LONG_TICKER_FOR_TEST_50CHARS_AAAAAAAAAAAAAAAA"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != CURRENCY {
				t.Errorf("scan: Kind = %s, want CURRENCY", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
			}
		})
	}
}

func TestIdentTokens(t *testing.T) {
	tests := []struct {
		input string
		raw   string
	}{
		{"open", "open"},
		{"close", "close"},
		{"txn", "txn"},
		{"some-key", "some-key"},
		{"option", "option"},
		{"pushtag", "pushtag"},
		{"filename", "filename"},
		// Length cap on isCurrency was removed; the only remaining gate at
		// length > 24 is the boundary rule that the first and last char must
		// be in [A-Z0-9]. scanUpperWord is reached only when the first byte
		// is uppercase, so the realisable boundary failure at the upper-word
		// entry point is a trailing inner-only char ('_', '.', '-', '\'').
		// One case per inner-only char, each length 25+.
		{"LONG_FAKE_TICKER_NAME_25_", "LONG_FAKE_TICKER_NAME_25_"},
		{"LONG_FAKE_TICKER_NAME_25.", "LONG_FAKE_TICKER_NAME_25."},
		{"LONG_FAKE_TICKER_NAME_25-", "LONG_FAKE_TICKER_NAME_25-"},
		{"LONG_FAKE_TICKER_NAME_25'", "LONG_FAKE_TICKER_NAME_25'"},
		// At length > 24 the all-uppercase check is the sole discriminator
		// between CURRENCY and IDENT, so a mixed-case 25+ char word must
		// still lex as IDENT.
		{"LongFakeTickerNameMixed25", "LongFakeTickerNameMixed25"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			if len(tokens) != 2 {
				t.Fatalf("scan: got %d tokens, want 2", len(tokens))
			}
			tok := tokens[0]
			if tok.Kind != IDENT {
				t.Errorf("scan: Kind = %s, want IDENT", tok.Kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
			}
		})
	}
}

func TestUppercaseNonCurrencyAsIdent(t *testing.T) {
	// "Assets" alone (titlecase, not all uppercase) without ':' -> IDENT
	tokens := collectTokens("Assets")
	if len(tokens) != 2 {
		t.Fatalf("scan: got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != IDENT {
		t.Errorf("scan: Kind = %s, want IDENT", tokens[0].Kind)
	}
	if tokens[0].Raw != "Assets" {
		t.Errorf("scan: Raw = %q, want %q", tokens[0].Raw, "Assets")
	}
}

func TestTagTokens(t *testing.T) {
	tests := []struct {
		input string
		kind  TokenKind
		raw   string
	}{
		{"#trip", TAG, "#trip"},
		{"#tax-2024", TAG, "#tax-2024"},
		{"#some_tag", TAG, "#some_tag"},
		{"#", HASH, "#"},
		{"# ", HASH, "#"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			tok := tokens[0]
			if tok.Kind != tt.kind {
				t.Errorf("scan: Kind = %s, want %s", tok.Kind, tt.kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
			}
		})
	}
}

func TestLinkTokens(t *testing.T) {
	tests := []struct {
		input string
		kind  TokenKind
		raw   string
	}{
		{"^invoice-123", LINK, "^invoice-123"},
		{"^ref", LINK, "^ref"},
		{"^ref001", LINK, "^ref001"},
		{"^", CARET, "^"},
		{"^ ", CARET, "^"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			tok := tokens[0]
			if tok.Kind != tt.kind {
				t.Errorf("scan: Kind = %s, want %s", tok.Kind, tt.kind)
			}
			if tok.Raw != tt.raw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.raw)
			}
		})
	}
}

func TestDisambiguation(t *testing.T) {
	// "Assets:Bank 100 USD" → ACCOUNT, NUMBER, CURRENCY
	tokens := collectTokens("Assets:Bank 100 USD")
	if len(tokens) != 4 {
		t.Fatalf("scan: got %d tokens, want 4", len(tokens))
	}
	if tokens[0].Kind != ACCOUNT {
		t.Errorf("scan: token[0] Kind = %s, want ACCOUNT", tokens[0].Kind)
	}
	if tokens[0].Raw != "Assets:Bank" {
		t.Errorf("scan: token[0] Raw = %q, want %q", tokens[0].Raw, "Assets:Bank")
	}
	if tokens[1].Kind != NUMBER {
		t.Errorf("scan: token[1] Kind = %s, want NUMBER", tokens[1].Kind)
	}
	if tokens[2].Kind != CURRENCY {
		t.Errorf("scan: token[2] Kind = %s, want CURRENCY", tokens[2].Kind)
	}
}

func TestFullLineDisambiguation(t *testing.T) {
	// "2024-01-15 open Assets:Bank USD" → DATE, IDENT, ACCOUNT, CURRENCY
	tokens := collectTokens("2024-01-15 open Assets:Bank USD")
	if len(tokens) != 5 {
		t.Fatalf("scan: got %d tokens, want 5", len(tokens))
	}
	if tokens[0].Kind != DATE {
		t.Errorf("scan: token[0] Kind = %s, want DATE", tokens[0].Kind)
	}
	if tokens[1].Kind != IDENT {
		t.Errorf("scan: token[1] Kind = %s, want IDENT", tokens[1].Kind)
	}
	if tokens[1].Raw != "open" {
		t.Errorf("scan: token[1] Raw = %q, want %q", tokens[1].Raw, "open")
	}
	if tokens[2].Kind != ACCOUNT {
		t.Errorf("scan: token[2] Kind = %s, want ACCOUNT", tokens[2].Kind)
	}
	if tokens[2].Raw != "Assets:Bank" {
		t.Errorf("scan: token[2] Raw = %q, want %q", tokens[2].Raw, "Assets:Bank")
	}
	if tokens[3].Kind != CURRENCY {
		t.Errorf("scan: token[3] Kind = %s, want CURRENCY", tokens[3].Kind)
	}
	if tokens[3].Raw != "USD" {
		t.Errorf("scan: token[3] Raw = %q, want %q", tokens[3].Raw, "USD")
	}
}

func TestRoundTripAccountsCurrenciesIdents(t *testing.T) {
	inputs := []string{
		"Assets:Bank:Checking",
		"Expenses:Food",
		"USD",
		"VWCE.DE",
		"open",
		"close",
		"#trip-2024",
		"^invoice-123",
		"Assets",
		"2024-01-15 open Assets:Bank USD",
		"Assets:Bank 100 USD",
		"#trip ^ref",
		"# ^",
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

func TestUnicodeAccountTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKind TokenKind
		wantRaw  string
	}{
		{"CJK_kanji", "Assets:食費", ACCOUNT, "Assets:食費"},
		{"katakana_and_kanji", "Expenses:カフェ代", ACCOUNT, "Expenses:カフェ代"},
		{"cyrillic_uppercase", "Assets:Банк", ACCOUNT, "Assets:Банк"},
		{"roman_numeral_start", "Income:Ⅱ期", ACCOUNT, "Income:Ⅱ期"},
		{"multi_component_unicode", "Expenses:食費:ランチ", ACCOUNT, "Expenses:食費:ランチ"},
		{"lowercase_start_not_account", "Assets:café", IDENT, "Assets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			tok := tokens[0]
			if tok.Kind != tt.wantKind {
				t.Errorf("scan: Kind = %s, want %s (raw=%q)", tok.Kind, tt.wantKind, tok.Raw)
			}
			if tok.Raw != tt.wantRaw {
				t.Errorf("scan: Raw = %q, want %q", tok.Raw, tt.wantRaw)
			}
		})
	}
}

func TestRoundTripUnicodeAccounts(t *testing.T) {
	inputs := []string{
		"Assets:食費",
		"Expenses:カフェ代",
		"Assets:Банк",
		"Income:Ⅱ期",
		"Expenses:食費:ランチ",
		"Assets:café",
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

func TestHeadingTrivia(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // expected HeadingTrivia Raw
	}{
		{"single star with text", "* Heading\n", "* Heading"},
		{"double star", "** Deeper\n", "** Deeper"},
		{"triple star", "*** Even deeper\n", "*** Even deeper"},
		{"star at EOF", "* Heading", "* Heading"},
		{"star newline", "*\n", "*"},
		{"star tab text", "*\tHeading\n", "*\tHeading"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := collectTokens(tt.input)
			// Heading should be leading trivia on the EOF token
			eof := tokens[len(tokens)-1]
			if eof.Kind != EOF {
				t.Fatalf("last token = %v, want EOF", eof.Kind)
			}
			found := false
			for _, tr := range eof.LeadingTrivia {
				if tr.Kind == HeadingTrivia {
					if tr.Raw != tt.want {
						t.Errorf("HeadingTrivia.Raw = %q, want %q", tr.Raw, tt.want)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("no HeadingTrivia found in EOF leading trivia: %v", eof.LeadingTrivia)
			}
			// Round-trip
			got := roundTrip(tokens)
			if got != tt.input {
				t.Errorf("round-trip mismatch:\n  input: %q\n  got:   %q", tt.input, got)
			}
		})
	}
}

func TestHeadingTriviaBeforeDirective(t *testing.T) {
	input := "* Section\n2024-01-01 open Assets:A\n"
	tokens := collectTokens(input)
	// First real token should be DATE with heading in its leading trivia
	if tokens[0].Kind != DATE {
		t.Fatalf("first token = %v, want DATE", tokens[0].Kind)
	}
	found := false
	for _, tr := range tokens[0].LeadingTrivia {
		if tr.Kind == HeadingTrivia && tr.Raw == "* Section" {
			found = true
		}
	}
	if !found {
		t.Errorf("scan: DATE leading trivia = %v, want HeadingTrivia", tokens[0].LeadingTrivia)
	}
	got := roundTrip(tokens)
	if got != input {
		t.Errorf("round-trip mismatch:\n  input: %q\n  got:   %q", input, got)
	}
}

func TestStarNotHeadingWhenIndented(t *testing.T) {
	// Indented * should be a STAR token, not heading trivia
	input := "  * Assets:A\n"
	tokens := collectTokens(input)
	found := false
	for _, tok := range tokens {
		if tok.Kind == STAR {
			found = true
		}
	}
	if !found {
		t.Errorf("scan: got no STAR token for indented *, want STAR")
	}
}

func TestStarNotHeadingWithoutSpace(t *testing.T) {
	// *USD at line start should not be heading trivia
	input := "*USD\n"
	tokens := collectTokens(input)
	if tokens[0].Kind != STAR {
		t.Errorf("first token = %v, want STAR", tokens[0].Kind)
	}
}
