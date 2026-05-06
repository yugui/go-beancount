package syntax

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScannerRoundTrip(t *testing.T) {
	// Find all .beancount files in testdata/
	files, err := filepath.Glob("testdata/*.beancount")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no testdata files found")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(data)

			s := newScanner(src)
			var reconstructed strings.Builder
			for {
				tok := s.scan()
				for _, tr := range tok.LeadingTrivia {
					reconstructed.WriteString(tr.Raw)
				}
				reconstructed.WriteString(tok.Raw)
				for _, tr := range tok.TrailingTrivia {
					reconstructed.WriteString(tr.Raw)
				}
				if tok.Kind == EOF {
					break
				}
			}

			if reconstructed.String() != src {
				t.Errorf("round-trip failed for %s:\ngot length %d, want length %d", file, reconstructed.Len(), len(src))
				// Show first difference
				got := reconstructed.String()
				for i := 0; i < len(src) && i < len(got); i++ {
					if src[i] != got[i] {
						start := i - 20
						if start < 0 {
							start = 0
						}
						end := i + 20
						if end > len(src) {
							end = len(src)
						}
						endGot := end
						if endGot > len(got) {
							endGot = len(got)
						}
						t.Errorf("first diff at byte %d:\n  got:  %q\n  want: %q", i, got[start:endGot], src[start:end])
						break
					}
				}
			}
		})
	}
}

func TestScannerTokenSequence(t *testing.T) {
	// Verify token sequence for simple.beancount
	data, err := os.ReadFile("testdata/simple.beancount")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)

	s := newScanner(src)
	var kinds []TokenKind
	for {
		tok := s.scan()
		kinds = append(kinds, tok.Kind)
		if tok.Kind == EOF {
			break
		}
	}

	// Verify we got a reasonable number of tokens
	if len(kinds) < 20 {
		t.Errorf("scan(testdata): got %d tokens, want >= 20", len(kinds))
	}

	// Verify first few tokens match expected pattern
	// The file starts with a comment (trivia), then "option" (IDENT)
	if kinds[0] != IDENT {
		t.Errorf("scan(testdata): kinds[0] = %s, want IDENT", kinds[0])
	}
}

func TestScannerAllTokenKinds(t *testing.T) {
	// Verify that all_tokens.beancount produces a variety of token kinds
	data, err := os.ReadFile("testdata/all_tokens.beancount")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)

	s := newScanner(src)
	seen := make(map[TokenKind]bool)
	for {
		tok := s.scan()
		seen[tok.Kind] = true
		if tok.Kind == EOF {
			break
		}
	}

	// We expect to see at least these kinds
	expected := []TokenKind{
		IDENT, STRING, DATE, NUMBER, ACCOUNT, CURRENCY,
		TAG, LINK, BANG, STAR, COMMA, TILDE,
		LBRACE, RBRACE, LBRACE2, RBRACE2,
		AT, ATAT, LPAREN, RPAREN, PLUS,
	}
	for _, k := range expected {
		if !seen[k] {
			t.Errorf("scan(all_tokens.beancount): missing token kind %s", k)
		}
	}
}
