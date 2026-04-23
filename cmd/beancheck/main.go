// Command beancheck parses a beancount ledger and reports any errors.
//
// Usage:
//
//	beancheck [flags] file ...
//
// beancheck loads each file (resolving include directives), runs the
// configured plugin and validation pipeline, and writes any diagnostics
// to standard error.
//
// Exit status:
//
//	0  no diagnostics
//	1  one or more ledger errors were reported
//	2  beancheck itself could not run (bad usage, unreadable input file)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
)

var werror = flag.Bool("Werror", false, "treat warnings as errors")

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "Usage: beancheck [flags] file ...")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Parses each beancount file, runs the plugin and validation")
	fmt.Fprintln(out, "pipeline, and reports any errors.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Exit status: 0 clean, 1 ledger errors found, 2 beancheck failed.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Flags:")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		usage()
		os.Exit(2)
	}

	hasErrors, err := run(context.Background(), os.Stderr, files, *werror)
	if err != nil {
		fmt.Fprintf(os.Stderr, "beancheck: %v\n", err)
		os.Exit(2)
	}
	if hasErrors {
		os.Exit(1)
	}
}

// run loads each file and writes ledger diagnostics to w. Warnings are
// always printed; they only count toward the returned "has errors" flag
// when werror is true. A non-nil error means beancheck itself could not
// run — e.g. an input file is unreadable — and the caller should exit
// with status 2 rather than the status-1 "errors found" code.
func run(ctx context.Context, w io.Writer, files []string, werror bool) (bool, error) {
	hasErrors := false
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			return hasErrors, err
		}

		ledger, errs, err := loader.Load(ctx, path)
		if err != nil {
			return hasErrors, fmt.Errorf("%s: %w", path, err)
		}

		for _, d := range ledger.Diagnostics {
			fmt.Fprintln(w, formatDiagnostic(d))
			switch d.Severity {
			case ast.Error:
				hasErrors = true
			case ast.Warning:
				if werror {
					hasErrors = true
				}
			}
		}
		for _, e := range errs {
			fmt.Fprintln(w, e.Error())
			hasErrors = true
		}
	}
	return hasErrors, nil
}

// formatDiagnostic renders an AST diagnostic with its source location when
// available. Warnings are labeled so they can be visually distinguished
// from errors, which are rendered in the same shape as validation and
// plugin errors.
func formatDiagnostic(d ast.Diagnostic) string {
	msg := d.Message
	if d.Severity == ast.Warning {
		msg = "warning: " + msg
	}
	pos := d.Span.Start
	if pos.Filename != "" {
		return fmt.Sprintf("%s:%d:%d: %s", pos.Filename, pos.Line, pos.Column, msg)
	}
	return msg
}
