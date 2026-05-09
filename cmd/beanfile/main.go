// Command beanfile is a stateless offline CLI that reads a beancount
// directive stream (stdin or one or more positional files) and merges
// each directive into the appropriate file in a multi-file ledger. It
// bridges directive producers — beanprice today, importers tomorrow —
// with the multi-file layout, without requiring bean-daemon to run.
//
// Usage:
//
//	beanfile [flags] --ledger ROOT.beancount [files...]
//
// Required:
//
//	--ledger PATH   ledger root file used to walk the include closure
//	                and build the dedup index (active + commented-out
//	                directives across the whole ledger)
//
// Common flags:
//
//	--config PATH                       TOML routing config (default:
//	                                    ./beanfile.toml if present)
//	--root PATH                         destination root (default:
//	                                    directory of --ledger). Also
//	                                    the base for dedup index path
//	                                    canonicalization.
//	--dry-run                           print proposed patches to stdout
//	                                    instead of writing files
//	--pass-through                      emit non-routable directives on
//	                                    stdout instead of erroring out
//	--quiet                             suppress per-file and total
//	                                    stats on stderr
//	--order ascending|descending|append how new directives are positioned
//	                                    relative to existing dated ones
//	--file-pattern YYYY|YYYYmm|YYYYmmdd date granularity for {date} in
//	                                    destination templates
//	--txn-strategy first-posting|last-posting|first-debit|first-credit
//	                                    posting selector when no
//	                                    route-account override is set
//	--override-meta-key STR             metadata key for transaction
//	                                    routing overrides (default:
//	                                    route-account)
//	--format-*                          override individual format
//	                                    options resolved from TOML
//	                                    (comma_grouping, align_amounts,
//	                                    amount_column,
//	                                    east_asian_ambiguous_width,
//	                                    indent_width,
//	                                    blank_lines_between_directives,
//	                                    insert_blank_lines_between_directives)
//
// # Inputs
//
// Positional files are read in argument order. With no positional args,
// or with a single "-", input comes from stdin. Relative include
// directives in the input are rejected (unlike for the ledger root,
// where include resolution is required to assemble the dedup index);
// absolute-path includes still resolve.
//
// # Standard routing convention
//
// Each directive is filed by its kind:
//
//	Open, Close, Balance, Note, Document, Pad, Transaction
//	    routed by Account →
//	    transactions/{account}/{date}.beancount
//	Price
//	    routed by Commodity →
//	    quotes/{commodity}/{date}.beancount
//	Option, Plugin, Include, Event, Query, Custom, Commodity
//	    not routable; emit error by default, or pass-through to
//	    stdout under --pass-through
//
// {account} expands to slash-separated path segments
// (Assets:Foo:Bar → Assets/Foo/Bar). {date} formats the directive's
// date under the configured file pattern. Calendar fields are read
// directly from the time value to avoid timezone conversion.
//
// A Transaction touches multiple accounts, so the routing key is
// resolved by a four-rule precedence chain:
//
//  1. Transaction-level metadata route-account: "Assets:Foo:Bar"
//     (string) — the value names the destination account verbatim.
//  2. The first posting whose metadata contains route-account: TRUE
//     (bool); FALSE is treated as if absent.
//  3. The configured default_strategy (first-posting, last-posting,
//     first-debit, or first-credit).
//  4. Fallback: the first posting's account.
//
// The routing-hint key is always stripped from the emitted directive
// (transaction header and every posting); the input AST is never
// mutated.
//
// # Three-way dedup decision
//
// For each input directive D that routes to destination P, the CLI
// consults the ledger-wide equivalence index built from the --ledger
// transitive include closure:
//
//  1. If P already contains an equivalent directive — active OR
//     commented-out — D is skipped (counted, not written).
//  2. Else, if any active equivalent of D exists at any path other
//     than P, D is written to P as a commented-out marker (the common
//     "this transaction lives in another file" annotation).
//  3. Otherwise D is written to P as a normal active directive.
//
// Equivalence is OR-combined: AST equality (with Span and the routing
// override key stripped) wins first; otherwise a metadata-key match
// against the resolved equivalence_meta_keys list produces a meta
// match. The index is updated as inserts are accepted within a single
// run, so duplicates within the input stream itself are skipped.
//
// # Merge semantics
//
// New destination files are created with parent directories. Existing
// files round-trip via the CST: every byte not covered by a new
// insertion is written back unchanged. Insertion offset is chosen by
// binary search on the existing dated directives under the requested
// order; the surrounding existing content is never reordered.
// Same-day, same-destination inserts keep input order. Each
// destination write is atomic (temp file in the same directory + fsync
// + rename). Spacing around new directives is governed by
// blank_lines_between_directives (target N) and
// insert_blank_lines_between_directives (whether to actively pad);
// the merger never reduces pre-existing blank lines — whole-file
// normalization is left to a later beanfmt pass.
//
// # Stats
//
// On exit, unless --quiet is given, each destination file gets one
// stderr line "beanfile: <path>: written=N commented=N skipped=N"
// followed by a single "beanfile: total: written=N commented=N
// skipped=N passthrough=N" summary. passthrough is global only — by
// design, non-routable directives have no destination path. Skip-only
// destinations (paths where no directive was added but at least one
// matched the index) appear in per-file stats as well.
//
// # Pass-through and dry-run
//
// Without --pass-through, encountering a non-routable directive in
// input is a hard error and no destination files are touched.
// With --pass-through, those directives are written verbatim to stdout
// in input order across each input source (sources processed in
// argument order, never interleaved).
//
// Under --dry-run, the route → dedup pipeline runs as usual, but no
// files are written: each destination's would-be inserts are emitted
// to stdout under a "--- <relative path> ---" header, with each line
// of an insert prefixed by "+ " (active) or ";+ " (commented). Stats
// still go to stderr unless --quiet.
//
// # Out of scope
//
// Live services, file watching, locking, multi-file atomic
// transactions, and auto-injecting include directives into the ledger
// when new destination files are created are explicitly not provided
// here; users are expected to use a glob include in their root file.
package main

import (
	"bytes"
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
	dryRun      bool
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
	cmd.BoolVar(&c.dryRun, "dry-run", false, "print proposed patches to stdout instead of writing files")

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
	rcfg.Warn = func(format string, args ...any) {
		// Pre-format the message before embedding it under a fixed
		// "%s" so that any '%' bytes in the routing warning are not
		// re-interpreted as format verbs by the outer Fprintf.
		fmt.Fprintf(stderr, "beanfile: route: %s\n", fmt.Sprintf(format, args...))
	}
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
	orderByPath := map[string]route.OrderKind{}
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
				stripped := ast.StripMetaKeys(d, decision.StripMetaKeys)
				if err := printer.Fprint(stdout, stripped); err != nil {
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
				Directive:     d,
				Commented:     commented,
				Format:        decision.Format,
				StripMetaKeys: decision.StripMetaKeys,
			})
			spacingByPath[decision.Path] = planSpacing{
				blankLines:       decision.BlankLinesBetweenDirectives,
				insertBlankLines: decision.InsertBlankLinesBetweenDirectives,
			}
			orderByPath[decision.Path] = decision.Order
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
			if c.dryRun {
				if err := writeDryRunBlock(stdout, p, inserts); err != nil {
					fmt.Fprintf(stderr, "beanfile: dry-run %s: %v\n", p, err)
					mergeFailed = true
					continue
				}
			} else {
				sp := spacingByPath[p]
				plan := merge.Plan{
					Path:                              filepath.Join(c.route.Root, p),
					Order:                             orderByPath[p],
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

// writeDryRunBlock prints the proposed patches for one destination path
// in the dry-run preview format: a header line "--- <relative path> ---"
// followed by one prefixed line per output line of each insert. Active
// inserts use the prefix "+ "; commented inserts use ";+ ". The header
// path is the same Config.Root-relative key used for stats, so users can
// correlate stderr stats with stdout previews.
//
// Both active and commented inserts go through printer.Fprint with the
// insert's body-level Format options applied; comment.Emit is
// intentionally NOT used here — the ";+ " prefix is the dry-run's own
// marker, distinct from the "; " prefix the real merger writes for a
// commented insert. The preview is therefore not byte-faithful for
// commented inserts: the resolved Format influences alignment in the
// preview but not in the actual on-disk commented block (comment.Emit
// uses default format options). Acceptable trade-off for an MVP
// preview; future refinement can route commented previews through
// comment.Emit to match disk byte-for-byte.
func writeDryRunBlock(w io.Writer, relPath string, inserts []merge.Insert) error {
	if _, err := fmt.Fprintf(w, "--- %s ---\n", relPath); err != nil {
		return err
	}
	for _, ins := range inserts {
		d := ast.StripMetaKeys(ins.Directive, ins.StripMetaKeys)
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, d, ins.Format...); err != nil {
			return fmt.Errorf("rendering directive: %w", err)
		}
		prefix := "+ "
		if ins.Commented {
			prefix = ";+ "
		}
		body := strings.TrimRight(buf.String(), "\n")
		if body == "" {
			// Defensive: every printable directive renders at least
			// one byte, but guard against strings.Split("", "\n")
			// emitting a spurious prefix-only line in case printer
			// behaviour ever changes.
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			if _, err := fmt.Fprintf(w, "%s%s\n", prefix, line); err != nil {
				return err
			}
		}
	}
	return nil
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
