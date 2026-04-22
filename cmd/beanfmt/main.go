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
	"os"
	"path/filepath"

	"github.com/yugui/go-beancount/pkg/format"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "beanfmt: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("beanfmt", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // We handle errors ourselves.

	writeInPlace := fs.Bool("w", false, "write result to (source) file instead of stdout")
	comma := fs.Bool("comma", false, "insert comma grouping in numbers")
	column := fs.Int("column", 52, "amount alignment column")
	indent := fs.Int("indent", 2, "indent width in spaces")
	blankLines := fs.Int("blank-lines", 1, "blank lines between directives")
	eaWidth := fs.Int("ea-width", 2, "East Asian Ambiguous character width (1 or 2)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := []format.Option{
		format.WithCommaGrouping(*comma),
		format.WithAmountColumn(*column),
		format.WithIndentWidth(*indent),
		format.WithBlankLinesBetweenDirectives(*blankLines),
		format.WithEastAsianAmbiguousWidth(*eaWidth),
	}

	files := fs.Args()

	if len(files) == 0 {
		if *writeInPlace {
			return fmt.Errorf("-w requires at least one file argument")
		}
		return formatReader(stdin, stdout, opts)
	}

	for i, path := range files {
		if err := formatFile(path, *writeInPlace, len(files) > 1, i > 0, stdout, opts); err != nil {
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

	// Ensure cleanup on failure.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Preserve original file permissions if possible.
	if info, err := os.Stat(path); err == nil {
		os.Chmod(tmpPath, info.Mode().Perm())
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	success = true
	return nil
}
