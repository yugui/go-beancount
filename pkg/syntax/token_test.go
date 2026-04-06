package syntax

import "testing"

func TestTokenMethods(t *testing.T) {
	tests := []struct {
		name        string
		token       Token
		wantText    string
		wantEnd     int
		wantFullPos int
		wantFullEnd int
	}{
		{
			name: "with leading and trailing trivia",
			token: Token{
				Kind: NUMBER,
				Pos:  6,
				Raw:  "42.00",
				LeadingTrivia: []Trivia{
					{Kind: WhitespaceTrivia, Raw: "  "},
					{Kind: CommentTrivia, Raw: "; hi"},
				},
				TrailingTrivia: []Trivia{
					{Kind: WhitespaceTrivia, Raw: " "},
					{Kind: CommentTrivia, Raw: "; note"},
				},
			},
			wantText:    "42.00",
			wantEnd:     11, // 6 + 5
			wantFullPos: 0,  // 6 - (2 + 4)
			wantFullEnd: 18, // 6 + 5 + 1 + 6
		},
		{
			name: "no trivia",
			token: Token{
				Kind: IDENT,
				Pos:  5,
				Raw:  "open",
			},
			wantText:    "open",
			wantEnd:     9, // 5 + 4
			wantFullPos: 5,
			wantFullEnd: 9,
		},
		{
			name: "only leading trivia",
			token: Token{
				Kind: DATE,
				Pos:  3,
				Raw:  "2024-01-15",
				LeadingTrivia: []Trivia{
					{Kind: WhitespaceTrivia, Raw: "   "},
				},
			},
			wantText:    "2024-01-15",
			wantEnd:     13, // 3 + 10
			wantFullPos: 0,  // 3 - 3
			wantFullEnd: 13,
		},
		{
			name: "only trailing trivia",
			token: Token{
				Kind: CURRENCY,
				Pos:  0,
				Raw:  "USD",
				TrailingTrivia: []Trivia{
					{Kind: WhitespaceTrivia, Raw: "\t"},
				},
			},
			wantText:    "USD",
			wantEnd:     3, // 0 + 3
			wantFullPos: 0,
			wantFullEnd: 4, // 0 + 3 + 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.Text(); got != tt.wantText {
				t.Errorf("Text() = %q, want %q", got, tt.wantText)
			}
			if got := tt.token.End(); got != tt.wantEnd {
				t.Errorf("End() = %d, want %d", got, tt.wantEnd)
			}
			if got := tt.token.FullPos(); got != tt.wantFullPos {
				t.Errorf("FullPos() = %d, want %d", got, tt.wantFullPos)
			}
			if got := tt.token.FullEnd(); got != tt.wantFullEnd {
				t.Errorf("FullEnd() = %d, want %d", got, tt.wantFullEnd)
			}
		})
	}
}
