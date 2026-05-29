// Command beanquery runs a Beancount Query Language (BQL) query over a
// loaded ledger and prints the result as an aligned text table.
//
// Usage:
//
//	beanquery [flags] <ledger-file> <query>
//
// It loads <ledger-file> through pkg/loader, reports any ledger
// diagnostics to stderr in the canonical
// "<path>:<line>:<col>: <severity>: <message>" form, then — if the
// ledger has no Error-severity diagnostics — compiles and runs <query>
// through pkg/query and writes the result table to stdout. The query is
// a single positional argument, so it must be quoted on the shell.
//
// beanquery is glue only: all loading, compilation, and evaluation live
// in pkg/loader and pkg/query. The built-in query functions are
// activated by the blank import of pkg/query/env/std below; without it
// the engine has no registered functions and any sum(...)/year(...)
// query fails to compile.
//
// Run "beanquery -h" for the flag set and the exit-code table.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/loader"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"

	// Activate the built-in query functions (date parts, string ops,
	// aggregators, getitem, ...). The engine registers nothing on its
	// own, so this blank import is load-bearing.
	_ "github.com/yugui/go-beancount/pkg/query/env/std"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point; args is os.Args[1:]. It returns a
// process exit code:
//
//	0  success: the query compiled, ran, and its table was written to
//	   stdout (a zero-row result still prints its header).
//	1  invalid ledger content (at least one Error-severity diagnostic,
//	   in which case the query is not run) OR a query parse/compile/run
//	   error.
//	2  CLI failure: bad flags, the wrong number of positional
//	   arguments, or a ledger file that could not be loaded
//	   (missing/unreadable, I/O error, context cancellation).
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd := flag.NewFlagSet("beanquery", flag.ContinueOnError)
	cmd.SetOutput(stderr)
	cmd.Usage = func() { printUsage(stderr, cmd) }
	if err := cmd.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if cmd.NArg() != 2 {
		fmt.Fprintf(stderr, "beanquery: expected <ledger-file> and <query>, got %d argument(s)\n", cmd.NArg())
		cmd.Usage()
		return 2
	}
	path, q := cmd.Arg(0), cmd.Arg(1)

	// loader.LoadFile reports a missing or unreadable top-level file as
	// an Error diagnostic on a non-nil ledger rather than via its error
	// return, which would otherwise land in the exit-1 (invalid content)
	// branch below. A file that is not even openable is a CLI failure,
	// so map it to exit 2 with a pre-flight check.
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "beanquery: %v\n", err)
		return 2
	}

	ledger, err := loader.LoadFile(ctx, path)
	if err != nil {
		fmt.Fprintf(stderr, "beanquery: loading %q: %v\n", path, err)
		return 2
	}

	if hasError := reportDiagnostics(stderr, ledger.Diagnostics); hasError {
		return 1
	}

	result, err := query.Query(ctx, q, ledger)
	if err != nil {
		fmt.Fprintf(stderr, "beanquery: %v\n", err)
		return 1
	}

	writeTable(stdout, result)
	return 0
}

// reportDiagnostics writes each diagnostic to w in the canonical
// greppable form (ast.Diagnostic.String) and reports whether any was
// Error severity. Warnings are printed but do not block the query.
func reportDiagnostics(w io.Writer, diags []ast.Diagnostic) (hasError bool) {
	for _, d := range diags {
		fmt.Fprintln(w, d.String())
		if d.Severity == ast.Error {
			hasError = true
		}
	}
	return hasError
}

// writeTable renders result as a space-padded text table: a header row
// of column names followed by one row per result row. Numeric columns
// (Int, Decimal, Amount) are right-aligned; all others are left-aligned.
// A zero-row result prints just the header. Column widths are measured
// in display cells via go-runewidth so wide runes line up.
func writeTable(w io.Writer, result query.Result) {
	n := len(result.Columns)
	if n == 0 {
		return
	}

	cells := make([][]string, 0, len(result.Rows)+1)
	header := make([]string, n)
	for j, c := range result.Columns {
		header[j] = c.Name
	}
	cells = append(cells, header)
	for _, row := range result.Rows {
		line := make([]string, n)
		for j, v := range row {
			line[j] = v.Format()
		}
		cells = append(cells, line)
	}

	widths := make([]int, n)
	for _, line := range cells {
		for j, s := range line {
			if wdt := runewidth.StringWidth(s); wdt > widths[j] {
				widths[j] = wdt
			}
		}
	}

	right := make([]bool, n)
	for j, c := range result.Columns {
		right[j] = isNumeric(c.Type)
	}

	var b strings.Builder
	for _, line := range cells {
		b.Reset()
		for j, s := range line {
			if j > 0 {
				b.WriteString("  ")
			}
			b.WriteString(pad(s, widths[j], right[j]))
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func isNumeric(t types.Type) bool {
	switch t {
	case types.Int, types.Decimal, types.Amount:
		return true
	default:
		return false
	}
}

// pad widens s to width display cells with spaces, on the left when
// rightAlign is set and on the right otherwise. s is returned unchanged
// when it already meets or exceeds the width.
func pad(s string, width int, rightAlign bool) string {
	gap := width - runewidth.StringWidth(s)
	if gap <= 0 {
		return s
	}
	fill := strings.Repeat(" ", gap)
	if rightAlign {
		return fill + s
	}
	return s + fill
}

func printUsage(w io.Writer, cmd *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: beanquery [flags] <ledger-file> <query>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run a BQL query over a beancount ledger and print an aligned")
	fmt.Fprintln(w, "text table to stdout. The query is a single positional argument,")
	fmt.Fprintln(w, "so quote it on the shell. Ledger diagnostics go to stderr.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	cmd.PrintDefaults()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXIT CODES")
	fmt.Fprintln(w, "  0  success; the result table was written to stdout")
	fmt.Fprintln(w, "  1  the ledger has Error-severity diagnostics (query not run),")
	fmt.Fprintln(w, "     OR the query failed to parse/compile/run")
	fmt.Fprintln(w, "  2  CLI failure: bad flags, wrong argument count, or the ledger")
	fmt.Fprintln(w, "     file could not be loaded")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXAMPLES")
	fmt.Fprintln(w, "  beanquery my.beancount \\")
	fmt.Fprintln(w, "    'SELECT account, sum(number) AS total GROUP BY account ORDER BY account'")
	fmt.Fprintln(w, "  beanquery my.beancount 'SELECT date, narration, tags WHERE year(date) = 2024'")
}
