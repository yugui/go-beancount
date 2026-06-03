package csvkit

import (
	"bufio"
	"encoding/csv"
	"errors"
	"io"
	"iter"
	"strings"

	"golang.org/x/text/encoding"
)

// Reader reads CSV/TSV records from a byte stream. It decodes an optional
// source Encoding to UTF-8, strips a leading UTF-8 byte-order mark, skips
// a fixed number of banner lines, and parses the remaining rows. The zero
// value reads comma-delimited UTF-8 with no banner lines.
//
// A Reader holds no state across calls; the same value is safe for
// concurrent use once constructed.
type Reader struct {
	// Delimiter is the field separator. The zero value selects ','.
	Delimiter rune

	// Encoding decodes source bytes to UTF-8 before parsing. A nil
	// Encoding passes bytes through unchanged (the UTF-8 / ASCII-compatible
	// path).
	Encoding encoding.Encoding

	// LazyQuotes relaxes quote handling, mirroring encoding/csv's option
	// of the same name.
	LazyQuotes bool

	// SkipLines is the count of raw banner lines preceding the header.
	SkipLines int
}

// Record is one parsed data row together with its 1-based source line,
// counted from the start of the undecoded stream (banner lines included).
type Record struct {
	Fields []string
	Line   int
}

// Records parses rc and returns the header row and an iterator over the
// body rows. The iterator yields (Record{}, err) for the first parse
// failure and then stops; io.EOF terminates iteration without an error.
// A header-read failure is reported as a non-nil error from Records
// itself. The caller owns rc and is responsible for closing it.
func (r *Reader) Records(rc io.Reader) (header []string, rows iter.Seq2[Record, error], err error) {
	if r.Encoding != nil {
		rc = r.Encoding.NewDecoder().Reader(rc)
	}
	br := bufio.NewReader(rc)
	if err := stripBOM(br); err != nil {
		return nil, nil, err
	}
	if err := skipRawLines(br, r.SkipLines); err != nil {
		return nil, nil, err
	}
	cr := csv.NewReader(br)
	if r.Delimiter != 0 {
		cr.Comma = r.Delimiter
	}
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = r.LazyQuotes

	header, err = cr.Read()
	if err != nil {
		return nil, nil, err
	}

	rows = func(yield func(Record, error) bool) {
		for {
			fields, err := cr.Read()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(Record{}, err)
				return
			}
			line, _ := cr.FieldPos(0)
			if !yield(Record{Fields: fields, Line: line + r.SkipLines}, nil) {
				return
			}
		}
	}
	return header, rows, nil
}

func stripBOM(br *bufio.Reader) error {
	b, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		_, _ = br.Discard(3) // cannot fail: Peek confirmed 3 buffered bytes
	}
	return nil
}

func skipRawLines(br *bufio.Reader, n int) error {
	for i := 0; i < n; i++ {
		if _, err := readLine(br); err != nil {
			return err
		}
	}
	return nil
}

// readLine reads one line up to (and including) '\n', strips the trailing
// CR/LF, and returns the body. A trailing partial line without a final
// newline is returned as success; only an EOF with no data returns io.EOF.
func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}
