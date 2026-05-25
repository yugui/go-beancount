package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
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
