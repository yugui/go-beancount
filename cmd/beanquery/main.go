// Command beanquery runs a Beancount Query Language (BQL) query over a
// loaded ledger and writes the result to stdout in the format selected
// by -format (text, the default aligned table; csv; or json).
//
// Usage:
//
//	beanquery [flags] <ledger-file> <query>
//
// It loads <ledger-file> through pkg/loader, reports any ledger
// diagnostics to stderr in the canonical
// "<path>:<line>:<col>: <severity>: <message>" form, then — if the
// ledger has no Error-severity diagnostics — compiles and runs <query>
// through pkg/query and writes the result to stdout. The query is
// a single positional argument, so it must be quoted on the shell.
//
// beanquery is glue only: all loading, compilation, and evaluation live
// in pkg/loader and pkg/query. The built-in query functions are
// activated by the blank import of pkg/query/env/std below; without it
// the engine has no registered functions and any sum(...)/year(...)
// query fails to compile.
//
// Out-of-tree query functions and ledger post-processors are loaded from
// goplug .so files named by the repeatable -plugin flag and by the
// BEANCOUNT_PLUGINS environment variable (a path-list-separated list, like
// PATH). A plugin's InitPlugin registers query functions via
// pkg/query/env.Register so that queries naming them compile, and/or
// post-processors via pkg/ext/postproc.Register so that plugin directives
// naming them resolve.
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
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/ext/goplug/goplugflag"
	"github.com/yugui/go-beancount/pkg/loader"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"

	// Register the built-in ledger post-processors so that plugin
	// directives in the loaded ledger resolve while loader.LoadFile runs
	// the booking/validation pipeline. std is the ported beancount plugin
	// library; sprout is the ported beansprout plugin library.
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/sprout"
	_ "github.com/yugui/go-beancount/pkg/ext/postproc/std"

	// Activate the built-in BQL query functions; the engine registers
	// none on its own, so these blank imports are load-bearing. std is
	// the beanquery-parity library, sprout the non-standard extensions.
	_ "github.com/yugui/go-beancount/pkg/query/env/sprout"
	_ "github.com/yugui/go-beancount/pkg/query/env/std"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point; args is os.Args[1:]. It returns a
// process exit code:
//
//	0  success: the query compiled, ran, and its result was written to
//	   stdout in the selected format (a zero-row result still prints its
//	   header).
//	1  invalid ledger content (at least one Error-severity diagnostic,
//	   in which case the query is not run) OR a query parse/compile/run
//	   error.
//	2  CLI failure: bad flags (including an unknown -format value), the
//	   wrong number of positional arguments, a ledger file that could not
//	   be loaded (missing/unreadable, I/O error, context cancellation), or
//	   a goplug plugin that failed to load.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd := flag.NewFlagSet("beanquery", flag.ContinueOnError)
	cmd.SetOutput(stderr)
	cmd.Usage = func() { printUsage(stderr, cmd) }
	pluginPaths := goplugflag.Var(cmd)
	format := cmd.String("format", "text", "output format: text, csv, or json")
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

	f, err := formatterFor(*format)
	if err != nil {
		fmt.Fprintf(stderr, "beanquery: %v\n", err)
		return 2
	}

	// loader.LoadFile reports a missing or unreadable top-level file as
	// an Error diagnostic on a non-nil ledger rather than via its error
	// return, which would otherwise land in the exit-1 (invalid content)
	// branch below. A file that is not even openable is a CLI failure,
	// so map it to exit 2 with a pre-flight check.
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "beanquery: %v\n", err)
		return 2
	}

	// Register out-of-tree query functions and post-processors before the
	// ledger loads and the query compiles. A load failure is a setup
	// failure, not invalid ledger content, so it maps to exit 2.
	if err := goplug.LoadAll(*pluginPaths); err != nil {
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

	if err := f.Format(stdout, result); err != nil {
		fmt.Fprintf(stderr, "beanquery: %v\n", err)
		return 1
	}
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
	fmt.Fprintln(w, "Run a BQL query over a beancount ledger and write the result to stdout.")
	fmt.Fprintln(w, "Use -format to choose text (default, aligned table), csv, or json.")
	fmt.Fprintln(w, "The query is a single positional argument, so quote it on the shell.")
	fmt.Fprintln(w, "Ledger diagnostics go to stderr.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Out-of-tree query functions and post-processors load from -plugin PATH")
	fmt.Fprintln(w, "(repeatable) and the BEANCOUNT_PLUGINS environment variable (a")
	fmt.Fprintln(w, "path-list-separated list).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	cmd.PrintDefaults()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXIT CODES")
	fmt.Fprintln(w, "  0  success; the result was written to stdout in the selected format")
	fmt.Fprintln(w, "  1  the ledger has Error-severity diagnostics (query not run),")
	fmt.Fprintln(w, "     OR the query failed to parse/compile/run")
	fmt.Fprintln(w, "  2  CLI failure: bad flags (including an unknown -format value),")
	fmt.Fprintln(w, "     wrong argument count, or the ledger file could not be loaded")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXAMPLES")
	fmt.Fprintln(w, "  beanquery my.beancount \\")
	fmt.Fprintln(w, "    'SELECT account, sum(number) AS total GROUP BY account ORDER BY account'")
	fmt.Fprintln(w, "  beanquery my.beancount 'SELECT date, narration, tags WHERE year(date) = 2024'")
	fmt.Fprintln(w, "  beanquery -format json my.beancount 'SELECT account, sum(number) AS total GROUP BY account'")
}
