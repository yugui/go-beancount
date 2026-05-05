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

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/dedup"
	"github.com/yugui/go-beancount/pkg/distribute/merge"
	"github.com/yugui/go-beancount/pkg/distribute/route"
	routeconfig "github.com/yugui/go-beancount/pkg/distribute/route/config"
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
	rootAbs     string
	passThrough bool
	quiet       bool
	route       *route.Config
}

// parsedFlags bundles the parsed flag values together with set-ness
// information so parseFlags can overlay only those flags the user
// explicitly passed onto the TOML-derived config.
type parsedFlags struct {
	configPath string
	ledgerArg  string
	rootArg    string

	order           string
	filePattern     string
	txnStrategy     string
	overrideMetaKey string

	commaGrouping               bool
	alignAmounts                bool
	amountColumn                int
	eastAsianAmbiguousWidth     int
	indentWidth                 int
	blankLinesBetweenDirs       int
	insertBlankLinesBetweenDirs bool

	set map[string]bool
}

func newFlagSet(stderr io.Writer) (*flag.FlagSet, *parsedFlags, *cfg) {
	c := &cfg{}
	fs := &parsedFlags{set: map[string]bool{}}
	cmd := flag.NewFlagSet("beanfile", flag.ContinueOnError)
	cmd.SetOutput(stderr)

	cmd.StringVar(&fs.ledgerArg, "ledger", "", "root ledger file (required)")
	cmd.StringVar(&fs.configPath, "config", "", "TOML config (default: ./beanfile.toml if present)")
	cmd.StringVar(&fs.rootArg, "root", "", "destination root directory (default: directory of --ledger)")
	cmd.BoolVar(&c.passThrough, "pass-through", false, "emit non-routable directives to stdout instead of erroring")
	cmd.BoolVar(&c.quiet, "quiet", false, "suppress per-file and total stats on stderr")

	cmd.StringVar(&fs.order, "order", "", "ascending | descending | append")
	cmd.StringVar(&fs.filePattern, "file-pattern", "", "YYYY | YYYYmm | YYYYmmdd")
	cmd.StringVar(&fs.txnStrategy, "txn-strategy", "", "first-posting | last-posting | first-debit | first-credit")
	cmd.StringVar(&fs.overrideMetaKey, "override-meta-key", "", "metadata key (default: route-account)")

	cmd.BoolVar(&fs.commaGrouping, "format-comma-grouping", false, "insert thousands separators in numbers")
	cmd.BoolVar(&fs.alignAmounts, "format-align-amounts", false, "column-align posting amounts")
	cmd.IntVar(&fs.amountColumn, "format-amount-column", 0, "right-edge column for amounts")
	cmd.IntVar(&fs.eastAsianAmbiguousWidth, "format-east-asian-ambiguous-width", 0, "EA Ambiguous char width: 1 or 2")
	cmd.IntVar(&fs.indentWidth, "format-indent-width", 0, "spaces per indent level")
	cmd.IntVar(&fs.blankLinesBetweenDirs, "format-blank-lines-between-directives", 0, "target blank lines between directives")
	cmd.BoolVar(&fs.insertBlankLinesBetweenDirs, "format-insert-blank-lines-between-directives", false, "actively insert blank lines between directives")

	cmd.Usage = func() { printUsage(stderr, cmd) }
	return cmd, fs, c
}

// parseFlags parses args, validates --ledger, resolves --ledger and
// --root to absolute paths, and loads the routing config. On user error
// (bad flag, missing --ledger, bad TOML) it prints to stderr and returns
// a nil cfg with the intended exit code (2). On -h/--help it returns
// (nil, nil, 0). On success it returns the populated cfg, the list of
// positional source paths, and 0.
func parseFlags(args []string, stderr io.Writer) (*cfg, []string, int) {
	cmd, fs, c := newFlagSet(stderr)
	if err := cmd.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, 0
		}
		return nil, nil, 2
	}
	cmd.Visit(func(f *flag.Flag) { fs.set[f.Name] = true })

	if fs.ledgerArg == "" {
		fmt.Fprintln(stderr, "beanfile: --ledger is required")
		return nil, nil, 2
	}
	if _, err := os.Stat(fs.ledgerArg); err != nil {
		fmt.Fprintf(stderr, "beanfile: %v\n", err)
		return nil, nil, 2
	}
	abs, err := filepath.Abs(fs.ledgerArg)
	if err != nil {
		fmt.Fprintf(stderr, "beanfile: resolving ledger %q: %v\n", fs.ledgerArg, err)
		return nil, nil, 2
	}
	c.ledgerAbs = abs

	rootArg := fs.rootArg
	if rootArg == "" {
		rootArg = filepath.Dir(fs.ledgerArg)
	}
	rootAbs, err := filepath.Abs(rootArg)
	if err != nil {
		fmt.Fprintf(stderr, "beanfile: resolving root %q: %v\n", rootArg, err)
		return nil, nil, 2
	}
	c.rootAbs = rootAbs

	rcfg, err := loadRouteConfig(fs)
	if err != nil {
		fmt.Fprintf(stderr, "beanfile: %v\n", err)
		return nil, nil, 2
	}
	rcfg.Root = rootAbs
	c.route = rcfg

	return c, cmd.Args(), 0
}

// loadRouteConfig resolves the routing config from explicit --config
// (when set), then ./beanfile.toml (when present), and finally a
// zero-value Config. CLI --order / --file-pattern / --txn-strategy /
// --override-meta-key / --format-* flags overlay the result.
func loadRouteConfig(fs *parsedFlags) (*route.Config, error) {
	var rcfg *route.Config
	if fs.set["config"] {
		loaded, err := routeconfig.Load(fs.configPath)
		if err != nil {
			return nil, err
		}
		rcfg = loaded
	} else {
		loaded, err := routeconfig.LoadIfExists(defaultConfigFile)
		if err != nil {
			return nil, err
		}
		rcfg = loaded
	}
	if rcfg == nil {
		rcfg = &route.Config{}
	}
	overlayFlags(rcfg, fs)
	return rcfg, nil
}

// overlayFlags writes the user's --order / --file-pattern /
// --txn-strategy / --override-meta-key and --format-* flags onto rcfg.
// Each flag is applied only when the user actually set it on the
// command line, leaving inherited values untouched otherwise.
func overlayFlags(rcfg *route.Config, fs *parsedFlags) {
	if fs.set["order"] {
		rcfg.Account.Order = fs.order
		rcfg.Price.Order = fs.order
	}
	if fs.set["file-pattern"] {
		rcfg.Account.FilePattern = fs.filePattern
		rcfg.Price.FilePattern = fs.filePattern
	}
	if fs.set["txn-strategy"] {
		rcfg.Transaction.DefaultStrategy = fs.txnStrategy
	}
	if fs.set["override-meta-key"] {
		rcfg.Transaction.OverrideMetaKey = fs.overrideMetaKey
	}

	flagFormat := route.FormatSection{}
	if fs.set["format-comma-grouping"] {
		v := fs.commaGrouping
		flagFormat.CommaGrouping = &v
	}
	if fs.set["format-align-amounts"] {
		v := fs.alignAmounts
		flagFormat.AlignAmounts = &v
	}
	if fs.set["format-amount-column"] {
		v := fs.amountColumn
		flagFormat.AmountColumn = &v
	}
	if fs.set["format-east-asian-ambiguous-width"] {
		v := fs.eastAsianAmbiguousWidth
		flagFormat.EastAsianAmbiguousWidth = &v
	}
	if fs.set["format-indent-width"] {
		v := fs.indentWidth
		flagFormat.IndentWidth = &v
	}
	if fs.set["format-blank-lines-between-directives"] {
		v := fs.blankLinesBetweenDirs
		flagFormat.BlankLinesBetweenDirectives = &v
	}
	if fs.set["format-insert-blank-lines-between-directives"] {
		v := fs.insertBlankLinesBetweenDirs
		flagFormat.InsertBlankLinesBetweenDirectives = &v
	}
	rcfg.Format = route.MergeFormatSections(rcfg.Format, flagFormat)
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
	index, ledgerDiags, err := dedup.BuildIndex(ctx, c.ledgerAbs, c.rootAbs, dedup.WithOverrideMetaKey(c.route.Transaction.OverrideMetaKey))
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
				blankLines:       decision.ResolvedBlankLinesBetweenDirectives,
				insertBlankLines: decision.ResolvedInsertBlankLinesBetweenDirectives,
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
				Path:                              filepath.Join(c.rootAbs, p),
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
// file. The values come from route.Decide via Decision.ResolvedBlank*
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
