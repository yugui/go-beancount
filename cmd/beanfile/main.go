// Command beanfile distributes beancount directives from input sources
// (stdin or one or more positional files) into a tree of per-account and
// per-commodity destination files under a chosen root, following the
// standard convention from the beanfile design doc (§2). Routing rules,
// account and commodity overrides, and output formatting are configured
// via a TOML file and overlaid by command-line flags.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/dedup"
	"github.com/yugui/go-beancount/pkg/distribute/merge"
	"github.com/yugui/go-beancount/pkg/distribute/route"
	"github.com/yugui/go-beancount/pkg/distribute/route/routeconfig"
	"github.com/yugui/go-beancount/pkg/printer"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// defaultConfigFile is the auto-discovery path searched when --config is
// not given; it is interpreted relative to the current working directory.
const defaultConfigFile = "beanfile.toml"

// cfg carries everything execute needs after flag parsing and config
// loading: the absolute ledger and destination root, the boolean
// behavior switches, and the resolved route.Config (TOML + flag overlay).
type cfg struct {
	ledgerAbs   string
	passThrough bool
	quiet       bool
	route       *route.Config
}

// scanConfigPath looks for --config / -config in args and returns the
// path explicitly set on the command line. The last occurrence wins,
// matching stdlib flag's behaviour. ok is true only when the flag was
// found (the empty string itself is a valid explicit path).
//
// A pre-scan for --config is needed because flag overlays mutate the
// route.Config in their callbacks; the loaded config must be in place
// before flag.Parse runs.
func scanConfigPath(args []string) (path string, ok bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		for _, prefix := range []string{"--config=", "-config="} {
			if strings.HasPrefix(a, prefix) {
				path = strings.TrimPrefix(a, prefix)
				ok = true
			}
		}
		if a == "--config" || a == "-config" {
			if i+1 < len(args) {
				path = args[i+1]
				ok = true
				i++
			}
		}
	}
	return path, ok
}

// parseFlags parses args, validates --ledger, resolves --ledger and
// --root to absolute paths, and loads the routing config. On user error
// (bad flag, missing --ledger, bad TOML) it prints to stderr and returns
// a nil cfg with the intended exit code (2). On -h/--help it returns
// (nil, nil, 0). On success it returns the populated cfg, the list of
// positional source paths, and 0.
//
// Two-pass parsing: a pre-scan locates --config so the TOML can be loaded
// before flag.Parse runs; each --order / --file-pattern / --txn-strategy
// / --override-meta-key / --format-* flag is then a flag.Func or
// flag.BoolFunc that mutates the loaded route.Config in place. Each
// flag's effect lives in one place — its callback — so adding a new flag
// is one block of code, not three sites.
func parseFlags(args []string, stderr io.Writer) (*cfg, []string, int) {
	c := &cfg{}
	var ledgerArg, rootArg string

	configPathFromArgs, configExplicit := scanConfigPath(args)

	var rcfg *route.Config
	if configExplicit {
		loaded, err := routeconfig.Load(configPathFromArgs)
		if err != nil {
			fmt.Fprintf(stderr, "beanfile: %v\n", err)
			return nil, nil, 2
		}
		rcfg = loaded
	} else {
		loaded, err := routeconfig.LoadIfExists(defaultConfigFile)
		if err != nil {
			fmt.Fprintf(stderr, "beanfile: %v\n", err)
			return nil, nil, 2
		}
		rcfg = loaded
	}
	if rcfg == nil {
		rcfg = &route.Config{}
	}

	cmd := flag.NewFlagSet("beanfile", flag.ContinueOnError)
	cmd.SetOutput(stderr)

	cmd.StringVar(&ledgerArg, "ledger", "", "root ledger file (required)")
	cmd.String("config", "", "TOML config (default: ./beanfile.toml if present)")
	cmd.StringVar(&rootArg, "root", "", "destination root directory (default: directory of --ledger)")
	cmd.BoolVar(&c.passThrough, "pass-through", false, "emit non-routable directives to stdout instead of erroring")
	cmd.BoolVar(&c.quiet, "quiet", false, "suppress per-file and total stats on stderr")

	cmd.Func("order", "ascending | descending | append", func(s string) error {
		rcfg.Routes.Account.Order = s
		rcfg.Routes.Price.Order = s
		return nil
	})
	cmd.Func("file-pattern", "YYYY | YYYYmm | YYYYmmdd", func(s string) error {
		rcfg.Routes.Account.FilePattern = s
		rcfg.Routes.Price.FilePattern = s
		return nil
	})
	cmd.Func("txn-strategy", "first-posting | last-posting | first-debit | first-credit", func(s string) error {
		rcfg.Routes.Transaction.DefaultStrategy = s
		return nil
	})
	cmd.Func("override-meta-key", "metadata key (default: route-account)", func(s string) error {
		rcfg.Routes.Transaction.OverrideMetaKey = s
		return nil
	})

	cmd.BoolFunc("format-comma-grouping", "insert thousands separators in numbers", func(s string) error {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.CommaGrouping = &v
		return nil
	})
	cmd.BoolFunc("format-align-amounts", "column-align posting amounts", func(s string) error {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.AlignAmounts = &v
		return nil
	})
	cmd.Func("format-amount-column", "right-edge column for amounts", func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.AmountColumn = &n
		return nil
	})
	cmd.Func("format-east-asian-ambiguous-width", "EA Ambiguous char width: 1 or 2", func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.EastAsianAmbiguousWidth = &n
		return nil
	})
	cmd.Func("format-indent-width", "spaces per indent level", func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.IndentWidth = &n
		return nil
	})
	cmd.Func("format-blank-lines-between-directives", "target blank lines between directives", func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.BlankLinesBetweenDirectives = &n
		return nil
	})
	cmd.BoolFunc("format-insert-blank-lines-between-directives", "actively insert blank lines between directives", func(s string) error {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		rcfg.Routes.Format.InsertBlankLinesBetweenDirectives = &v
		return nil
	})

	cmd.Usage = func() { printUsage(stderr, cmd) }
	if err := cmd.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, 0
		}
		return nil, nil, 2
	}

	if ledgerArg == "" {
		fmt.Fprintln(stderr, "beanfile: --ledger is required")
		return nil, nil, 2
	}
	if _, err := os.Stat(ledgerArg); err != nil {
		fmt.Fprintf(stderr, "beanfile: %v\n", err)
		return nil, nil, 2
	}
	abs, err := filepath.Abs(ledgerArg)
	if err != nil {
		fmt.Fprintf(stderr, "beanfile: resolving ledger %q: %v\n", ledgerArg, err)
		return nil, nil, 2
	}
	c.ledgerAbs = abs

	if rootArg == "" {
		rootArg = filepath.Dir(ledgerArg)
	}
	rootAbs, err := filepath.Abs(rootArg)
	if err != nil {
		fmt.Fprintf(stderr, "beanfile: resolving root %q: %v\n", rootArg, err)
		return nil, nil, 2
	}

	rcfg.Root = rootAbs
	c.route = rcfg

	return c, cmd.Args(), 0
}

func printUsage(w io.Writer, cmd *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: beanfile [flags] --ledger ROOT.beancount [files...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Distribute beancount directives from stdin or positional files into")
	fmt.Fprintln(w, "per-account and per-commodity destination files under the directory of")
	fmt.Fprintln(w, "--ledger (or --root if set).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	cmd.PrintDefaults()
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c, positional, exit := parseFlags(args, stderr)
	if c == nil {
		return exit
	}
	return execute(ctx, c, sourceReaders(positional, stdin), stdout, stderr)
}

// execute is the core orchestration: build the dedup index, dispatch
// each source through the routing/three-way-decision loop, merge the
// resulting plans into their destinations, and emit stats.
func execute(ctx context.Context, c *cfg, sources iter.Seq2[*inputSource, error], stdout, stderr io.Writer) int {
	index, ledgerDiags, err := dedup.BuildIndex(
		ctx,
		c.ledgerAbs,
		c.route.Root,
		dedup.WithOverrideMetaKey(c.route.Routes.Transaction.OverrideMetaKey),
	)
	if err != nil {
		fmt.Fprintf(stderr, "beanfile: %v\n", err)
		return 2
	}
	if emitDiagnostics(stderr, ledgerDiags, c.quiet) {
		return 1
	}

	planByPath := map[string][]merge.Insert{}
	spacingByPath := map[string]planSpacing{}
	writtenByPath := map[string]int{}
	commentedByPath := map[string]int{}
	skippedByPath := map[string]int{}
	var passthroughCount int

	for src, err := range sources {
		if err != nil {
			fmt.Fprintf(stderr, "beanfile: %v\n", err)
			return 1
		}
		ledger, err := loadSource(src)
		if err != nil {
			fmt.Fprintf(stderr, "beanfile: %v\n", err)
			return 1
		}
		if emitDiagnostics(stderr, ledger.Diagnostics, c.quiet) {
			return 1
		}
		// Iterate ledger.All so transitively included directives surface
		// here while the Include directives themselves (already consumed
		// by the loader) do not.
		for _, d := range ledger.All() {
			decision, err := route.Decide(d, c.route)
			if err != nil {
				fmt.Fprintf(stderr, "beanfile: route: %v\n", err)
				return 1
			}
			if decision.PassThrough {
				if !c.passThrough {
					pos := d.DirSpan().Start
					if pos.Filename != "" {
						fmt.Fprintf(stderr, "beanfile: %s:%d:%d: non-routable directive (%s) without --pass-through\n",
							pos.Filename, pos.Line, pos.Column, directiveKindName(d))
					} else {
						fmt.Fprintf(stderr, "beanfile: non-routable directive (%s) without --pass-through\n", directiveKindName(d))
					}
					return 2
				}
				if err := printer.Fprint(stdout, d); err != nil {
					fmt.Fprintf(stderr, "beanfile: writing pass-through: %v\n", err)
					return 1
				}
				passthroughCount++
				continue
			}

			if matched, _ := index.InDestination(decision.Path, d, decision.EqMetaKeys); matched {
				skippedByPath[decision.Path]++
				continue
			}
			commented, _ := index.InOtherActive(decision.Path, d, decision.EqMetaKeys)
			planByPath[decision.Path] = append(planByPath[decision.Path], merge.Insert{
				Directive: d,
				Commented: commented,
				Format:    decision.Format,
			})
			spacingByPath[decision.Path] = planSpacing{
				blankLines:       decision.BlankLinesBetweenDirectives,
				insertBlankLines: decision.InsertBlankLinesBetweenDirectives,
			}
			if commented {
				commentedByPath[decision.Path]++
			} else {
				writtenByPath[decision.Path]++
			}
			index.Add(decision.Path, d, commented)
		}
	}

	pathSet := map[string]struct{}{}
	for p := range planByPath {
		pathSet[p] = struct{}{}
	}
	for p := range skippedByPath {
		pathSet[p] = struct{}{}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var stats []pathStat
	mergeFailed := false
	for _, p := range paths {
		inserts := planByPath[p]
		if len(inserts) > 0 {
			sp := spacingByPath[p]
			plan := merge.Plan{
				Path:                              filepath.Join(c.route.Root, p),
				Order:                             route.OrderAscending,
				BlankLinesBetweenDirectives:       sp.blankLines,
				InsertBlankLinesBetweenDirectives: sp.insertBlankLines,
				Inserts:                           inserts,
			}
			if _, err := merge.Merge(plan, merge.Options{}); err != nil {
				fmt.Fprintf(stderr, "beanfile: merge %s: %v\n", p, err)
				mergeFailed = true
				continue
			}
		}
		stats = append(stats, pathStat{
			relPath:   p,
			written:   writtenByPath[p],
			commented: commentedByPath[p],
			skipped:   skippedByPath[p],
		})
	}

	if !c.quiet {
		writeStats(stderr, stats, passthroughCount)
	}

	if mergeFailed {
		return 1
	}
	return 0
}

// planSpacing carries the resolved spacing fields for one destination
// file. The values come from route.Decide via Decision's BlankLines*
// fields and feed merge.Plan's spacing knobs unchanged.
type planSpacing struct {
	blankLines       int
	insertBlankLines bool
}

// inputSource is one element of the input stream: a named reader. The
// name is "-" for stdin or the absolute path for a positional file; it
// flows into ast.LoadReader's WithFilename so diagnostics from the
// input parse name the source they came from.
type inputSource struct {
	name string
	r    io.ReadCloser
}

// sourceReaders yields one inputSource per CLI input source in
// argument order.
func sourceReaders(positional []string, stdin io.Reader) iter.Seq2[*inputSource, error] {
	return func(yield func(*inputSource, error) bool) {
		if len(positional) == 0 || (len(positional) == 1 && positional[0] == "-") {
			yield(&inputSource{name: "-", r: io.NopCloser(stdin)}, nil)
			return
		}
		for _, p := range positional {
			abs, err := filepath.Abs(p)
			if err != nil {
				if !yield(nil, fmt.Errorf("resolving %q: %w", p, err)) {
					return
				}
				continue
			}
			f, err := os.Open(abs)
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}
			if !yield(&inputSource{name: abs, r: f}, nil) {
				f.Close()
				return
			}
		}
	}
}

// loadSource consumes src.r in full and returns the parsed ledger.
// Relative include directives in input are rejected via WithBaseDir("").
func loadSource(src *inputSource) (*ast.Ledger, error) {
	defer src.r.Close()
	opts := []ast.LoadOption{ast.WithBaseDir("")}
	if src.name != "-" {
		opts = append(opts, ast.WithFilename(src.name))
	}
	return ast.LoadReader(src.r, opts...)
}

// emitDiagnostics prints diags to stderr in source-position order.
// Errors are always emitted; warnings are suppressed when quiet is true.
// The returned bool is true when at least one error was present, signaling
// the caller to exit non-zero.
func emitDiagnostics(stderr io.Writer, diags []ast.Diagnostic, quiet bool) (hasError bool) {
	for _, d := range diags {
		if d.Severity == ast.Error {
			hasError = true
			break
		}
	}
	if !hasError && quiet {
		return false
	}
	sorted := sortDiagnostics(diags)
	if hasError {
		for _, d := range sorted {
			fmt.Fprintln(stderr, formatDiagnostic(d))
		}
		return true
	}
	for _, d := range sorted {
		if d.Severity == ast.Warning {
			fmt.Fprintln(stderr, formatDiagnostic(d))
		}
	}
	return false
}

func sortDiagnostics(diags []ast.Diagnostic) []ast.Diagnostic {
	sorted := make([]ast.Diagnostic, len(diags))
	copy(sorted, diags)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Span.Start, sorted[j].Span.Start
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Offset < b.Offset
	})
	return sorted
}

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

func severityString(s ast.Severity) string {
	switch s {
	case ast.Warning:
		return "warning"
	default:
		return "error"
	}
}

func directiveKindName(d ast.Directive) string {
	switch d.(type) {
	case *ast.Option:
		return "option"
	case *ast.Plugin:
		return "plugin"
	case *ast.Include:
		return "include"
	case *ast.Event:
		return "event"
	case *ast.Query:
		return "query"
	case *ast.Custom:
		return "custom"
	case *ast.Commodity:
		return "commodity"
	}
	return fmt.Sprintf("%T", d)
}

type pathStat struct {
	relPath   string
	written   int
	commented int
	skipped   int
}

func writeStats(w io.Writer, stats []pathStat, passthrough int) {
	maxPath := 0
	for _, s := range stats {
		if l := len(s.relPath); l > maxPath {
			maxPath = l
		}
	}
	totalWritten, totalCommented, totalSkipped := 0, 0, 0
	for _, s := range stats {
		fmt.Fprintf(w, "beanfile: %-*s written=%d commented=%d skipped=%d\n",
			maxPath+1, s.relPath+":", s.written, s.commented, s.skipped)
		totalWritten += s.written
		totalCommented += s.commented
		totalSkipped += s.skipped
	}
	fmt.Fprintf(w, "beanfile: total: written=%d commented=%d skipped=%d passthrough=%d\n",
		totalWritten, totalCommented, totalSkipped, passthrough)
}
