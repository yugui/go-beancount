package ast

import (
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/syntax"
)

// TestParseDateAcceptsConsistentSeparators verifies that both canonical
// beancount date forms parse to the same calendar date.
func TestParseDateAcceptsConsistentSeparators(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"dash", "2024-01-15"},
		{"slash", "2024/01/15"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDate(&syntax.Token{Raw: tc.raw})
			if err != nil {
				t.Fatalf("parseDate(%q) error = %v, want nil", tc.raw, err)
			}
			if y, m, d := got.Date(); y != 2024 || m != 1 || d != 15 {
				t.Errorf("parseDate(%q) = %04d-%02d-%02d, want 2024-01-15", tc.raw, y, m, d)
			}
		})
	}
}

// TestParseDateRejectsMixedSeparators verifies that mixing '-' and '/'
// within the same date is reported as an error rather than silently
// normalized; the error message must include the offending input so
// callers surface the input mistake to users.
func TestParseDateRejectsMixedSeparators(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"dash-then-slash", "2024-01/15"},
		{"slash-then-dash", "2024/01-15"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDate(&syntax.Token{Raw: tc.raw})
			if err == nil {
				t.Fatalf("parseDate(%q) error = nil, want non-nil", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.raw) {
				t.Errorf("parseDate(%q) error = %q, want message containing %q", tc.raw, err.Error(), tc.raw)
			}
		})
	}
}
