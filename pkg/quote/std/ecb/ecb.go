package ecb

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// defaultBaseURL is the production ECB endpoint root. The three concrete
// feed URLs (daily, 90-day, full history) are formed by joining a file
// name to this base.
const defaultBaseURL = "https://www.ecb.europa.eu/stats/eurofxref"

// Feed file names appended to BaseURL.
const (
	dailyFile   = "eurofxref-daily.xml"
	hist90dFile = "eurofxref-hist-90d.xml"
	histFile    = "eurofxref-hist.xml"
)

// hist90dWindow is how many days back the 90-day feed is guaranteed to
// cover. Anything strictly older than this is served from the full
// historical feed.
const hist90dWindow = 90 * 24 * time.Hour

// Source is the ECB reference-rates quote source.
//
// It serves only EUR-base pairs (Pair.Commodity == "EUR"); requests
// for any other base commodity emit a quote-source-mismatch
// diagnostic and are skipped without producing a Price.
//
// Symbol semantics: ECB has no per-instrument symbol concept. A
// SourceQuery whose Symbol is the empty string is equivalent to one
// whose Symbol is the same string as Pair.QuoteCurrency; any non-
// empty Symbol is the ISO currency code that will be looked up in
// the feed.
//
// Source is safe for concurrent use by multiple goroutines.
type Source struct {
	// Client is the HTTP client used for feed fetches. If nil,
	// http.DefaultClient is used.
	Client *http.Client
	// Now returns the current time. If nil, time.Now is used. Tests
	// inject a fixed clock so the 90-day cutoff is deterministic.
	Now func() time.Time
	// BaseURL is the prefix joined with the per-feed file names. If
	// empty, the public ECB endpoint is used. Tests substitute a
	// local httptest.NewServer URL.
	BaseURL string
}

// init registers the package-level Source value under the name "ecb"
// when this package is imported.
func init() {
	quote.Register("ecb", &Source{})
}

// Name returns the registry name of this source.
func (s *Source) Name() string { return "ecb" }

// Capabilities reports the static set of shapes this source serves.
//
// The three feed files cover all three time-shape axes; BatchPairs is
// true because a single XML download already contains every currency
// for every covered date, so multiple pair queries against the same
// date or date span are served by a single HTTP request. RangePerCall
// is 0 (unbounded) because the historical feed itself is unbounded.
func (s *Source) Capabilities() api.Capabilities {
	return api.Capabilities{
		SupportsLatest: true,
		SupportsAt:     true,
		SupportsRange:  true,
		BatchPairs:     true,
		RangePerCall:   0,
	}
}

// QuoteLatest returns prices from the daily feed.
func (s *Source) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	wanted, diags := classify(q)
	if len(wanted) == 0 {
		return nil, diags, nil
	}
	env, err := s.fetchFeed(ctx, dailyFile)
	if err != nil {
		return nil, diags, err
	}
	prices, more := pickAllDates(env, wanted)
	return prices, append(diags, more...), nil
}

// QuoteAt returns prices for a specific calendar date. The 90-day feed
// is used when at is within the last 90 days of s.now(); otherwise the
// full historical feed is used.
func (s *Source) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	wanted, diags := classify(q)
	if len(wanted) == 0 {
		return nil, diags, nil
	}
	feed := s.feedForRange(at)
	env, err := s.fetchFeed(ctx, feed)
	if err != nil {
		return nil, diags, err
	}
	prices, more := pickOnDate(env, wanted, at)
	return prices, append(diags, more...), nil
}

// QuoteRange returns prices for the half-open interval [start, end).
// The 90-day feed is used when the entire range fits inside the 90-day
// window; otherwise the full historical feed is used.
func (s *Source) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	wanted, diags := classify(q)
	if len(wanted) == 0 {
		return nil, diags, nil
	}
	feed := s.feedForRange(start)
	env, err := s.fetchFeed(ctx, feed)
	if err != nil {
		return nil, diags, err
	}
	prices, more := pickInRange(env, wanted, start, end)
	return prices, append(diags, more...), nil
}

// classify partitions queries into the EUR-base ones the source can
// serve and a slice of quote-source-mismatch diagnostics for any
// non-EUR base. Returned slice elements preserve input order among
// EUR-base queries.
func classify(q []api.SourceQuery) ([]api.SourceQuery, []ast.Diagnostic) {
	var wanted []api.SourceQuery
	var diags []ast.Diagnostic
	for _, sq := range q {
		if sq.Pair.Commodity != "EUR" {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-source-mismatch",
				Severity: ast.Warning,
				Message: fmt.Sprintf(
					"ecb: only EUR-base pairs are supported; pair %s/%s skipped (ECB publishes reference rates against EUR only)",
					sq.Pair.Commodity, sq.Pair.QuoteCurrency,
				),
			})
			continue
		}
		wanted = append(wanted, sq)
	}
	return wanted, diags
}

// feedForRange chooses between the 90-day and the full-history feed.
// If start is older than the 90-day window relative to s.now(), the
// historical feed is needed. Only the start of the range matters: the
// 90-day feed is a strict suffix of the full-history feed, so once
// start falls inside the 90-day window the entire range does too.
func (s *Source) feedForRange(start time.Time) string {
	now := s.now()
	if start.Before(now.Add(-hist90dWindow)) {
		return histFile
	}
	return hist90dFile
}

// fetchFeed downloads and parses one feed file. The URL is formed by
// joining BaseURL and file with a "/" separator.
func (s *Source) fetchFeed(ctx context.Context, file string) (*envelope, error) {
	url := s.baseURL() + "/" + file
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("ecb: build request for %s: %w", file, err)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ecb: GET %s: %w", file, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Drain a small amount so connections can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ecb: GET %s: HTTP %d", file, resp.StatusCode)
	}
	var env envelope
	dec := xml.NewDecoder(resp.Body)
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("ecb: parse %s: %w", file, err)
	}
	return &env, nil
}

func (s *Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Source) baseURL() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return defaultBaseURL
}

// envelope mirrors the ECB feed XML structure. The default xml package
// matching is namespace-agnostic with respect to element local names,
// which is what we want: the "Cube" elements live in the eurofxref
// namespace but the local-name match suffices for an unambiguous
// schema.
type envelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Cube    outerCube
}

// outerCube is the top-level <Cube> wrapper. Its children are the
// per-date <Cube time="..."> entries.
type outerCube struct {
	Days []dayCube `xml:"Cube"`
}

// dayCube is one calendar date's worth of rates.
type dayCube struct {
	Time  string     `xml:"time,attr"`
	Rates []rateCube `xml:"Cube"`
}

// rateCube is one (currency, rate) pair within a day.
type rateCube struct {
	Currency string `xml:"currency,attr"`
	Rate     string `xml:"rate,attr"`
}

// parseDay parses a YYYY-MM-DD attribute into a UTC midnight time.
func parseDay(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// pickAllDates applies the latest-shape projection: for each date
// present in the envelope, every wanted pair contributes one Price.
// In practice the daily feed contains exactly one date.
func pickAllDates(env *envelope, wanted []api.SourceQuery) ([]ast.Price, []ast.Diagnostic) {
	var prices []ast.Price
	var diags []ast.Diagnostic
	for _, day := range env.Cube.Days {
		ts, err := parseDay(day.Time)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-fetch-error",
				Severity: ast.Error,
				Message:  fmt.Sprintf("ecb: invalid time attribute %q: %v", day.Time, err),
			})
			continue
		}
		ps, ds := projectDay(day, ts, wanted)
		prices = append(prices, ps...)
		diags = append(diags, ds...)
	}
	return prices, diags
}

// pickOnDate selects the requested calendar day from the envelope. If
// no exact match is found, the resulting price slice is empty (no
// per-pair diagnostic is emitted: ECB does not publish on weekends or
// holidays, and the orchestrator's no-result handling is the right
// place to surface that).
func pickOnDate(env *envelope, wanted []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic) {
	target := at.Format("2006-01-02")
	for _, day := range env.Cube.Days {
		if day.Time != target {
			continue
		}
		return projectDay(day, mustParseDay(day.Time), wanted)
	}
	return nil, nil
}

// pickInRange selects every day inside [start, end) and projects each
// onto the wanted queries.
func pickInRange(env *envelope, wanted []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic) {
	var prices []ast.Price
	var diags []ast.Diagnostic
	for _, day := range env.Cube.Days {
		ts, err := parseDay(day.Time)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-fetch-error",
				Severity: ast.Error,
				Message:  fmt.Sprintf("ecb: invalid time attribute %q: %v", day.Time, err),
			})
			continue
		}
		if ts.Before(start) || !ts.Before(end) {
			continue
		}
		ps, ds := projectDay(day, ts, wanted)
		prices = append(prices, ps...)
		diags = append(diags, ds...)
	}
	return prices, diags
}

// projectDay turns one day's rates plus the wanted queries into a
// slice of Prices. A query whose currency is missing on this day is
// silently skipped; no per-pair diagnostic is emitted because a
// missing entry is indistinguishable in the feed from "ECB does not
// publish that pair", and surfacing one diagnostic per absent day in
// a multi-day range would drown the output.
func projectDay(day dayCube, ts time.Time, wanted []api.SourceQuery) ([]ast.Price, []ast.Diagnostic) {
	// Build a small map from currency to rate string. Rebuilding per
	// call avoids any cross-call state and keeps the source
	// goroutine-safe; the map is tiny (~30 currencies).
	byCcy := make(map[string]string, len(day.Rates))
	for _, r := range day.Rates {
		byCcy[r.Currency] = r.Rate
	}
	var prices []ast.Price
	var diags []ast.Diagnostic
	for _, sq := range wanted {
		ccy := sq.Symbol
		if ccy == "" {
			ccy = sq.Pair.QuoteCurrency
		}
		raw, ok := byCcy[ccy]
		if !ok {
			continue
		}
		num, _, err := apd.NewFromString(raw)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:     "quote-fetch-error",
				Severity: ast.Error,
				Message: fmt.Sprintf(
					"ecb: invalid rate %q for %s on %s: %v",
					raw, ccy, day.Time, err,
				),
			})
			continue
		}
		prices = append(prices, ast.Price{
			Date:      ts,
			Commodity: sq.Pair.Commodity,
			Amount: ast.Amount{
				Number:   *num,
				Currency: sq.Pair.QuoteCurrency,
			},
		})
	}
	return prices, diags
}

// mustParseDay is a helper used in code paths where the caller has
// already validated the format upstream.
func mustParseDay(s string) time.Time {
	t, err := parseDay(s)
	if err != nil {
		// Should not happen: callers only pass strings that already
		// parsed once. Surface the zero time so the resulting Price
		// has an obviously-bogus date rather than panicking.
		return time.Time{}
	}
	return t
}
