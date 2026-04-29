// Command beanprice fetches commodity prices using the Phase 7 quote
// pipeline. It walks one or more beancount ledgers for Commodity
// directives carrying bean-price-compatible "price" meta values, adds
// any inline --source requests, dispatches them through one of
// pkg/quote.FetchLatest / FetchAt / FetchRange, deduplicates the
// result, and prints canonical price directives to stdout.
// Diagnostics from any layer are printed to stderr in the same
// "<path>:<line>:<col>: <severity>: <message>" form that cmd/beancheck
// uses.
//
// Run "beanprice -h" for the full flag set, the price-meta grammar, the
// --source syntax, the exit-code table, and worked examples.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/loader"
	"github.com/yugui/go-beancount/pkg/quote"
	"github.com/yugui/go-beancount/pkg/quote/api"
	"github.com/yugui/go-beancount/pkg/quote/meta"
	"github.com/yugui/go-beancount/pkg/quote/pricedb"

	// Blank-import the in-tree ECB source so the default invocation
	//   beanprice --source 'EUR=USD:ecb/USD' --latest
	// works without a --plugin flag.
	_ "github.com/yugui/go-beancount/pkg/quote/std/ecb"
)

// Diagnostic codes emitted by beanprice itself (i.e. not produced by an
// underlying package). They follow the existing "quote-<noun>" naming.
const (
	codeCommodityMissing = "quote-commodity-missing"
	codeNoMeta           = "quote-no-meta"
	codeSourceFlagSyntax = "quote-source-flag-syntax"
)

// stringSlice is a flag.Value receiver that accumulates repeated flag
// occurrences. Comma is reserved for the bean-price meta fallback
// chain; CLI-level repetition is expressed solely through repeating the
// flag itself.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. args is os.Args[1:].
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags, err := parseFlags(args, stderr)
	if err != nil {
		// Flag parse errors already printed by the FlagSet's own
		// ContinueOnError handler. Distinguish -h (no error) from bad
		// flags.
		if errors.Is(err, errHelpRequested) {
			return 0
		}
		return 2
	}

	// Mode resolution.
	mode, at, start, end, err := resolveMode(flags)
	if err != nil {
		fmt.Fprintf(stderr, "beanprice: %v\n", err)
		return 2
	}

	// Plugin loading. Failures here are CLI-level (exit 2) — the
	// operator named a path that does not load.
	for _, p := range flags.plugins {
		if err := goplug.Load(p); err != nil {
			fmt.Fprintf(stderr, "beanprice: plugin load failed: %v\n", err)
			return 2
		}
	}

	// Assemble requests from --ledger walk and inline --source flags.
	var (
		requests []api.PriceRequest
		diags    []ast.Diagnostic
	)
	ledgerReqs, ledgerDiags, err := requestsFromLedgers(ctx, flags)
	if err != nil {
		fmt.Fprintf(stderr, "beanprice: %v\n", err)
		return 2
	}
	requests = append(requests, ledgerReqs...)
	diags = append(diags, ledgerDiags...)

	sourceReqs, sourceDiags := requestsFromSourceFlags(flags.sources)
	requests = append(requests, sourceReqs...)
	diags = append(diags, sourceDiags...)

	if len(requests) == 0 {
		fmt.Fprintln(stderr, "beanprice: no requests (use --ledger or --source)")
		return 2
	}

	// Fetch. The mode is encoded in the function chosen, so the time
	// arguments only flow into the entry point that actually consults
	// them.
	var (
		prices     []ast.Price
		fetchDiags []ast.Diagnostic
		fetchErr   error
	)
	switch mode {
	case api.ModeLatest:
		prices, fetchDiags, fetchErr = quote.FetchLatest(
			ctx,
			quote.GlobalRegistry(),
			requests,
			quote.WithConcurrency(flags.concurrency),
		)
	case api.ModeAt:
		prices, fetchDiags, fetchErr = quote.FetchAt(
			ctx,
			quote.GlobalRegistry(),
			requests,
			at,
			quote.WithConcurrency(flags.concurrency),
		)
	case api.ModeRange:
		prices, fetchDiags, fetchErr = quote.FetchRange(
			ctx,
			quote.GlobalRegistry(),
			requests,
			start,
			end,
			quote.WithConcurrency(flags.concurrency),
		)
	default:
		fmt.Fprintf(stderr, "beanprice: internal error: unhandled fetch mode %v\n", mode)
		return 2
	}
	diags = append(diags, fetchDiags...)

	// A bad --range (reversed/empty interval) surfaces here as
	// ErrInvalidRange. That is a CLI usage error (exit 2), not a
	// fetch failure (exit 1).
	if errors.Is(fetchErr, quote.ErrInvalidRange) {
		fmt.Fprintf(stderr, "beanprice: %v\n", fetchErr)
		return 2
	}

	// Dedup before printing; duplicates are fed in as diagnostics.
	kept, dupDiags := pricedb.Dedup(prices, true)
	diags = append(diags, dupDiags...)

	// Print prices to stdout, diagnostics to stderr. Even on a Fetch
	// error there may be partial output worth showing.
	if err := pricedb.FormatStream(stdout, kept); err != nil {
		fmt.Fprintf(stderr, "beanprice: writing stdout: %v\n", err)
		return 1
	}
	exit := report(stderr, diags, flags.strict)
	if fetchErr != nil {
		fmt.Fprintf(stderr, "beanprice: fetch: %v\n", fetchErr)
		if exit == 0 {
			exit = 1
		}
	}
	return exit
}

// errHelpRequested is returned by parseFlags when the user passed -h
// or --help. run maps it to exit 0 without further processing.
var errHelpRequested = errors.New("help requested")

// resolvedFlags is the post-parse flag bundle handed to the rest of run.
type resolvedFlags struct {
	ledgers     []string
	commodities []string
	sources     []string
	plugins     []string
	metaKey     string
	date        string
	rng         string
	latest      bool
	concurrency int
	strict      bool
}

// parseFlags configures and parses the flag set. It returns
// errHelpRequested when -h / --help was handled cleanly, a non-nil
// error on parse failure, or a populated *resolvedFlags on success.
func parseFlags(args []string, stderr io.Writer) (*resolvedFlags, error) {
	r := &resolvedFlags{
		metaKey:     meta.DefaultMetaKey,
		concurrency: 32,
	}
	var (
		ledgers     stringSlice
		commodities stringSlice
		sources     stringSlice
		plugins     stringSlice
	)
	cmd := flag.NewFlagSet("beanprice", flag.ContinueOnError)
	cmd.SetOutput(stderr)
	cmd.Var(&ledgers, "ledger", "ledger file to walk for Commodity price meta (repeatable)")
	cmd.Var(&commodities, "commodity", "filter --ledger walk to commodity CODE (repeatable)")
	cmd.Var(&sources, "source", "inline price request COMMODITY=CCY:source/SYM[,source/SYM]* (repeatable)")
	cmd.Var(&plugins, "plugin", "load goplug .so quoter from PATH (repeatable)")
	cmd.StringVar(&r.metaKey, "meta-key", r.metaKey, "meta key on Commodity directives that holds the price spec")
	cmd.StringVar(&r.date, "date", "", "fetch the price as of YYYY-MM-DD (mode At)")
	cmd.StringVar(&r.rng, "range", "", "fetch the half-open range START..END as YYYY-MM-DD..YYYY-MM-DD (mode Range)")
	cmd.BoolVar(&r.latest, "latest", false, "fetch the latest available price (default if no --date or --range)")
	cmd.IntVar(&r.concurrency, "concurrency", r.concurrency, "max in-flight source method calls across all sources")
	cmd.BoolVar(&r.strict, "strict", false, "treat warnings as errors (exit 1 on any Warning)")
	cmd.Usage = func() { printUsage(stderr, cmd) }

	if err := cmd.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelpRequested
		}
		return nil, err
	}
	r.ledgers = []string(ledgers)
	r.commodities = []string(commodities)
	r.sources = []string(sources)
	r.plugins = []string(plugins)
	return r, nil
}

// resolveMode picks api.Mode and time fields from the date / range /
// latest flags. --date and --range are mutually exclusive, and --latest
// cannot be combined with either of them; if no mode flag is set,
// ModeLatest is selected (--latest is the default).
func resolveMode(r *resolvedFlags) (mode api.Mode, at, start, end time.Time, err error) {
	if r.latest && (r.date != "" || r.rng != "") {
		return 0, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("--latest is mutually exclusive with --date and --range")
	}
	if r.date != "" && r.rng != "" {
		return 0, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("--date and --range are mutually exclusive")
	}
	switch {
	case r.date != "":
		t, perr := time.ParseInLocation("2006-01-02", r.date, time.UTC)
		if perr != nil {
			return 0, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("--date: %w", perr)
		}
		return api.ModeAt, t, time.Time{}, time.Time{}, nil
	case r.rng != "":
		parts := strings.SplitN(r.rng, "..", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return 0, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("--range must be START..END (got %q)", r.rng)
		}
		s, perr := time.ParseInLocation("2006-01-02", parts[0], time.UTC)
		if perr != nil {
			return 0, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("--range start: %w", perr)
		}
		e, perr := time.ParseInLocation("2006-01-02", parts[1], time.UTC)
		if perr != nil {
			return 0, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("--range end: %w", perr)
		}
		return api.ModeRange, time.Time{}, s, e, nil
	default:
		// --latest is the default; explicit --latest is also accepted.
		return api.ModeLatest, time.Time{}, time.Time{}, time.Time{}, nil
	}
}

// requestsFromLedgers walks each --ledger path, extracts price-meta
// PriceRequests from every Commodity directive, and applies any
// --commodity filter. It returns ledger-level diagnostics
// (loader.Diagnostics, meta-parser diagnostics, missing/no-meta
// warnings) accumulated along the way. A non-nil error means a ledger
// could not be loaded at all (file missing, IO error) — that is a
// CLI-level failure (exit 2).
func requestsFromLedgers(ctx context.Context, r *resolvedFlags) ([]api.PriceRequest, []ast.Diagnostic, error) {
	if len(r.ledgers) == 0 {
		if len(r.commodities) > 0 {
			return nil, nil, fmt.Errorf("--commodity requires --ledger")
		}
		return nil, nil, nil
	}
	commoditySet := map[string]struct{}{}
	for _, c := range r.commodities {
		commoditySet[c] = struct{}{}
	}
	seen := map[string]bool{} // commodity name -> had any meta?
	var (
		out   []api.PriceRequest
		diags []ast.Diagnostic
	)
	for _, path := range r.ledgers {
		ledger, err := loader.LoadFile(ctx, path)
		if err != nil {
			return nil, nil, fmt.Errorf("loading %q: %w", path, err)
		}
		diags = append(diags, ledger.Diagnostics...)
		for _, d := range ledger.All() {
			c, ok := d.(*ast.Commodity)
			if !ok {
				continue
			}
			if len(commoditySet) > 0 {
				if _, want := commoditySet[c.Currency]; !want {
					continue
				}
			}
			reqs, dd := meta.ExtractFromCommodity(c, r.metaKey)
			diags = append(diags, dd...)
			if len(reqs) > 0 {
				seen[c.Currency] = true
				out = append(out, reqs...)
			} else if _, want := commoditySet[c.Currency]; want {
				// In --commodity mode, a matching directive without
				// any usable meta is a quote-no-meta warning.
				if _, ok := c.Meta.Props[r.metaKey]; !ok {
					diags = append(diags, ast.Diagnostic{
						Code:     codeNoMeta,
						Span:     c.Span,
						Severity: ast.Warning,
						Message: fmt.Sprintf(
							"commodity %q has no %q meta; cannot fetch its price",
							c.Currency, r.metaKey),
					})
				}
				seen[c.Currency] = true
			}
		}
	}
	// Commodities named on --commodity but never seen in any ledger.
	for _, name := range r.commodities {
		if !seen[name] {
			diags = append(diags, ast.Diagnostic{
				Code:     codeCommodityMissing,
				Severity: ast.Warning,
				Message: fmt.Sprintf(
					"commodity %q named on --commodity was not declared in any --ledger",
					name),
			})
		}
	}
	return out, diags, nil
}

// requestsFromSourceFlags parses each --source value. The expected
// form is `COMMODITY=CCY:source/SYM[,source/SYM]*`: the COMMODITY=
// prefix (a beanprice extension) names the base commodity, and the
// rest is parsed by pkg/quote/meta.ParsePriceMeta verbatim.
func requestsFromSourceFlags(sources []string) ([]api.PriceRequest, []ast.Diagnostic) {
	var (
		out   []api.PriceRequest
		diags []ast.Diagnostic
	)
	for _, raw := range sources {
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 || eq == len(raw)-1 {
			diags = append(diags, ast.Diagnostic{
				Code:     codeSourceFlagSyntax,
				Severity: ast.Error,
				Message: fmt.Sprintf(
					"--source %q must be COMMODITY=CCY:source/SYM[,source/SYM]*",
					raw),
			})
			continue
		}
		commodity := raw[:eq]
		body := raw[eq+1:]
		reqs, dd := meta.ParsePriceMeta(commodity, body)
		diags = append(diags, dd...)
		out = append(out, reqs...)
	}
	return out, diags
}

// report writes each diagnostic to w and returns the exit code per the
// exit-code table documented in the package doc. Identical in shape to
// cmd/beancheck.report.
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

// formatDiagnostic mirrors cmd/beancheck's diagnostic format so a
// single grep pattern matches output from either tool.
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

// printUsage writes the full --help block. It is the destination for
// the design doc's CLI section: price-meta grammar, --source syntax,
// exit-code table, examples.
func printUsage(w io.Writer, cmd *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: beanprice [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Fetch commodity prices via the Phase 7 quote pipeline.")
	fmt.Fprintln(w, "Walks --ledger files for Commodity directives carrying bean-price-")
	fmt.Fprintln(w, `compatible "price" meta values, plus any inline --source flags,`)
	fmt.Fprintln(w, "dispatches them through pkg/quote, and prints canonical")
	fmt.Fprintln(w, "price directives to stdout. Diagnostics go to stderr.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	cmd.PrintDefaults()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "PRICE META FORMAT")
	fmt.Fprintln(w, "  Each Commodity directive may carry a bean-price-compatible meta")
	fmt.Fprintln(w, "  value (default key: \"price\"; override with --meta-key).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    value   := psource (WS+ psource)*")
	fmt.Fprintln(w, "    psource := CCY \":\" entry (\",\" entry)*")
	fmt.Fprintln(w, "    entry   := SOURCE \"/\" SYMBOL")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Worked examples (inside a Commodity directive):")
	fmt.Fprintln(w, "    price: \"USD:yahoo/AAPL\"")
	fmt.Fprintln(w, "        single quote currency, single source")
	fmt.Fprintln(w, "    price: \"USD:yahoo/AAPL,google/AAPL\"")
	fmt.Fprintln(w, "        single currency with priority-ordered fallback chain")
	fmt.Fprintln(w, "    price: \"USD:yahoo/X JPY:yahoo/XJPY\"")
	fmt.Fprintln(w, "        same commodity, two different quote currencies (two requests)")
	fmt.Fprintln(w, "    price: \"USD:yahoo/X,google/X JPY:google/XJPY\"")
	fmt.Fprintln(w, "        combination of the above")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Within a single psource, a comma-separated chain expresses fallback")
	fmt.Fprintln(w, "  in priority order. Whitespace separates psources for distinct quote")
	fmt.Fprintln(w, "  currencies of the same commodity. The bean-price '^' inverted-quote")
	fmt.Fprintln(w, "  prefix is not supported.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "--source FLAG FORMAT")
	fmt.Fprintln(w, "  --source 'COMMODITY=CCY:source/SYM[,source/SYM]*'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  The bean-price meta value carried on a Commodity directive implies")
	fmt.Fprintln(w, "  its base commodity from the directive it sits on. Outside that")
	fmt.Fprintln(w, "  context — a one-shot CLI invocation with no ledger — the commodity")
	fmt.Fprintln(w, "  must be spelled. The COMMODITY= prefix is the smallest extension")
	fmt.Fprintln(w, "  that preserves the rest of the bean-price grammar verbatim.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Multi-value flags (--ledger, --commodity, --source, --plugin) repeat")
	fmt.Fprintln(w, "  rather than comma-split: the comma is reserved for the fallback chain")
	fmt.Fprintln(w, "  inside a single meta value.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXIT CODES")
	fmt.Fprintln(w, "  0  success; no Error diagnostics, no fetch error")
	fmt.Fprintln(w, "     (Warnings allowed unless --strict.)")
	fmt.Fprintln(w, "  1  at least one Error diagnostic, OR pkg/quote fetch returned an")
	fmt.Fprintln(w, "     error, OR --strict and at least one Warning")
	fmt.Fprintln(w, "  2  CLI failure: bad flags, mutually exclusive --date/--range,")
	fmt.Fprintln(w, "     missing ledger file, plugin load failure, or no requests")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXAMPLES")
	fmt.Fprintln(w, "  beanprice --source 'EUR=USD:ecb/USD' --latest")
	fmt.Fprintln(w, "      one-shot: fetch EUR's latest price in USD via ECB")
	fmt.Fprintln(w, "  beanprice --ledger my.beancount --range 2026-01-01..2026-02-01")
	fmt.Fprintln(w, "      walk my.beancount, fetch every priced commodity over January")
	fmt.Fprintln(w, "  beanprice --ledger my.beancount --commodity AAPL --commodity GOOG")
	fmt.Fprintln(w, "      walk my.beancount but restrict to AAPL and GOOG")
	fmt.Fprintln(w, "  beanprice --plugin /path/to/x.so --source 'JPY=USD:x/USDJPY'")
	fmt.Fprintln(w, "      load an out-of-tree quoter and use it for one inline request")
}
