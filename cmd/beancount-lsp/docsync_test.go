package main

import (
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func pos(line, ch uint32) protocol.Position {
	return protocol.Position{Line: line, Character: ch}
}

func rng(sl, sc, el, ec uint32) *protocol.Range {
	return &protocol.Range{Start: pos(sl, sc), End: pos(el, ec)}
}

func TestApplyChange(t *testing.T) {
	tests := []struct {
		name string
		buf  string
		ch   rawContentChange
		want string
	}{
		{
			name: "full replace (range nil)",
			buf:  "old content\n",
			ch:   rawContentChange{Range: nil, Text: "new content\n"},
			want: "new content\n",
		},
		{
			name: "full replace to empty",
			buf:  "old\n",
			ch:   rawContentChange{Range: nil, Text: ""},
			want: "",
		},
		{
			name: "insert at start (distinguishes from full replace)",
			buf:  "world\n",
			ch:   rawContentChange{Range: rng(0, 0, 0, 0), Text: "hello "},
			want: "hello world\n",
		},
		{
			name: "insert in middle of line",
			buf:  "ab\n",
			ch:   rawContentChange{Range: rng(0, 1, 0, 1), Text: "X"},
			want: "aXb\n",
		},
		{
			name: "insert at EOF (no trailing newline)",
			buf:  "abc",
			ch:   rawContentChange{Range: rng(0, 3, 0, 3), Text: "def"},
			want: "abcdef",
		},
		{
			name: "delete single character",
			buf:  "abc\n",
			ch:   rawContentChange{Range: rng(0, 1, 0, 2), Text: ""},
			want: "ac\n",
		},
		{
			name: "replace single character",
			buf:  "abc\n",
			ch:   rawContentChange{Range: rng(0, 1, 0, 2), Text: "XY"},
			want: "aXYc\n",
		},
		{
			name: "replace across lines",
			buf:  "line1\nline2\nline3\n",
			ch:   rawContentChange{Range: rng(0, 4, 2, 0), Text: "X"},
			want: "lineXline3\n",
		},
		{
			name: "delete entire line including newline",
			buf:  "a\nb\nc\n",
			ch:   rawContentChange{Range: rng(1, 0, 2, 0), Text: ""},
			want: "a\nc\n",
		},
		{
			name: "insert newline",
			buf:  "abc",
			ch:   rawContentChange{Range: rng(0, 1, 0, 1), Text: "\n"},
			want: "a\nbc",
		},
		{
			name: "utf-16 surrogate pair: insert before emoji",
			buf:  "\U0001F600 tail",
			ch:   rawContentChange{Range: rng(0, 0, 0, 0), Text: "X"},
			want: "X\U0001F600 tail",
		},
		{
			name: "utf-16 surrogate pair: insert after emoji at character 2",
			buf:  "\U0001F600 tail",
			ch:   rawContentChange{Range: rng(0, 2, 0, 2), Text: "X"},
			want: "\U0001F600X tail",
		},
		{
			name: "japanese: replace one BMP character",
			buf:  "あいう",
			ch:   rawContentChange{Range: rng(0, 1, 0, 2), Text: "X"},
			want: "あXう",
		},
		{
			name: "reversed range is normalized",
			buf:  "abcd",
			ch:   rawContentChange{Range: rng(0, 3, 0, 1), Text: "X"},
			want: "aXd",
		},
		{
			name: "empty buffer: insert at (0,0)",
			buf:  "",
			ch:   rawContentChange{Range: rng(0, 0, 0, 0), Text: "hello"},
			want: "hello",
		},
		{
			name: "past-EOF position clamps to end",
			buf:  "abc",
			ch:   rawContentChange{Range: rng(5, 0, 5, 0), Text: "X"},
			want: "abcX",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(applyChange([]byte(tc.buf), tc.ch))
			if got != tc.want {
				t.Errorf("applyChange: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyChanges_SequentialSplice(t *testing.T) {
	ds := newDocStore()
	u := uri.File("/x.beancount")
	ds.set(u, 1, []byte("hello world\n"))

	// Two changes in one batch:
	//   1. replace "world" with "there"   → "hello there\n"
	//   2. replace "there" with "you"     → "hello you\n"
	// Offsets for change 2 must be resolved against the buffer state
	// produced by change 1, not the original.
	changes := []rawContentChange{
		{Range: rng(0, 6, 0, 11), Text: "there"},
		{Range: rng(0, 6, 0, 11), Text: "you"},
	}
	got := string(applyChanges(ds, u, changes))
	want := "hello you\n"
	if got != want {
		t.Errorf("applyChanges: got %q, want %q", got, want)
	}
}

func TestApplyChanges_UserBugRepro(t *testing.T) {
	// The original Phase 11 implementation treated every change as a
	// full-document replace, so an incremental edit truncated the buffer
	// to just the change text. This regression test pins the fix.
	ds := newDocStore()
	u := uri.File("/lsp.beancount")
	original := "2020-01-01 open Assets:A\n2020-01-02 balance Assets:A 100 USD\n"
	ds.set(u, 1, []byte(original))

	// Editor inserts "1" before the "100" on line 1, changing it to "1100".
	changes := []rawContentChange{
		{Range: rng(1, 28, 1, 28), Text: "1"},
	}
	got := string(applyChanges(ds, u, changes))
	want := "2020-01-01 open Assets:A\n2020-01-02 balance Assets:A 1100 USD\n"
	if got != want {
		t.Errorf("applyChanges (incremental insert): got %q, want %q", got, want)
	}
}

func TestApplyChanges_FullReplaceUnchanged(t *testing.T) {
	// Clients sending TextDocumentSyncKindFull omit `range`; the JSON
	// decoder leaves Range nil and we must replace the entire buffer.
	ds := newDocStore()
	u := uri.File("/x.beancount")
	ds.set(u, 1, []byte("old\n"))

	changes := []rawContentChange{
		{Range: nil, Text: "wholly new\n"},
	}
	got := string(applyChanges(ds, u, changes))
	want := "wholly new\n"
	if got != want {
		t.Errorf("applyChanges (full replace): got %q, want %q", got, want)
	}
}
