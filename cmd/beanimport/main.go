// Command beanimport drives the pkg/importer + pkg/importer/hook
// pipeline against a single input file. See -help for usage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/ext/goplug/goplugflag"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/hook"
	"github.com/yugui/go-beancount/pkg/printer"

	_ "github.com/yugui/go-beancount/pkg/importer/hook/std/classify"
	_ "github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
	_ "github.com/yugui/go-beancount/pkg/importer/std/csvimp"
	_ "github.com/yugui/go-beancount/pkg/importer/std/csvsexp"
)

// commaSlice accumulates repeated flag occurrences and splits each value
// on commas, discarding empty segments. Both -hook A,B and -hook A -hook B
// compose into the same slice.
type commaSlice []string

func (s *commaSlice) String() string { return strings.Join(*s, ",") }
func (s *commaSlice) Set(v string) error {
	for _, seg := range strings.Split(v, ",") {
		if seg = strings.TrimSpace(seg); seg != "" {
			*s = append(*s, seg)
		}
	}
	return nil
}

// errFlagValidation signals that flag parsing succeeded but a post-parse
// validation check failed; parseFlags has already written the diagnostic
// message to stderr.
var errFlagValidation = errors.New("flag validation failed")

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

type runOptions struct {
	config    string
	hooks     []string
	importer  string
	account   string
	plugins   []string
	strict    bool
	inputPath string
}

// run is the testable entry point. args is os.Args[1:]; stdout and stderr
// receive the binary's two output streams. The return value is the process
// exit code per the exit-code table in the package doc. run does not call
// os.Exit; main does.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if err := goplug.LoadAll(opts.plugins); err != nil {
		fmt.Fprintf(stderr, "beanimport: %v\n", err)
		return 2
	}

	f, err := os.Open(opts.config)
	if err != nil {
		fmt.Fprintf(stderr, "beanimport: config %s: open: %v\n", opts.config, err)
		return 2
	}
	importers, hooks, loadErr := loadConfig(f, opts.config)
	f.Close()
	if loadErr != nil {
		fmt.Fprintln(stderr, loadErr)
		return 2
	}

	impReg, err := importer.NewRegistry(importers)
	if err != nil {
		fmt.Fprintf(stderr, "beanimport: %v\n", err)
		return 2
	}

	selected, err := selectHooks(hooks, opts.hooks)
	if err != nil {
		fmt.Fprintf(stderr, "beanimport: %v\n", err)
		return 2
	}
	filteredReg, err := hook.NewRegistry(selected)
	if err != nil {
		fmt.Fprintf(stderr, "beanimport: %v\n", err)
		return 2
	}

	fh, err := os.Open(opts.inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "beanimport: %v\n", err)
		return 2
	}
	fh.Close()

	in := importer.Input{
		Path: opts.inputPath,
		Opener: func() (io.ReadCloser, error) {
			return os.Open(opts.inputPath)
		},
	}
	if opts.account != "" {
		in.Hints = map[string]string{"account": opts.account}
	}

	var (
		out            importer.Output
		extractErr     error
		identifyForced bool
	)
	if opts.importer != "" {
		imp, ok := impReg.Lookup(opts.importer)
		if !ok {
			fmt.Fprintf(stderr, "beanimport: unknown importer %q\n", opts.importer)
			return 2
		}
		identifyForced = !imp.Identify(ctx, in)
		out, extractErr = imp.Extract(ctx, in)
	} else {
		out, extractErr = importer.Apply(ctx, impReg, in)
	}

	result, chainErr := hook.Chain(ctx, filteredReg, hook.HookInput{
		Directives: out.Directives,
		Hints:      in.Hints,
	})

	if err := printer.Fprint(stdout, result.Directives); err != nil {
		fmt.Fprintf(stderr, "beanimport: writing stdout: %v\n", err)
		return 1
	}

	var composed []ast.Diagnostic
	composed = append(composed, out.Diagnostics...)
	if identifyForced {
		composed = append(composed, ast.Diagnostic{
			Code:     codeIdentifyForced,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"importer %q: Identify returned false; extracting anyway because -importer was set",
				opts.importer),
			Span: ast.Span{Start: ast.Position{Filename: in.Path}},
		})
	}
	composed = append(composed, result.Diagnostics...)
	if extractErr != nil && ctx.Err() == nil {
		composed = append(composed, ast.Diagnostic{
			Code:     codeExtract,
			Severity: ast.Error,
			Message:  extractErr.Error(),
			Span:     ast.Span{Start: ast.Position{Filename: in.Path}},
		})
	}
	if chainErr != nil && ctx.Err() == nil {
		composed = append(composed, ast.Diagnostic{
			Code:     codeHook,
			Severity: ast.Error,
			Message:  chainErr.Error(),
			Span:     ast.Span{Start: ast.Position{Filename: in.Path}},
		})
	}
	if ctx.Err() != nil {
		composed = append(composed, ast.Diagnostic{
			Code:     codeCancelled,
			Severity: ast.Error,
			Message:  ctx.Err().Error(),
		})
	}

	printDiagnostics(stderr, composed)
	return exitCode(composed, opts.strict)
}

// exitCode maps the composed diagnostic stream and strict flag to the
// process exit code: 1 when any Error is present (or any Warning under
// strict), otherwise 0.
func exitCode(diags []ast.Diagnostic, strict bool) int {
	hasError, hasWarning := false, false
	for _, d := range diags {
		switch d.Severity {
		case ast.Error:
			hasError = true
		case ast.Warning:
			hasWarning = true
		}
	}
	if hasError || (strict && hasWarning) {
		return 1
	}
	return 0
}

// parseFlags parses args into a runOptions. Returns flag.ErrHelp when
// -h was handled cleanly, errFlagValidation when a required flag or
// positional argument is missing (a diagnostic message has been written
// to stderr), or the underlying flag-parse error otherwise.
func parseFlags(args []string, stderr io.Writer) (*runOptions, error) {
	opts := &runOptions{}
	var hooks commaSlice

	cmd := flag.NewFlagSet("beanimport", flag.ContinueOnError)
	cmd.SetOutput(stderr)
	cmd.StringVar(&opts.config, "config", "", "TOML config file path (required)")
	cmd.Var(&hooks, "hook", "hook instance name(s), comma- or repeat-separated, in chain order")
	cmd.StringVar(&opts.importer, "importer", "", "force a specific importer instance (bypass Dispatch)")
	cmd.StringVar(&opts.account, "account", "", `account hint passed to Extract via Hints["account"]`)
	plugins := goplugflag.Var(cmd)
	cmd.BoolVar(&opts.strict, "strict", false, "treat warnings as errors (exit 1 on any Warning)")
	cmd.Usage = func() { printUsage(stderr, cmd) }

	if err := cmd.Parse(args); err != nil {
		return nil, err
	}

	if opts.config == "" {
		fmt.Fprintln(stderr, "beanimport: -config is required")
		return nil, errFlagValidation
	}

	positionals := cmd.Args()
	if len(positionals) != 1 {
		fmt.Fprintln(stderr, "beanimport: exactly one input file required")
		return nil, errFlagValidation
	}

	opts.hooks = []string(hooks)
	opts.plugins = *plugins
	opts.inputPath = positionals[0]
	return opts, nil
}

func printUsage(w io.Writer, cmd *flag.FlagSet) {
	fmt.Fprint(w, `Usage: beanimport [flags] <input-file>

Drive the pkg/importer + pkg/importer/hook pipeline against a single input
file. Load importers and hooks from a flat [[importer]] / [[hook]] TOML
config. Print beancount directives to stdout; diagnostics to stderr in the
canonical <path>:<line>:<col>: <severity>: <message> form.

Flags:
`)
	cmd.PrintDefaults()
	fmt.Fprint(w, `
PLUGINS
  Out-of-tree importers and hooks load from -plugin PATH (repeatable) and from
  the BEANCOUNT_PLUGINS environment variable (a path-list-separated list, like
  PATH). A path that does not load is a CLI failure (exit 2).

EXIT CODES
  0  pipeline completed; no Error diagnostics
     (Warnings are allowed unless -strict.)
  1  at least one Error diagnostic, OR (-strict AND at least one Warning)
     Extract/Chain system errors are also promoted to Error diagnostics.
  2  CLI failure: missing -config, bad flags, config load failure,
     plugin load failure, unknown -hook or -importer name,
     positional-argument-count mismatch, input-file open failure,
     instance-registry construction failure.

EXAMPLE CONFIG (config.toml)
  [[importer]]
  kind = "csv"
  name = "boa_checking"

    [importer.date]
    col    = "Date"
    format = "2006-01-02"

    [importer.account]
    default = "Assets:BOA:Checking"

    [importer.currency]
    default = "USD"

    [[importer.amount]]
    col    = "Withdrawal"
    negate = true

    [[importer.amount]]
    col    = "Deposit"

  [[hook]]
  kind = "classify"
  name = "default"

    [[hook.rule]]
    payee_regex = "(?i)acme"
    account     = "Expenses:Office"

EXAMPLES
  beanimport -config config.toml statement.csv
      import statement.csv using all importers/hooks in config.toml
  beanimport -config config.toml -hook classify statement.csv
      run only the 'classify' hook
  beanimport -config config.toml -importer boa_checking statement.csv
      force the 'boa_checking' importer (bypass Dispatch)
  beanimport -config config.toml -account Assets:Checking statement.csv
      override the account hint
`)
}
