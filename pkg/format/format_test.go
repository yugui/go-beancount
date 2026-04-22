package format

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatIdentity(t *testing.T) {
	// Already well-formatted input should be unchanged.
	// "  Expenses:Food" = 15 display cols, "50.00 USD" = 9, padding = 52-15-9 = 28
	src := "2024-01-15 * \"Store\" \"Groceries\"\n  Expenses:Food                            50.00 USD\n  Assets:Checking\n"
	got := Format(src)
	if got != src {
		t.Errorf("Format() changed well-formatted input:\ngot:\n%q\nwant:\n%q", got, src)
	}
}

func TestFormatBlankLineNormalization(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no blank lines between directives normalized to 1",
			src:  "2024-01-01 open Assets:Cash\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "three blank lines normalized to 1",
			src:  "2024-01-01 open Assets:Cash\n\n\n\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "already one blank line stays same",
			src:  "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.src)
			if got != tt.want {
				t.Errorf("Format(%q):\ngot:\n%s\nwant:\n%s", tt.src, got, tt.want)
			}
		})
	}
}

func TestFormatBlankLinesBetweenDirectivesZero(t *testing.T) {
	src := "2024-01-01 open Assets:Cash\n\n2024-01-02 open Expenses:Food\n"
	want := "2024-01-01 open Assets:Cash\n2024-01-02 open Expenses:Food\n"
	got := Format(src, WithBlankLinesBetweenDirectives(0))
	if got != want {
		t.Errorf("Format(%q):\ngot:\n%s\nwant:\n%s", src, got, want)
	}
}

func TestFormatIndentNormalization(t *testing.T) {
	// Wrong indent (4 spaces) should be fixed to 2 spaces (default).
	// "  Expenses:Food" = 15 cols, "50.00 USD" = 9 cols, padding = 52-15-9 = 28
	src := "2024-01-15 * \"Test\"\n    Expenses:Food  50.00 USD\n    Assets:Cash\n"
	want := "2024-01-15 * \"Test\"\n  Expenses:Food                            50.00 USD\n  Assets:Cash\n"
	got := Format(src)
	if got != want {
		t.Errorf("Format(%q):\ngot:\n%q\nwant:\n%q", src, got, want)
	}
}

func TestFormatAmountAlignment(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  Expenses:Food 50.00 USD\n  Assets:Checking -50.00 USD\n"
	got := Format(src)
	// Expenses:Food is 13 chars, indent is 2 => 15 used.
	// "50.00 USD" is 9 chars, trailing space in NUMBER trivia = part of amount.
	// padding = 52 - 15 - 9 = 28 spaces.
	// Assets:Checking is 16 chars, indent is 2 => 18 used.
	// "-50.00 USD" is 10 chars. padding = 52 - 18 - 10 = 24 spaces.
	want := "2024-01-15 * \"Test\"\n  Expenses:Food                            50.00 USD\n  Assets:Checking                         -50.00 USD\n"
	if got != want {
		t.Errorf("Format(%q):\ngot:\n%q\nwant:\n%q", src, got, want)
	}
}

func TestFormatAmountAlignmentDisabled(t *testing.T) {
	// With AlignAmounts=false, only indent should change, not amount spacing.
	src := "2024-01-15 * \"Test\"\n  Expenses:Food 50.00 USD\n  Assets:Cash\n"
	got := Format(src, WithAlignAmounts(false))
	want := "2024-01-15 * \"Test\"\n  Expenses:Food 50.00 USD\n  Assets:Cash\n"
	if got != want {
		t.Errorf("Format(%q):\ngot:\n%s\nwant:\n%s", src, got, want)
	}
}

func TestFormatCommaGroupingInsert(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  Expenses:Food  1234567.89 USD\n  Assets:Cash\n"
	got := Format(src, WithCommaGrouping(true))
	if !strings.Contains(got, "1,234,567.89") {
		t.Errorf("Format(%q, WithCommaGrouping(true)) should insert commas, got:\n%s", src, got)
	}
}

func TestFormatCommaGroupingStrip(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  Expenses:Food  1,234.56 USD\n  Assets:Cash\n"
	got := Format(src) // default CommaGrouping is false
	if strings.Contains(got, ",") {
		t.Errorf("Format(%q) with CommaGrouping=false should strip commas, got:\n%s", src, got)
	}
}

func TestFormatUnicodeAwareAlignment(t *testing.T) {
	// CJK characters are double-width.
	src := "2024-01-15 * \"Test\"\n  Expenses:\u98df\u54c1 50.00 USD\n  Assets:Cash\n"
	got := Format(src)
	if got == "" {
		t.Errorf("Format(%q) returned empty string for Unicode input", src)
	}
	if !strings.Contains(got, "50.00") {
		t.Errorf("Format(%q) lost number, got:\n%s", src, got)
	}
}

func TestFormatMetadataIndent(t *testing.T) {
	// Posting metadata (at 4-space indent, inside PostingNode) should get double indent.
	src := "2024-01-15 * \"Test\"\n  Expenses:Food  50.00 USD\n    note: \"receipt\"\n  Assets:Cash\n"
	got := Format(src)
	if !strings.Contains(got, "\n    note:") {
		t.Errorf("Format(%q) should keep posting metadata at double indent (4 spaces), got:\n%s", src, got)
	}
}

func TestFormatTransactionMetadata(t *testing.T) {
	// Transaction-level metadata gets single indent.
	src := "2024-01-15 * \"Test\"\n      note: \"txn-level\"\n  Expenses:Food  50.00 USD\n  Assets:Cash\n"
	got := Format(src)
	if !strings.Contains(got, "\n  note:") {
		t.Errorf("Format(%q) should set transaction-level metadata to single indent, got:\n%s", src, got)
	}
}

func TestFormatNonTransactionDirectives(t *testing.T) {
	// Non-transaction directives should be preserved (except blank lines).
	src := "option \"operating_currency\" \"USD\"\n\nplugin \"beancount.plugins.auto\"\n"
	got := Format(src)
	if got != src {
		t.Errorf("Format(%q):\ngot:\n%s\nwant:\n%s", src, got, src)
	}
}

func TestFormatCommentsPreserved(t *testing.T) {
	src := "; This is a comment\n2024-01-01 open Assets:Cash\n"
	got := Format(src)
	if !strings.Contains(got, "; This is a comment") {
		t.Errorf("Format(%q) lost comment, got:\n%s", src, got)
	}
}

func TestFormatInterDirectiveComments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "single comment between directives",
			src:  "2024-01-01 open Assets:Cash\n; section header\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n; section header\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "multiple comments between directives",
			src:  "2024-01-01 open Assets:Cash\n; line 1\n; line 2\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n; line 1\n; line 2\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "comment with surrounding blank lines preserved",
			src:  "2024-01-01 open Assets:Cash\n\n; comment\n\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n; comment\n2024-01-02 open Expenses:Food\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.src)
			if got != tt.want {
				t.Errorf("Format():\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestFormatHeadingsPreserved(t *testing.T) {
	src := "* Section\n2024-01-01 open Assets:Cash\n"
	got := Format(src)
	if !strings.Contains(got, "* Section") {
		t.Errorf("Format(%q) lost heading, got:\n%s", src, got)
	}
}

func TestFormatInterDirectiveHeadings(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "heading between directives",
			src:  "2024-01-01 open Assets:Cash\n* Section\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n* Section\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "deeper heading between directives",
			src:  "2024-01-01 open Assets:Cash\n** Subsection\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n** Subsection\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "heading with surrounding blank lines",
			src:  "2024-01-01 open Assets:Cash\n\n* Section\n\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n* Section\n2024-01-02 open Expenses:Food\n",
		},
		{
			name: "heading and comment between directives",
			src:  "2024-01-01 open Assets:Cash\n* Section\n; comment\n2024-01-02 open Expenses:Food\n",
			want: "2024-01-01 open Assets:Cash\n\n* Section\n; comment\n2024-01-02 open Expenses:Food\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.src)
			if got != tt.want {
				t.Errorf("Format():\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestFormatErrorNodesPassThrough(t *testing.T) {
	src := "this is not valid beancount\n"
	got := Format(src)
	if !strings.Contains(got, "this is not valid") {
		t.Errorf("Format(%q) lost error content, got:\n%s", src, got)
	}
}

func TestFormatEmptyInput(t *testing.T) {
	got := Format("")
	if got != "" {
		t.Errorf("Format(\"\") = %q, want \"\"", got)
	}
}

func TestFormatPreservesLeadingFileComments(t *testing.T) {
	src := "; File header comment\n; Another comment\n\n2024-01-01 open Assets:Cash\n"
	got := Format(src)
	if !strings.Contains(got, "; File header comment") || !strings.Contains(got, "; Another comment") {
		t.Errorf("Format(%q) lost leading file comments, got:\n%s", src, got)
	}
}

func TestFormatPostingWithCostSpec(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n  Assets:Stock  10 HOOL {100 USD} @ 150 USD\n  Assets:Cash\n"
	got := Format(src)
	if got == "" {
		t.Errorf("Format(%q) returned empty string", src)
	}
	if !strings.Contains(got, "HOOL") || !strings.Contains(got, "{100 USD}") || !strings.Contains(got, "@ 150 USD") {
		t.Errorf("Format(%q) lost cost/price info, got:\n%s", src, got)
	}
}

func TestFormatPostingWithCombinedCostSpec(t *testing.T) {
	// Round-trip the combined per-unit/total cost form `{per # total CCY}`.
	// The formatter should preserve it byte-for-byte (modulo amount alignment
	// padding, which is determined by the AmountNode width and not the cost).
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "combined cost only",
			// "  Assets:Brokerage" = 18 cols, "10 GOOG" = 7 cols, padding = 52-18-7 = 27.
			// "  Assets:Cash" = 13 cols, "-5031.15 USD" = 12 cols, padding = 52-13-12 = 27.
			src: "2024-01-15 * \"Buy GOOG with commission\"\n" +
				"  Assets:Brokerage                           10 GOOG {502.12 # 9.95 USD}\n" +
				"  Assets:Cash                           -5031.15 USD\n",
		},
		{
			name: "combined cost with explicit per-unit currency",
			src: "2024-01-15 * \"Buy GOOG\"\n" +
				"  Assets:Brokerage                           10 GOOG {502.12 USD # 9.95 USD}\n" +
				"  Assets:Cash                           -5031.15 USD\n",
		},
		{
			name: "combined cost with date and label",
			src: "2024-01-15 * \"Buy GOOG\"\n" +
				"  Assets:Brokerage                           10 GOOG {502.12 # 9.95 USD, 2024-01-15, \"lot1\"}\n" +
				"  Assets:Cash                           -5031.15 USD\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.src)
			if got != tt.src {
				t.Errorf("Format() not byte-for-byte stable:\ngot:\n%q\nwant:\n%q", got, tt.src)
			}
		})
	}
}

func TestFormatAutoBalancedPosting(t *testing.T) {
	src := "2024-01-15 * \"Test\"\n     Expenses:Food  50.00 USD\n     Assets:Cash\n"
	got := Format(src)
	if !strings.Contains(got, "\n  Assets:Cash") {
		t.Errorf("Format(%q) should fix indent for auto-balanced posting, got:\n%s", src, got)
	}
}

func TestFormatStringPreservation(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "multiline",
			src:  "2024-01-01 note Assets:A \"Line one\nline two\"\n",
		},
		{
			name: "tab",
			src:  "2024-01-01 note Assets:A \"before\tafter\"\n",
		},
		{
			name: "carriage return",
			src:  "2024-01-01 note Assets:A \"before\rafter\"\n",
		},
		{
			name: "backslash",
			src:  "2024-01-01 note Assets:A \"path\\\\to\\\\file\"\n",
		},
		{
			name: "escaped quote",
			src:  "2024-01-01 note Assets:A \"say \\\"hello\\\"\"\n",
		},
		{
			name: "accented",
			src:  "2024-01-01 note Assets:A \"café résumé\"\n",
		},
		{
			name: "combining character",
			src:  "2024-01-01 note Assets:A \"e\u0301\"\n",
		},
		{
			name: "CJK",
			src:  "2024-01-01 note Assets:A \"日本語テスト\"\n",
		},
		{
			name: "emoji",
			src:  "2024-01-01 note Assets:A \"🎉 party\"\n",
		},
		{
			name: "mixed newline and special",
			src:  "2024-01-01 note Assets:A \"café\n日本語\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.src)
			if got != tt.src {
				t.Errorf("Format(%q) changed string content:\ngot:  %q\nwant: %q", tt.src, got, tt.src)
			}
		})
	}
}

func TestFormatReader(t *testing.T) {
	src := "2024-01-01 open Assets:Bank\n"
	got, err := FormatReader(strings.NewReader(src))
	if err != nil {
		t.Fatalf("FormatReader: %v", err)
	}
	if want := Format(src); got != want {
		t.Errorf("FormatReader output differs from Format:\ngot:  %q\nwant: %q", got, want)
	}
}

// failingReader returns its configured error on the first Read call.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestFormatReaderError(t *testing.T) {
	want := errors.New("boom")
	got, err := FormatReader(failingReader{err: want})
	if got != "" {
		t.Errorf("FormatReader(failingReader{err: %v}) returned non-empty string %q, want \"\"", want, got)
	}
	if !errors.Is(err, want) {
		t.Errorf("FormatReader(failingReader{err: %v}) error = %v, want %v", want, err, want)
	}
}

func TestFormatFile(t *testing.T) {
	src := "2024-01-01 open Assets:Bank\n"
	path := filepath.Join(t.TempDir(), "a.beancount")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := FormatFile(path)
	if err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if want := Format(src); got != want {
		t.Errorf("FormatFile output differs from Format:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFormatFileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.beancount")
	got, err := FormatFile(path)
	if got != "" {
		t.Errorf("FormatFile(%q) returned non-empty string %q, want \"\"", path, got)
	}
	if err == nil {
		t.Errorf("FormatFile(%q) returned nil error, want non-nil", path)
	}
}
