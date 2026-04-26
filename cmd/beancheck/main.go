// Command beancheck loads a beancount ledger through the full processing
// pipeline (pad, balance, validations, plugins) and reports every diagnostic
// it finds.
//
// Usage:
//
//	beancheck [flags] <file>
//
// beancheck exits 0 when the ledger is clean, 1 when the ledger has errors
// (or warnings under -strict), and 2 when beancheck itself cannot run (bad
// arguments, IO failure outside the ledger, flag parse error).
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

var strict = flag.Bool("strict", false, "treat warnings as errors")

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "Usage: beancheck [flags] <file>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Loads the beancount ledger rooted at <file>, runs the full plugin")
	fmt.Fprintln(out, "pipeline (pad, balance, validations, ...), and reports every diagnostic.")
	fmt.Fprintln(out, "Exits 1 if the ledger has errors, 2 if the checker itself could not run,")
	fmt.Fprintln(out, "0 otherwise.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Flags:")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	// flag.ExitOnError already maps parse failures to exit 2, matching our
	// "checker meta-failure" convention.
	flag.Parse()
	os.Exit(run(context.Background(), flag.Args(), *strict, os.Stderr))
}

// run is the testable entry point. It returns the process exit code.
func run(ctx context.Context, args []string, strict bool, stderr io.Writer) int {
	switch len(args) {
	case 0:
		// TODO: wire stdin to loader.LoadReader. The entry point now
		// exists, but stdin handling and base-directory configuration
		// are deferred to a follow-up so behavior stays explicit.
		fmt.Fprintln(stderr, "beancheck: no file argument (stdin not yet supported)")
		fmt.Fprintln(stderr, "Usage: beancheck [flags] <file>")
		return 2
	case 1:
		return check(ctx, args[0], strict, stderr)
	default:
		fmt.Fprintln(stderr, "beancheck: expected exactly one file argument")
		fmt.Fprintln(stderr, "Usage: beancheck [flags] <file>")
		return 2
	}
}

// check loads filename and reports diagnostics. A non-nil loader error is a
// checker meta-failure (exit 2); all ledger content problems surface as
// diagnostics in ledger.Diagnostics and exit 1.
func check(ctx context.Context, filename string, strict bool, stderr io.Writer) int {
	ledger, err := loader.LoadFile(ctx, filename)
	if err != nil {
		fmt.Fprintf(stderr, "beancheck: %v\n", err)
		return 2
	}
	return report(stderr, ledger.Diagnostics, strict)
}

// report writes each diagnostic to w and returns the exit code per the
// exit-code table documented in the package doc. It is a pure function of its
// inputs so tests can drive it directly without invoking the loader.
func report(w io.Writer, diags []ast.Diagnostic, strict bool) int {
	hasError := false
	hasWarning := false

	for _, d := range diags {
		fmt.Fprintln(w, formatDiagnostic(d))
		switch d.Severity {
		case ast.Error:
			hasError = true
		case ast.Warning:
			hasWarning = true
		}
	}

	switch {
	case hasError:
		return 1
	case hasWarning && strict:
		return 1
	default:
		return 0
	}
}

// formatDiagnostic renders an ast.Diagnostic in the canonical greppable form:
// "<path>:<line>:<col>: <severity>: <message>", or "<severity>: <message>"
// when the span has no filename. When Code is non-empty it is appended in
// brackets so callers can grep on the machine-readable classifier.
func formatDiagnostic(d ast.Diagnostic) string {
	sev := severityString(d.Severity)
	msg := d.Message
	if d.Code != "" {
		msg = fmt.Sprintf("%s [%s]", msg, d.Code)
	}
	pos := d.Span.Start
	if pos.Filename == "" {
		return fmt.Sprintf("%s: %s", sev, msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s", pos.Filename, pos.Line, pos.Column, sev, msg)
}

// severityString maps ast.Severity to its lowercase label.
func severityString(s ast.Severity) string {
	switch s {
	case ast.Warning:
		return "warning"
	default:
		return "error"
	}
}
