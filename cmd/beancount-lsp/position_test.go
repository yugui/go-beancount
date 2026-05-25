package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/protocol"
)

func TestComputeLineOffsets_LFOnly(t *testing.T) {
	lo := computeLineOffsets([]byte("a\nb\nc"))
	want := lineOffsets{0, 2, 4}
	if d := cmp.Diff(want, lo); d != "" {
		t.Errorf("computeLineOffsets(%q): mismatch (-want +got):\n%s", "a\nb\nc", d)
	}
}

func TestComputeLineOffsets_TrailingLF(t *testing.T) {
	lo := computeLineOffsets([]byte("a\nb\n"))
	want := lineOffsets{0, 2, 4}
	if d := cmp.Diff(want, lo); d != "" {
		t.Errorf("computeLineOffsets(%q): mismatch (-want +got):\n%s", "a\nb\n", d)
	}
}

func TestComputeLineOffsets_NoFinalLF(t *testing.T) {
	lo := computeLineOffsets([]byte("a\nb"))
	want := lineOffsets{0, 2}
	if d := cmp.Diff(want, lo); d != "" {
		t.Errorf("computeLineOffsets(%q): mismatch (-want +got):\n%s", "a\nb", d)
	}
}

func TestComputeLineOffsets_Empty(t *testing.T) {
	lo := computeLineOffsets([]byte{})
	want := lineOffsets{0}
	if d := cmp.Diff(want, lo); d != "" {
		t.Errorf("computeLineOffsets(%q): mismatch (-want +got):\n%s", "", d)
	}
}

func TestComputeLineOffsets_BareCR(t *testing.T) {
	// bare \r is a line terminator, matching scanner.go behaviour
	lo := computeLineOffsets([]byte("a\rb\rc"))
	want := lineOffsets{0, 2, 4}
	if diff := cmp.Diff(want, lo); diff != "" {
		t.Errorf("computeLineOffsets bare-CR mismatch (-want +got):\n%s", diff)
	}
}

func TestComputeLineOffsets_CRLF(t *testing.T) {
	lo := computeLineOffsets([]byte("a\r\nb\r\nc"))
	want := lineOffsets{0, 3, 6}
	if diff := cmp.Diff(want, lo); diff != "" {
		t.Errorf("computeLineOffsets CRLF mismatch (-want +got):\n%s", diff)
	}
}

func TestAstPositionToLSP_ASCII(t *testing.T) {
	src := []byte("hello\n")
	lo := computeLineOffsets(src)
	got := astPositionToLSP(ast.Position{Line: 1, Column: 3}, src, lo)
	if got.Line != 0 || got.Character != 2 {
		t.Errorf("got (%d,%d), want (0,2)", got.Line, got.Character)
	}
}

func TestAstPositionToLSP_CJK3Byte(t *testing.T) {
	// "こんにちは\n" — each rune is 3 bytes, but stays in BMP (1 UTF-16 unit)
	src := []byte("こんにちは\n")
	lo := computeLineOffsets(src)
	// Column 3 → rune index 2 → 2 UTF-16 units
	got := astPositionToLSP(ast.Position{Line: 1, Column: 3}, src, lo)
	if got.Line != 0 || got.Character != 2 {
		t.Errorf("got (%d,%d), want (0,2)", got.Line, got.Character)
	}
}

func TestAstPositionToLSP_EmojiSurrogate(t *testing.T) {
	// "a😀b\n" — 😀 is U+1F600, encoded as 4-byte UTF-8, surrogate pair in UTF-16
	src := []byte("a\xf0\x9f\x98\x80b\n")
	lo := computeLineOffsets(src)
	// Column 3 = rune index 2 (a, 😀 are runes 0 and 1).
	// 'a' → 1 unit, '😀' → 2 units → total 3 before 'b'
	got := astPositionToLSP(ast.Position{Line: 1, Column: 3}, src, lo)
	if got.Line != 0 || got.Character != 3 {
		t.Errorf("got (%d,%d), want (0,3)", got.Line, got.Character)
	}
}

func TestAstPositionToLSP_TAB(t *testing.T) {
	// "a\tb\n" — TAB counts as 1 UTF-16 unit
	src := []byte("a\tb\n")
	lo := computeLineOffsets(src)
	// Column 3 = rune index 2 → 'a'=1 unit, '\t'=1 unit → Character=2
	got := astPositionToLSP(ast.Position{Line: 1, Column: 3}, src, lo)
	if got.Line != 0 || got.Character != 2 {
		t.Errorf("got (%d,%d), want (0,2)", got.Line, got.Character)
	}
}

func TestAstPositionToLSP_LineStart(t *testing.T) {
	src := []byte("abc\ndef\n")
	lo := computeLineOffsets(src)
	// Line 2, Column 1 = start of second line
	got := astPositionToLSP(ast.Position{Line: 2, Column: 1}, src, lo)
	if got.Line != 1 || got.Character != 0 {
		t.Errorf("got (%d,%d), want (1,0)", got.Line, got.Character)
	}
}

func TestAstPositionToLSP_LineEnd(t *testing.T) {
	src := []byte("abc\ndef\n")
	lo := computeLineOffsets(src)
	// Line 1, Column 4 = after 'c', end of first line (3 runes → character 3)
	got := astPositionToLSP(ast.Position{Line: 1, Column: 4}, src, lo)
	if got.Line != 0 || got.Character != 3 {
		t.Errorf("got (%d,%d), want (0,3)", got.Line, got.Character)
	}
}

func TestAstPositionToLSP_PastEOF(t *testing.T) {
	src := []byte("abc\n")
	lo := computeLineOffsets(src)
	// Line 99, Column 99 → clamp to last line, last column
	got := astPositionToLSP(ast.Position{Line: 99, Column: 99}, src, lo)
	// last line is line index 1 (the empty line after the trailing \n), character 0
	// OR line index 0 with character 3 — depends on whether lo has entry for trailing \n
	// computeLineOffsets("abc\n") → [0, 4]; line 0 = "abc", line 1 = "" (past EOF)
	// clamped line = len(lo)-1 = 1; runeLen(lineBytes(src, lo, 1)) = 0
	if got.Line != 1 || got.Character != 0 {
		t.Errorf("got (%d,%d), want (1,0)", got.Line, got.Character)
	}
}

func TestLspPositionToByte_ASCII(t *testing.T) {
	src := []byte("hello\nworld\n")
	lo := computeLineOffsets(src)
	// Line 0, character 3 → byte offset 3 ('l' in "hello")
	got := lspPositionToByte(protocol.Position{Line: 0, Character: 3}, src, lo)
	if got != 3 {
		t.Errorf("lspPositionToByte(0,3): got %d, want 3", got)
	}
}

func TestLspPositionToByte_CJK3Byte(t *testing.T) {
	// "こん\n" — each rune is 3 bytes UTF-8, but 1 UTF-16 unit
	src := []byte("\xe3\x81\x93\xe3\x82\x93\n")
	lo := computeLineOffsets(src)
	// Line 0, character 1 → second CJK rune starts at byte 3
	got := lspPositionToByte(protocol.Position{Line: 0, Character: 1}, src, lo)
	if got != 3 {
		t.Errorf("lspPositionToByte(CJK, 0,1): got %d, want 3", got)
	}
}

func TestLspPositionToByte_EmojiSurrogatePair(t *testing.T) {
	// "a😀b\n" — 😀 is U+1F600, 4 bytes UTF-8, 2 UTF-16 units
	src := []byte("a\xf0\x9f\x98\x80b\n")
	lo := computeLineOffsets(src)
	// Line 0, character 3 (after 'a'=1 unit, '😀'=2 units) → byte offset 5 ('b')
	got := lspPositionToByte(protocol.Position{Line: 0, Character: 3}, src, lo)
	if got != 5 {
		t.Errorf("lspPositionToByte(emoji, 0,3): got %d, want 5", got)
	}
}

func TestLspPositionToByte_LineStart(t *testing.T) {
	src := []byte("abc\ndef\n")
	lo := computeLineOffsets(src)
	// Line 1, character 0 → byte offset 4 (start of "def")
	got := lspPositionToByte(protocol.Position{Line: 1, Character: 0}, src, lo)
	if got != 4 {
		t.Errorf("lspPositionToByte(line-start, 1,0): got %d, want 4", got)
	}
}

func TestLspPositionToByte_LineEnd(t *testing.T) {
	src := []byte("abc\ndef\n")
	lo := computeLineOffsets(src)
	// Line 0, character 3 → byte offset 3 ('\n')
	got := lspPositionToByte(protocol.Position{Line: 0, Character: 3}, src, lo)
	if got != 3 {
		t.Errorf("lspPositionToByte(line-end, 0,3): got %d, want 3", got)
	}
}

func TestLspPositionToByte_PastEOFClamp(t *testing.T) {
	src := []byte("abc\n")
	lo := computeLineOffsets(src)
	// Line 99 → clamp to len(src)
	got := lspPositionToByte(protocol.Position{Line: 99, Character: 0}, src, lo)
	if got != len(src) {
		t.Errorf("lspPositionToByte(past-EOF, 99,0): got %d, want %d", got, len(src))
	}
}

func TestByteOffsetToLSP_ASCII(t *testing.T) {
	src := []byte("hello\nworld\n")
	lo := computeLineOffsets(src)
	// Byte offset 3 → line 0, character 3
	got := byteOffsetToLSP(3, src, lo)
	if got.Line != 0 || got.Character != 3 {
		t.Errorf("byteOffsetToLSP(3): got (%d,%d), want (0,3)", got.Line, got.Character)
	}
}

func TestByteOffsetToLSP_CJK3Byte(t *testing.T) {
	// "こん\n" — each CJK rune is 3 bytes UTF-8, 1 UTF-16 unit
	src := []byte("\xe3\x81\x93\xe3\x82\x93\n")
	lo := computeLineOffsets(src)
	// Byte offset 3 (start of second CJK rune) → line 0, character 1
	got := byteOffsetToLSP(3, src, lo)
	if got.Line != 0 || got.Character != 1 {
		t.Errorf("byteOffsetToLSP(CJK, 3): got (%d,%d), want (0,1)", got.Line, got.Character)
	}
}

func TestByteOffsetToLSP_EmojiSurrogatePair(t *testing.T) {
	// "a😀b\n" — 😀 is U+1F600, 4 bytes UTF-8, 2 UTF-16 units
	src := []byte("a\xf0\x9f\x98\x80b\n")
	lo := computeLineOffsets(src)
	// Byte offset 5 ('b') → line 0, character 3 ('a'=1, '😀'=2)
	got := byteOffsetToLSP(5, src, lo)
	if got.Line != 0 || got.Character != 3 {
		t.Errorf("byteOffsetToLSP(emoji, 5): got (%d,%d), want (0,3)", got.Line, got.Character)
	}
}

func TestByteOffsetToLSP_LineStart(t *testing.T) {
	src := []byte("abc\ndef\n")
	lo := computeLineOffsets(src)
	// Byte offset 4 (start of "def") → line 1, character 0
	got := byteOffsetToLSP(4, src, lo)
	if got.Line != 1 || got.Character != 0 {
		t.Errorf("byteOffsetToLSP(line-start, 4): got (%d,%d), want (1,0)", got.Line, got.Character)
	}
}

func TestByteOffsetToLSP_LineEnd(t *testing.T) {
	src := []byte("abc\ndef\n")
	lo := computeLineOffsets(src)
	// Byte offset 3 ('\n') → line 0, character 3
	got := byteOffsetToLSP(3, src, lo)
	if got.Line != 0 || got.Character != 3 {
		t.Errorf("byteOffsetToLSP(line-end, 3): got (%d,%d), want (0,3)", got.Line, got.Character)
	}
}

func TestByteOffsetToLSP_PastEOFClamp(t *testing.T) {
	src := []byte("abc\n")
	lo := computeLineOffsets(src)
	// Byte offset past EOF → clamp to last position (line 1, character 0)
	got := byteOffsetToLSP(999, src, lo)
	// computeLineOffsets("abc\n") → [0, 4]; EOF is at line 1, char 0
	if got.Line != 1 || got.Character != 0 {
		t.Errorf("byteOffsetToLSP(past-EOF, 999): got (%d,%d), want (1,0)", got.Line, got.Character)
	}
}

func TestAstSpanToLSP(t *testing.T) {
	// Multi-line span with mixed-width content
	// Line 1: "a😀\n" (a=1 unit, 😀=2 units)
	// Line 2: "bc\n"
	src := []byte("a\xf0\x9f\x98\x80\nbc\n")
	lo := computeLineOffsets(src)
	span := ast.Span{
		Start: ast.Position{Line: 1, Column: 2}, // '😀' → character 1 (after 'a')
		End:   ast.Position{Line: 2, Column: 2}, // 'c' → character 1 (after 'b')
	}
	got := astSpanToLSP(span, src, lo)
	if got.Start.Line != 0 || got.Start.Character != 1 {
		t.Errorf("Start: got (%d,%d), want (0,1)", got.Start.Line, got.Start.Character)
	}
	if got.End.Line != 1 || got.End.Character != 1 {
		t.Errorf("End: got (%d,%d), want (1,1)", got.End.Line, got.End.Character)
	}
}
