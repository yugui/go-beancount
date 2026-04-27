// Command beanfmt formats beancount source files.
//
// Usage:
//
//	beanfmt [flags] [file ...]
//
// If no files are given, beanfmt reads from stdin and writes to stdout.
// If files are given, beanfmt formats each file and writes to stdout.
// With -w, beanfmt writes the result back to each source file atomically.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/yugui/go-beancount/pkg/format"
)

var (
	writeInPlace = flag.Bool("w", false, "write result to (source) file instead of stdout")
	comma        = flag.Bool("comma", false, "insert comma grouping in numbers")
	column       = flag.Int("column", 52, "amount alignment column")
	indent       = flag.Int("indent", 2, "indent width in spaces")
	blankLines   = flag.Int("blank-lines", 1, "blank lines between directives")
	eaWidth      = flag.Int("ea-width", 2, "East Asian Ambiguous character width (1 or 2)")
)

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "Usage: beanfmt [flags] [file ...]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Formats beancount source files. With no file arguments, reads from stdin.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Flags:")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "beanfmt: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	opts := []format.Option{
		format.WithCommaGrouping(*comma),
		format.WithAmountColumn(*column),
		format.WithIndentWidth(*indent),
		format.WithBlankLinesBetweenDirectives(*blankLines),
		format.WithEastAsianAmbiguousWidth(*eaWidth),
	}

	files := flag.Args()
	if len(files) == 0 {
		if *writeInPlace {
			return fmt.Errorf("-w requires at least one file argument")
		}
		return formatReader(os.Stdin, os.Stdout, opts)
	}

	for i, path := range files {
		if err := formatFile(path, *writeInPlace, len(files) > 1, i > 0, os.Stdout, opts); err != nil {
			return err
		}
	}
	return nil
}

// formatReader reads from r, formats, and writes to w.
func formatReader(r io.Reader, w io.Writer, opts []format.Option) error {
	result, err := format.FormatReader(r, opts...)
	if err != nil {
		return fmt.Errorf("formatting stdin: %w", err)
	}
	_, err = io.WriteString(w, result)
	return err
}

// formatFile formats a single file. If writeInPlace is true, the result is
// written back to the file atomically. Otherwise it is written to w.
func formatFile(path string, writeInPlace, multiFile, needSeparator bool, w io.Writer, opts []format.Option) error {
	result, err := format.FormatFile(path, opts...)
	if err != nil {
		return err
	}

	if writeInPlace {
		return atomicWrite(path, []byte(result))
	}

	if multiFile && needSeparator {
		fmt.Fprintln(w)
	}
	if multiFile {
		fmt.Fprintf(w, "==> %s <==\n", path)
	}
	_, err = io.WriteString(w, result)
	return err
}

// atomicWrite writes data to path atomically by writing to a temporary file
// in the same directory and then renaming it.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".beanfmt-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := f.Name()

	// Ensure cleanup on failure. The cleanup is best-effort: if it fails we
	// surface the leftover temp file via the logger rather than masking the
	// original failure.
	success := false
	defer func() {
		if !success {
			if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
				log.Printf("beanfmt: removing temp file %q: %v", tmpPath, err)
			}
		}
	}()

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Preserve original file permissions if possible. A chmod failure is not
	// fatal — the output is still valid — but we log it so silent permission
	// drift is observable.
	if info, err := os.Stat(path); err == nil {
		if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
			log.Printf("beanfmt: preserving permissions on %q: %v", path, err)
		}
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	success = true
	return nil
}
