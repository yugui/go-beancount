package syntax

import "testing"

func TestIllegalIsZeroValue(t *testing.T) {
	var k TokenKind
	if k != ILLEGAL {
		t.Errorf("zero value of TokenKind = %d (%s), want ILLEGAL (0)", k, k)
	}
}

func TestTokenKindStringAll(t *testing.T) {
	for i := TokenKind(0); i < tokenKindCount; i++ {
		name := i.String()
		if name == "" {
			t.Errorf("TokenKind(%d).String() returned empty string", i)
		}
		if name == "UNKNOWN" {
			t.Errorf("TokenKind(%d).String() returned UNKNOWN; missing from tokenKindNames?", i)
		}
	}
}

func TestTokenKindStringSpecificValues(t *testing.T) {
	tests := []struct {
		kind TokenKind
		want string
	}{
		{DATE, "DATE"},
		{NUMBER, "NUMBER"},
		{STRING, "STRING"},
		{ACCOUNT, "ACCOUNT"},
		{CURRENCY, "CURRENCY"},
		{TAG, "TAG"},
		{LINK, "LINK"},
		{IDENT, "IDENT"},
		{PLUS, "PLUS"},
		{MINUS, "MINUS"},
		{STAR, "STAR"},
		{SLASH, "SLASH"},
		{LPAREN, "LPAREN"},
		{RPAREN, "RPAREN"},
		{LBRACE, "LBRACE"},
		{RBRACE, "RBRACE"},
		{LBRACE2, "LBRACE2"},
		{RBRACE2, "RBRACE2"},
		{AT, "AT"},
		{ATAT, "ATAT"},
		{TILDE, "TILDE"},
		{COMMA, "COMMA"},
		{COLON, "COLON"},
		{BANG, "BANG"},
		{HASH, "HASH"},
		{CARET, "CARET"},
		{EOF, "EOF"},
		{ILLEGAL, "ILLEGAL"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestTokenKindStringUniqueness(t *testing.T) {
	seen := make(map[string]TokenKind)
	for i := TokenKind(0); i < tokenKindCount; i++ {
		name := i.String()
		if prev, ok := seen[name]; ok {
			t.Errorf("duplicate String() %q for TokenKind %d and %d", name, prev, i)
		}
		seen[name] = i
	}
}

func TestTokenKindStringUnknown(t *testing.T) {
	unknown := TokenKind(9999)
	if got := unknown.String(); got != "UNKNOWN" {
		t.Errorf("out-of-range TokenKind.String() = %q, want UNKNOWN", got)
	}
}
