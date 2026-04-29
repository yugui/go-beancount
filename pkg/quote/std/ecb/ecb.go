package ecb

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote"
	"github.com/yugui/go-beancount/pkg/quote/api"
	"github.com/yugui/go-beancount/pkg/quote/sourceutil"
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

// ecbBaseCurrency is the fixed quote-cache QC dimension for every cache
// entry this source writes or reads. ECB publishes reference rates only
// against EUR, and classify rejects any non-EUR base, so the cache's
// (qc, sym) key collapses to ("EUR", target-currency) by construction.
const ecbBaseCurrency = "EUR"

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
// Source satisfies api.LatestSource, api.AtSource, and api.RangeSource:
// the three feed files cover all three time-shape axes, and a single
// XML download already contains every currency for every covered
// date, so the source naturally serves any mix of pair queries in
// one call and any range up to the full historical feed without
// needing a SplitBatch or SplitRange wrapper.
//
// # Caching
//
// Each parsed feed download contributes a full day×currency matrix.
// Source memoises every parsed (currency, day) rate in an internal
// [sourceutil.QuoteCache] so a follow-up QuoteAt / QuoteLatest call
// for any pair whose rate appeared in an earlier feed does not
// re-download. The latest-shape entries are populated alongside
// per-day entries when the daily feed is parsed, so a later
// QuoteLatest call for a different pair on the same day also skips
// the network. The cache is keyed on (QuoteCurrency, Symbol, time
// qualifier) — ECB's source-physical addressing units — and lasts
// for the lifetime of the Source value.
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

	cacheOnce sync.Once
	cache     *sourceutil.QuoteCache[*apd.Decimal]

	mu         sync.Mutex
	latestDate time.Time // most recent day observed in any parsed feed
}

// init registers the package-level Source value under the name "ecb"
// when this package is imported.
func init() {
	quote.Register("ecb", &Source{})
}

// Name returns the registry name of this source ("ecb").
func (s *Source) Name() string { return "ecb" }

// quoteCache lazily constructs the per-Source rate cache. The cache
// has no TTL or cap by default: ECB rates are immutable historical
// reference data, and the natural lifetime of a Source value is one
// CLI run, so unbounded growth within that scope is acceptable.
func (s *Source) quoteCache() *sourceutil.QuoteCache[*apd.Decimal] {
	s.cacheOnce.Do(func() {
		s.cache = sourceutil.NewQuoteCache[*apd.Decimal](sourceutil.QuoteCacheOptions{})
	})
	return s.cache
}

// resolveCcy returns the ISO currency code that a SourceQuery looks
// up in the feed. Empty Symbol falls back to Pair.QuoteCurrency.
func resolveCcy(sq api.SourceQuery) string {
	if sq.Symbol != "" {
		return sq.Symbol
	}
	return sq.Pair.QuoteCurrency
}

// QuoteLatest returns prices from the daily feed. A successful parse
// records the publication date alongside the rates; subsequent calls
// skip the network if every requested pair is already cached.
func (s *Source) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	wanted, diags := classify(q)
	if len(wanted) == 0 {
		return nil, diags, nil
	}

	cache := s.quoteCache()
	latestDate := s.observedLatestDate()

	hits := make([]ast.Price, 0, len(wanted))
	var missing []api.SourceQuery
	if !latestDate.IsZero() {
		for _, sq := range wanted {
			ccy := resolveCcy(sq)
			// QC dimension is always "EUR": ECB publishes
			// EUR-base rates only.
			v, ok := cache.GetLatest(ecbBaseCurrency, ccy)
			if !ok {
				missing = append(missing, sq)
				continue
			}
			hits = append(hits, ast.Price{
				Date:      latestDate,
				Commodity: sq.Pair.Commodity,
				Amount: ast.Amount{
					Number:   *v,
					Currency: sq.Pair.QuoteCurrency,
				},
			})
		}
	} else {
		missing = wanted
	}

	if len(missing) == 0 {
		return hits, diags, nil
	}

	env, err := s.fetchFeed(ctx, dailyFile)
	if err != nil {
		return nil, diags, err
	}
	diags = append(diags, s.populateCache(env)...)

	fresh, more := pickAllDates(env, missing)
	out := append(hits, fresh...)
	return out, append(diags, more...), nil
}

// observedLatestDate returns the most recent day any populateCache
// call has parsed. Zero time means no feed has been observed yet,
// which forces QuoteLatest into a fetch.
func (s *Source) observedLatestDate() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestDate
}

// QuoteAt returns prices for a specific calendar date. The 90-day feed
// is used when at is within the last 90 days of s.now(); otherwise the
// full historical feed is used.
func (s *Source) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	wanted, diags := classify(q)
	if len(wanted) == 0 {
		return nil, diags, nil
	}

	cache := s.quoteCache()

	// Build the timestamp that the resulting Price will carry. For
	// cache hits we use a normalised UTC midnight matching the parsed
	// feed; for misses the eventual fetch returns the same.
	atUTC := normaliseDay(at)

	hits := make([]ast.Price, 0, len(wanted))
	var missing []api.SourceQuery
	for _, sq := range wanted {
		ccy := resolveCcy(sq)
		// QC dimension is always "EUR": ECB publishes
		// EUR-base rates only.
		if v, ok := cache.GetAt(ecbBaseCurrency, ccy, at); ok {
			hits = append(hits, ast.Price{
				Date:      atUTC,
				Commodity: sq.Pair.Commodity,
				Amount: ast.Amount{
					Number:   *v,
					Currency: sq.Pair.QuoteCurrency,
				},
			})
			continue
		}
		missing = append(missing, sq)
	}

	if len(missing) == 0 {
		return hits, diags, nil
	}

	feed := s.feedForRange(at)
	env, err := s.fetchFeed(ctx, feed)
	if err != nil {
		return nil, diags, err
	}
	diags = append(diags, s.populateCache(env)...)

	fresh, more := pickOnDate(env, missing, at)
	out := append(hits, fresh...)
	return out, append(diags, more...), nil
}

// QuoteRange returns prices for the half-open interval [start, end).
// The 90-day feed is used when the entire range fits inside the 90-day
// window; otherwise the full historical feed is used.
//
// Before issuing a network fetch QuoteRange probes the per-Source
// QuoteCache for every (query, calendar-day) combination in
// [start, end). If every combination is already present (the typical
// case after an earlier QuoteAt or QuoteRange call against the same
// window) the result is assembled from the cache and no HTTP request
// is made. Any miss falls through to the existing fetch path; partial-
// day caching (fetch only the missing days) is intentionally out of
// scope — the ECB feed file already covers many days in one download
// so a full-window refetch costs little.
func (s *Source) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	wanted, diags := classify(q)
	if len(wanted) == 0 {
		return nil, diags, nil
	}

	cache := s.quoteCache()

	// Try to satisfy the entire window from the cache. We iterate
	// day-by-day across the half-open interval [start, end) so the
	// enumeration matches the convention used in pickInRange.
	startUTC := normaliseDay(start)
	endUTC := normaliseDay(end)
	allHit := true
	var cachedPrices []ast.Price
	for _, sq := range wanted {
		ccy := resolveCcy(sq)
		for d := startUTC; d.Before(endUTC); d = d.AddDate(0, 0, 1) {
			v, ok := cache.GetAt(ecbBaseCurrency, ccy, d)
			if !ok {
				allHit = false
				break
			}
			cachedPrices = append(cachedPrices, ast.Price{
				Date:      d,
				Commodity: sq.Pair.Commodity,
				Amount: ast.Amount{
					Number:   *v,
					Currency: sq.Pair.QuoteCurrency,
				},
			})
		}
		if !allHit {
			break
		}
	}
	if allHit {
		return cachedPrices, diags, nil
	}

	feed := s.feedForRange(start)
	env, err := s.fetchFeed(ctx, feed)
	if err != nil {
		return nil, diags, err
	}
	diags = append(diags, s.populateCache(env)...)
	prices, more := pickInRange(env, wanted, start, end)
	return prices, append(diags, more...), nil
}

// populateCache stores every (currency, day) rate in env into the
// per-Source cache. The most recent day's rates are also stored as
// "latest" entries so a follow-up QuoteLatest call hits without
// re-downloading the daily feed; that day is also recorded as the
// observed latestDate so later QuoteLatest hits can stamp the Price
// with the original publication date.
//
// Per-entry parse failures (a malformed time attribute or rate
// attribute) are reported as ecb-parse-error warning diagnostics and
// the offending entry is skipped; the rest of the feed still
// populates. Callers merge the returned diagnostics into their own
// slice so the orchestrator surfaces them to the user.
func (s *Source) populateCache(env *envelope) []ast.Diagnostic {
	cache := s.quoteCache()
	var diags []ast.Diagnostic
	var latestTS time.Time
	var latestRates []rateCube
	var latestDayStr string
	for _, day := range env.Cube.Days {
		ts, err := parseDay(day.Time)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:     "ecb-parse-error",
				Severity: ast.Warning,
				Message: fmt.Sprintf(
					"ecb: skipping feed day with invalid time attribute %q: %v",
					day.Time, err,
				),
			})
			continue
		}
		for _, r := range day.Rates {
			num, _, err := apd.NewFromString(r.Rate)
			if err != nil {
				diags = append(diags, ast.Diagnostic{
					Code:     "ecb-parse-error",
					Severity: ast.Warning,
					Message: fmt.Sprintf(
						"ecb: skipping rate %q for %s on %s: %v",
						r.Rate, r.Currency, day.Time, err,
					),
				})
				continue
			}
			// QC dimension is hardcoded to "EUR": ECB feeds
			// publish EUR-base rates only.
			cache.PutAt(ecbBaseCurrency, r.Currency, ts, num)
		}
		if ts.After(latestTS) {
			latestTS = ts
			latestRates = day.Rates
			latestDayStr = day.Time
		}
	}
	for _, r := range latestRates {
		num, _, err := apd.NewFromString(r.Rate)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:     "ecb-parse-error",
				Severity: ast.Warning,
				Message: fmt.Sprintf(
					"ecb: skipping latest-shape rate %q for %s on %s: %v",
					r.Rate, r.Currency, latestDayStr, err,
				),
			})
			continue
		}
		cache.PutLatest(ecbBaseCurrency, r.Currency, num)
	}
	if !latestTS.IsZero() {
		s.mu.Lock()
		if latestTS.After(s.latestDate) {
			s.latestDate = latestTS
		}
		s.mu.Unlock()
	}
	return diags
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

// normaliseDay returns 00:00 UTC of t's calendar date.
func normaliseDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
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
