package comment

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/yugui/go-beancount/pkg/ast"
)

var jan15 = time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)

// blockShape captures the value-typed fields of a Block for one-shot
// comparison via cmp.Diff. The Body and Directive fields are excluded
// because Body is a derived join and Directive is an interface that is
// inspected separately for kind/date.
type blockShape struct {
	SourcePath string
	StartLine  int
	EndLine    int
	Indent     string
}

type want struct {
	shape   blockShape
	dirKind ast.DirectiveKind
	dirDate string // YYYY-MM-DD; empty means no date check
}

func TestExtract(t *testing.T) {
	const path = "test.beancount"
	tests := []struct {
		name string
		src  string
		want []want
	}{
		{
			name: "single_directive",
			src: "; 2024-01-15 * \"Coffee\"\n" +
				";   Expenses:Food   3.50 USD\n" +
				";   Assets:Cash    -3.50 USD\n",
			want: []want{{blockShape{path, 0, 3, "; "}, ast.KindTransaction, "2024-01-15"}},
		},
		{
			name: "prefix_no_space",
			src:  ";2024-01-15 open Assets:Bank\n",
			want: []want{{blockShape{path, 0, 1, ";"}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "prefix_multiple_spaces",
			src:  ";   2024-01-15 open Assets:Bank\n",
			want: []want{{blockShape{path, 0, 1, ";   "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "prefix_tab",
			src:  ";\t2024-01-15 open Assets:Bank\n",
			want: []want{{blockShape{path, 0, 1, ";\t"}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "block_terminated_by_blank_line",
			src: "; 2024-01-15 open Assets:Bank\n" +
				"\n" +
				"; 2024-02-15 open Assets:Cash\n",
			want: []want{
				{blockShape{path, 0, 1, "; "}, ast.KindOpen, "2024-01-15"},
				{blockShape{path, 2, 3, "; "}, ast.KindOpen, "2024-02-15"},
			},
		},
		{
			name: "block_terminated_by_active_directive",
			src: "; 2024-01-15 open Assets:Bank\n" +
				"2024-02-15 open Assets:Cash\n",
			want: []want{{blockShape{path, 0, 1, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "block_terminated_by_shorter_prefix",
			src: "; 2024-01-15 open Assets:Bank\n" +
				";\n",
			want: []want{{blockShape{path, 0, 1, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "non_dated_block_dropped",
			src: "; just a regular comment\n" +
				";another comment\n",
			want: nil,
		},
		{
			name: "multiple_blocks",
			src: "; 2024-01-15 open Assets:Bank\n" +
				"\n" +
				"; 2024-02-15 close Assets:Bank\n",
			want: []want{
				{blockShape{path, 0, 1, "; "}, ast.KindOpen, "2024-01-15"},
				{blockShape{path, 2, 3, "; "}, ast.KindClose, "2024-02-15"},
			},
		},
		{
			name: "crlf_line_endings",
			src:  "; 2024-01-15 open Assets:Bank\r\n;   foo: \"bar\"\r\n",
			want: []want{{blockShape{path, 0, 2, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "eof_no_terminator",
			src:  "; 2024-01-15 open Assets:Bank",
			want: []want{{blockShape{path, 0, 1, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "interleaved_with_active_directives",
			src: "2024-01-10 open Assets:Cash\n" +
				"; 2024-01-15 open Assets:Bank\n" +
				"2024-01-20 close Assets:Cash\n",
			want: []want{{blockShape{path, 1, 2, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "commented_with_metadata",
			src: "; 2024-01-15 open Assets:Bank\n" +
				";   foo: \"bar\"\n",
			want: []want{{blockShape{path, 0, 2, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			// A trailing same-prefixed plain-comment annotation is included
			// in the recognized block: ast.Load recovers the leading
			// directive and reports a diagnostic for the annotation line
			// (Len() >= 1), so the K=N attempt of the tail-shrink loop wins
			// and EndLine reflects the full N-line candidate. The dropped-
			// tail behavior in tryParse only fires if parser recovery ever
			// regresses; see the comment on tryParse.
			name: "trailing_annotation_included_via_recovery",
			src: "; 2024-01-15 * \"Coffee\" \"Espresso bar\"\n" +
				";   Expenses:Food:Cafe   3.50 USD\n" +
				";   Assets:Cash         -3.50 USD\n" +
				"; receipt was scanned 2024-01-16\n",
			want: []want{{blockShape{path, 0, 4, "; "}, ast.KindTransaction, "2024-01-15"}},
		},
		{
			name: "empty_source",
			src:  "",
			want: nil,
		},
		{
			name: "no_semicolons",
			src:  "2024-01-15 open Assets:Bank\n",
			want: nil,
		},
		{
			name: "candidate_after_prelude",
			src: "; preamble\n" +
				"\n" +
				"option \"title\" \"X\"\n" +
				"\n" +
				"\n" +
				"; 2024-01-15 open Assets:Bank\n",
			want: []want{{blockShape{path, 5, 6, "; "}, ast.KindOpen, "2024-01-15"}},
		},
		{
			name: "non_directive_after_semicolon_dropped",
			src:  ";option \"foo\" \"bar\"\n",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Extract(tc.src, path)
			if len(got) != len(tc.want) {
				t.Fatalf("Extract(...) returned %d blocks, want %d:\n  got=%+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				gotShape := blockShape{
					SourcePath: got[i].SourcePath,
					StartLine:  got[i].StartLine,
					EndLine:    got[i].EndLine,
					Indent:     got[i].Indent,
				}
				if diff := cmp.Diff(w.shape, gotShape); diff != "" {
					t.Errorf("Extract(...) block %d shape mismatch (-want +got):\n%s", i, diff)
				}
				if got[i].Directive == nil {
					t.Fatalf("Extract(...) block %d: Directive is nil", i)
				}
				if k := got[i].Directive.DirKind(); k != w.dirKind {
					t.Errorf("Extract(...) block %d: DirKind = %v, want %v", i, k, w.dirKind)
				}
				if w.dirDate != "" {
					if d := got[i].Directive.DirDate().Format("2006-01-02"); d != w.dirDate {
						t.Errorf("Extract(...) block %d: DirDate = %q, want %q", i, d, w.dirDate)
					}
				}
			}
		})
	}
}

// TestExtract_NoParseEvenAtKEqualsOne ensures that a candidate block whose
// every prefix length fails to lower a directive is dropped, not surfaced
// as a Block with a nil Directive.
func TestExtract_NoParseEvenAtKEqualsOne(t *testing.T) {
	src := "; 2024-01-15 garbled garbled garbled\n"
	got := Extract(src, "test.beancount")
	if len(got) != 0 {
		t.Fatalf("Extract(...) returned %d blocks, want 0: %+v", len(got), got)
	}
}

// TestExtract_DirectiveContent verifies that the recognized directive
// carries the same field values it would have if the source had been
// parsed without the leading `;` prefix.
func TestExtract_DirectiveContent(t *testing.T) {
	src := "; 2024-01-15 open Assets:Bank USD\n"
	got := Extract(src, "test.beancount")
	if len(got) != 1 {
		t.Fatalf("Extract(...) returned %d blocks, want 1", len(got))
	}
	open, ok := got[0].Directive.(*ast.Open)
	if !ok {
		t.Fatalf("Extract(...) block 0 Directive type = %T, want *ast.Open", got[0].Directive)
	}
	wantOpen := &ast.Open{
		Date:       jan15,
		Account:    ast.Assets.MustSub("Bank"),
		Currencies: []string{"USD"},
	}
	// The lowered directive carries a span that depends on the input layout;
	// only the semantic fields are asserted.
	if diff := cmp.Diff(wantOpen, open, cmpopts.IgnoreFields(ast.Open{}, "Span", "Meta")); diff != "" {
		t.Errorf("Extract(...) block 0 Open mismatch (-want +got):\n%s", diff)
	}
}

func TestEmit(t *testing.T) {
	tests := []struct {
		name   string
		d      ast.Directive
		prefix string
		want   string
	}{
		{
			name:   "single_line_open",
			d:      &ast.Open{Date: jan15, Account: ast.Assets.MustSub("Bank")},
			prefix: "; ",
			want:   "; 2024-01-15 open Assets:Bank\n",
		},
		{
			name:   "custom_prefix_no_space",
			d:      &ast.Open{Date: jan15, Account: ast.Assets.MustSub("Bank")},
			prefix: ";",
			want:   ";2024-01-15 open Assets:Bank\n",
		},
		{
			name:   "custom_prefix_three_spaces",
			d:      &ast.Open{Date: jan15, Account: ast.Assets.MustSub("Bank")},
			prefix: ";   ",
			want:   ";   2024-01-15 open Assets:Bank\n",
		},
		{
			name:   "custom_prefix_tab",
			d:      &ast.Open{Date: jan15, Account: ast.Assets.MustSub("Bank")},
			prefix: ";\t",
			want:   ";\t2024-01-15 open Assets:Bank\n",
		},
		{
			name: "multi_line_close_with_metadata",
			d: &ast.Close{
				Date:    jan15,
				Account: ast.Assets.MustSub("Bank"),
				Meta: ast.Metadata{Props: map[string]ast.MetaValue{
					"foo": {Kind: ast.MetaString, String: "bar"},
				}},
			},
			prefix: "; ",
			want: "; 2024-01-15 close Assets:Bank\n" +
				";   foo: \"bar\"\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Emit(&buf, tc.d, tc.prefix); err != nil {
				t.Fatalf("Emit(_, _, %q) returned error: %v", tc.prefix, err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("Emit(_, _, %q) =\n  got: %q\n want: %q", tc.prefix, got, tc.want)
			}
		})
	}
}

// TestEmit_TrailingNewline confirms the emitter ends with exactly one
// newline regardless of how many lines the directive spans.
func TestEmit_TrailingNewline(t *testing.T) {
	d := &ast.Close{
		Date:    jan15,
		Account: ast.Assets.MustSub("Bank"),
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"k1": {Kind: ast.MetaString, String: "v1"},
			"k2": {Kind: ast.MetaString, String: "v2"},
		}},
	}
	var buf bytes.Buffer
	if err := Emit(&buf, d, "; "); err != nil {
		t.Fatalf("Emit(...) returned error: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("Emit(...) output does not end with newline: %q", out)
	}
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("Emit(...) output ends with multiple newlines: %q", out)
	}
}

// TestExtractAndEmit_RoundTrip extracts a recognized block and re-emits it
// with the captured Indent. The emitted bytes should re-parse (after
// stripping the prefix) to a directive of the same kind and date and the
// same Indent.
func TestExtractAndEmit_RoundTrip(t *testing.T) {
	src := ";   2024-01-15 open Assets:Bank USD\n"
	got := Extract(src, "test.beancount")
	if len(got) != 1 {
		t.Fatalf("Extract(...) returned %d blocks, want 1", len(got))
	}
	b := got[0]

	var buf bytes.Buffer
	if err := Emit(&buf, b.Directive, b.Indent); err != nil {
		t.Fatalf("Emit(...) returned error: %v", err)
	}
	out := buf.String()

	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !strings.HasPrefix(line, b.Indent) {
			t.Errorf("Emit(...) output line %d %q does not start with Indent %q", i, line, b.Indent)
		}
	}

	again := Extract(out, "test.beancount")
	if len(again) != 1 {
		t.Fatalf("re-Extract(...) returned %d blocks, want 1", len(again))
	}
	if again[0].Directive.DirKind() != b.Directive.DirKind() {
		t.Errorf("re-Extract DirKind = %v, want %v",
			again[0].Directive.DirKind(), b.Directive.DirKind())
	}
	if !again[0].Directive.DirDate().Equal(b.Directive.DirDate()) {
		t.Errorf("re-Extract DirDate = %v, want %v",
			again[0].Directive.DirDate(), b.Directive.DirDate())
	}
	if again[0].Indent != b.Indent {
		t.Errorf("re-Extract Indent = %q, want %q", again[0].Indent, b.Indent)
	}
}
