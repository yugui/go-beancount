package main

import (
	"unicode/utf8"

	"github.com/yugui/go-beancount/pkg/ast"
	"go.lsp.dev/protocol"
)

// lineOffsets is the byte offset of each line's first byte, indexed from 0.
type lineOffsets []int

// computeLineOffsets builds a line-start byte offset table for src.
// Recognizes \n, \r\n, and bare \r as line terminators, matching the scanner.
func computeLineOffsets(src []byte) lineOffsets {
	lo := lineOffsets{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			lo = append(lo, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				// CRLF
				lo = append(lo, i+2)
				i++
			} else {
				// bare CR
				lo = append(lo, i+1)
			}
		}
	}
	return lo
}

// astPositionToLSP converts an ast.Position (Line 1-based, Column 1-based
// rune index) to an LSP Position (Line 0-based, Character UTF-16 code-unit
// index). src is the source bytes for the file; lo is its pre-built offset
// table.
//
// Past-EOF positions are clamped to the last valid position. Invalid UTF-8
// bytes each count as one UTF-16 unit, matching gopls behaviour.
func astPositionToLSP(p ast.Position, src []byte, lo lineOffsets) protocol.Position {
	line := p.Line - 1
	col := p.Column - 1

	if line < 0 {
		line = 0
		col = 0
	}

	if line >= len(lo) {
		line = len(lo) - 1
		col = runeLen(lineBytes(src, lo, line))
	}

	lb := lineBytes(src, lo, line)

	if lineRunes := runeLen(lb); col > lineRunes {
		col = lineRunes
	}

	ch := runeColToUTF16(lb, col)
	return protocol.Position{
		Line:      uint32(line),
		Character: ch,
	}
}

// astSpanToLSP converts an ast.Span to an LSP Range using the provided source
// bytes and pre-built line offset table.
func astSpanToLSP(s ast.Span, src []byte, lo lineOffsets) protocol.Range {
	return protocol.Range{
		Start: astPositionToLSP(s.Start, src, lo),
		End:   astPositionToLSP(s.End, src, lo),
	}
}

// lineBytes returns the bytes of the given 0-based line from src using the
// precomputed offset table. It does not include the trailing '\n'.
func lineBytes(src []byte, lo lineOffsets, line int) []byte {
	if line < 0 || line >= len(lo) {
		return nil
	}
	start := lo[line]
	var end int
	if line+1 < len(lo) {
		end = lo[line+1] - 1
		if end > len(src) {
			end = len(src)
		}
		if end > start && src[end-1] == '\r' {
			end--
		}
	} else {
		end = len(src)
	}
	if start > end {
		return nil
	}
	return src[start:end]
}

// runeLen returns the number of runes (possibly including replacement runes for
// invalid UTF-8) in b.
func runeLen(b []byte) int {
	n := 0
	for len(b) > 0 {
		_, size := utf8.DecodeRune(b)
		b = b[size:]
		n++
	}
	return n
}

// runeColToUTF16 converts a 0-based rune column index into a UTF-16 code-unit
// count by scanning line from the start. Runes in the Basic Multilingual Plane
// (U+0000–U+FFFF) contribute 1 unit; supplementary-plane runes (U+10000+)
// contribute 2 units (surrogate pair). Invalid UTF-8 bytes each contribute 1
// unit, matching gopls.
func runeColToUTF16(line []byte, col int) uint32 {
	var units uint32
	for col > 0 {
		r, size := utf8.DecodeRune(line)
		if r == utf8.RuneError && size == 1 {
			// invalid byte — 1 UTF-16 unit
			units++
		} else if r >= 0x10000 {
			// supplementary plane — surrogate pair
			units += 2
		} else {
			units++
		}
		line = line[size:]
		col--
	}
	return units
}
