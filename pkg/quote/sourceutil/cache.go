package sourceutil

import (
	"context"
	"sync"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// CacheOptions configures Cache.
type CacheOptions struct {
	// TTL is the per-entry time-to-live. A value of 0 means entries
	// never expire — appropriate for the default per-Fetch lifetime
	// model where the Cache is created fresh for each top-level
	// Fetch call. A non-zero TTL keys entries by insertion time and
	// lazily evicts on read.
	TTL time.Duration
	// MaxEntries caps the number of cached prices. A value of 0
	// means unbounded. When the cap is reached, the oldest entry
	// (by insertion time) is evicted to make room for a new one.
	MaxEntries int
}

// Cache memoises returned Price values keyed by (Pair, Symbol,
// date-or-range) so identical follow-up queries short-circuit the
// wrapped source. With separate maps per call shape (Latest / At /
// Range), a hit on one shape does not bleed into another. Each call
// splits its input queries into "already cached" and "missing"
// portions; only the missing portion is forwarded to the wrapped
// source. If every query is cached the wrapped source is not called
// at all.
//
// # Why caching
//
// Real-world rate-published sources like the ECB return many prices
// per call (a full reference-rate matrix for the day). Without a cache,
// follow-up calls for different pairs hitting the same source would
// re-fetch the same matrix. Cache stores every returned ast.Price keyed
// on (Date.UTC, Commodity, Amount.Currency) so a subsequent fallback
// level looking for a different pair on the same day finds the price
// in the cache and the wrapped source is not consulted again.
//
// # Partial fan-out
//
// When a batched query arrives, the cache splits it into already-
// known and missing entries and forwards only the missing ones in a
// single call to the wrapped source. The Phase 7 contract is that a
// Source must natively handle any-size batch; callers whose wrapped
// source needs per-entry splitting should stack SplitBatch(s, 1)
// underneath the cache. Per-call returned prices are written back
// into the cache before being merged with the hits and returned to
// the caller.
//
// # Range mode
//
// QuoteRange is cached on a coarser key: (Pair, Symbol, start.UTC,
// end.UTC) maps to the full slice of prices the wrapped source returned
// for that exact interval. Per-date partial fan-out is not attempted
// because a wrapped RangeSource's batching axis is the full interval;
// upstream callers can stack DateRangeIter to get per-day caching.
//
// # Lifetime
//
// The default model is one Cache instance per top-level Fetch call (or
// per CLI run). This keeps memory pressure bounded and makes the
// "subsequent fallback level reuses the matrix" optimisation work
// without leaking state across Fetch invocations. Cross-process
// persistence is explicitly out of scope.
//
// # TTL
//
// TTL=0 means entries never expire and is appropriate for the per-
// Fetch lifetime model. With a non-zero TTL, entries are stamped at
// insertion time and lazily evicted on read; an evicted entry is
// treated as a cache miss and forwarded.
//
// # Goroutine safety
//
// All cache state is protected by a single mutex; the returned source
// is safe for concurrent use. Stack a Concurrency decorator beneath
// Cache to bound calls to the wrapped source independently of the
// cache's internal serialisation.
//
// # Stacking
//
// Cache typically sits outermost: Cache(RateLimit(RetryOnError(s))).
// Outer placement maximises hit rate because cached responses short-
// circuit the entire downstream stack.
func Cache(s api.Source, opts CacheOptions) api.Source {
	c := &cacheSource{
		base:      s,
		opts:      opts,
		atEntries: map[atKey]*cacheEntry{},
		latest:    map[latestKey]*cacheEntry{},
		rng:       map[rangeKey]*rangeEntry{},
	}
	if l, ok := s.(api.LatestSource); ok {
		c.latestSrc = l
	}
	if a, ok := s.(api.AtSource); ok {
		c.atSrc = a
	}
	if r, ok := s.(api.RangeSource); ok {
		c.rngSrc = r
	}
	return c.asSource()
}

// atKey identifies one cached At entry. The day component is
// normalised to 0:00 UTC of the requested calendar date.
type atKey struct {
	day       time.Time
	commodity string
	currency  string
	symbol    string
}

// latestKey identifies one cached Latest entry. There is no date
// component; insertion time governs TTL eviction.
type latestKey struct {
	commodity string
	currency  string
	symbol    string
}

// rangeKey identifies one cached Range slice. Endpoints are normalised
// to 0:00 UTC of the corresponding calendar date.
type rangeKey struct {
	start     time.Time
	end       time.Time
	commodity string
	currency  string
	symbol    string
}

type cacheEntry struct {
	price    ast.Price
	inserted time.Time
}

type rangeEntry struct {
	prices   []ast.Price
	diags    []ast.Diagnostic
	inserted time.Time
}

type cacheSource struct {
	base      api.Source
	opts      CacheOptions
	latestSrc api.LatestSource
	atSrc     api.AtSource
	rngSrc    api.RangeSource

	mu        sync.Mutex
	atEntries map[atKey]*cacheEntry
	latest    map[latestKey]*cacheEntry
	rng       map[rangeKey]*rangeEntry
}

func (c *cacheSource) Name() string                   { return c.base.Name() }
func (c *cacheSource) Capabilities() api.Capabilities { return c.base.Capabilities() }

func normaliseDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func (c *cacheSource) expired(inserted time.Time, now time.Time) bool {
	if c.opts.TTL <= 0 {
		return false
	}
	return now.Sub(inserted) >= c.opts.TTL
}

// evictIfFull drops the oldest entry across all maps when MaxEntries
// is non-zero and the total count is at the cap. Caller holds c.mu.
func (c *cacheSource) evictIfFull() {
	if c.opts.MaxEntries <= 0 {
		return
	}
	for c.totalLocked() >= c.opts.MaxEntries {
		c.evictOldestLocked()
	}
}

func (c *cacheSource) totalLocked() int {
	return len(c.atEntries) + len(c.latest) + len(c.rng)
}

// evictOldestLocked removes the single oldest entry across all three
// maps. Caller holds c.mu. Used to enforce MaxEntries.
func (c *cacheSource) evictOldestLocked() {
	var (
		bestT    time.Time
		haveBest bool
		bestKind int // 0 at, 1 latest, 2 rng
		bestAtK  atKey
		bestLK   latestKey
		bestRK   rangeKey
	)
	for k, e := range c.atEntries {
		if !haveBest || e.inserted.Before(bestT) {
			bestT, bestAtK, bestKind, haveBest = e.inserted, k, 0, true
		}
	}
	for k, e := range c.latest {
		if !haveBest || e.inserted.Before(bestT) {
			bestT, bestLK, bestKind, haveBest = e.inserted, k, 1, true
		}
	}
	for k, e := range c.rng {
		if !haveBest || e.inserted.Before(bestT) {
			bestT, bestRK, bestKind, haveBest = e.inserted, k, 2, true
		}
	}
	if !haveBest {
		return
	}
	switch bestKind {
	case 0:
		delete(c.atEntries, bestAtK)
	case 1:
		delete(c.latest, bestLK)
	case 2:
		delete(c.rng, bestRK)
	}
}

// doAt splits q into hits and misses and forwards only the misses.
func (c *cacheSource) doAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	day := normaliseDay(at)
	now := time.Now()

	hits := make([]ast.Price, 0, len(q))
	missed := make([]api.SourceQuery, 0, len(q))

	c.mu.Lock()
	for _, query := range q {
		k := atKey{day: day, commodity: query.Pair.Commodity, currency: query.Pair.QuoteCurrency, symbol: query.Symbol}
		e, ok := c.atEntries[k]
		if !ok {
			missed = append(missed, query)
			continue
		}
		if c.expired(e.inserted, now) {
			delete(c.atEntries, k)
			missed = append(missed, query)
			continue
		}
		hits = append(hits, e.price)
	}
	c.mu.Unlock()

	if len(missed) == 0 {
		return hits, nil, nil
	}

	prices, diags, err := c.fetchAt(ctx, missed, at)
	if err != nil {
		return append(hits, prices...), diags, err
	}

	c.storeAtPrices(prices, day, missed)

	out := make([]ast.Price, 0, len(hits)+len(prices))
	out = append(out, hits...)
	out = append(out, prices...)
	return out, diags, nil
}

// fetchAt forwards missed queries to the wrapped AtSource in a
// single batched call. The Phase 7 contract is that a Source serves
// any-size batch natively; callers who need per-entry splitting wrap
// the underlying source in SplitBatch(s, 1).
func (c *cacheSource) fetchAt(ctx context.Context, missed []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.atSrc.QuoteAt(ctx, missed, at)
}

// storeAtPrices writes returned prices back into the cache. The
// authoritative key is (Date.UTC, Commodity, Amount.Currency); the
// queried Symbol is matched up by (commodity, currency) to fill in the
// symbol component so a follow-up query with the same symbol hits.
func (c *cacheSource) storeAtPrices(prices []ast.Price, day time.Time, missed []api.SourceQuery) {
	if len(prices) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range prices {
		// Find the symbol that produced this price, if any.
		sym := ""
		for _, m := range missed {
			if m.Pair.Commodity == p.Commodity && m.Pair.QuoteCurrency == p.Amount.Currency {
				sym = m.Symbol
				break
			}
		}
		k := atKey{
			day:       normaliseDay(p.Date),
			commodity: p.Commodity,
			currency:  p.Amount.Currency,
			symbol:    sym,
		}
		// Even if the source returned the day matching the
		// requested day, key on the price's own date so other
		// callers asking for that exact date hit.
		if k.day.IsZero() {
			k.day = day
		}
		c.evictIfFull()
		c.atEntries[k] = &cacheEntry{price: p, inserted: now}
	}
}

// doLatest mirrors doAt but without a date dimension.
func (c *cacheSource) doLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	now := time.Now()
	hits := make([]ast.Price, 0, len(q))
	missed := make([]api.SourceQuery, 0, len(q))

	c.mu.Lock()
	for _, query := range q {
		k := latestKey{commodity: query.Pair.Commodity, currency: query.Pair.QuoteCurrency, symbol: query.Symbol}
		e, ok := c.latest[k]
		if !ok {
			missed = append(missed, query)
			continue
		}
		if c.expired(e.inserted, now) {
			delete(c.latest, k)
			missed = append(missed, query)
			continue
		}
		hits = append(hits, e.price)
	}
	c.mu.Unlock()

	if len(missed) == 0 {
		return hits, nil, nil
	}

	prices, diags, err := c.fetchLatest(ctx, missed)
	if err != nil {
		return append(hits, prices...), diags, err
	}

	c.storeLatestPrices(prices, missed)

	out := make([]ast.Price, 0, len(hits)+len(prices))
	out = append(out, hits...)
	out = append(out, prices...)
	return out, diags, nil
}

func (c *cacheSource) fetchLatest(ctx context.Context, missed []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return c.latestSrc.QuoteLatest(ctx, missed)
}

func (c *cacheSource) storeLatestPrices(prices []ast.Price, missed []api.SourceQuery) {
	if len(prices) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range prices {
		sym := ""
		for _, m := range missed {
			if m.Pair.Commodity == p.Commodity && m.Pair.QuoteCurrency == p.Amount.Currency {
				sym = m.Symbol
				break
			}
		}
		k := latestKey{commodity: p.Commodity, currency: p.Amount.Currency, symbol: sym}
		c.evictIfFull()
		c.latest[k] = &cacheEntry{price: p, inserted: now}
	}
}

// doRange caches per (Pair, Symbol, start, end). Range is the wrapped
// source's batching axis so per-date splitting is intentionally not
// attempted at this layer.
func (c *cacheSource) doRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	startDay := normaliseDay(start)
	endDay := normaliseDay(end)
	now := time.Now()
	hitPrices := make([]ast.Price, 0)
	hitDiags := make([]ast.Diagnostic, 0)
	missed := make([]api.SourceQuery, 0, len(q))

	c.mu.Lock()
	for _, query := range q {
		k := rangeKey{start: startDay, end: endDay, commodity: query.Pair.Commodity, currency: query.Pair.QuoteCurrency, symbol: query.Symbol}
		e, ok := c.rng[k]
		if !ok {
			missed = append(missed, query)
			continue
		}
		if c.expired(e.inserted, now) {
			delete(c.rng, k)
			missed = append(missed, query)
			continue
		}
		hitPrices = append(hitPrices, e.prices...)
		hitDiags = append(hitDiags, e.diags...)
	}
	c.mu.Unlock()

	if len(missed) == 0 {
		return hitPrices, hitDiags, nil
	}

	prices, diags, err := c.rngSrc.QuoteRange(ctx, missed, start, end)
	if err != nil {
		out := append([]ast.Price{}, hitPrices...)
		out = append(out, prices...)
		outDiags := append([]ast.Diagnostic{}, hitDiags...)
		outDiags = append(outDiags, diags...)
		return out, outDiags, err
	}

	c.storeRangePrices(missed, startDay, endDay, prices, diags)

	out := make([]ast.Price, 0, len(hitPrices)+len(prices))
	out = append(out, hitPrices...)
	out = append(out, prices...)
	outDiags := make([]ast.Diagnostic, 0, len(hitDiags)+len(diags))
	outDiags = append(outDiags, hitDiags...)
	outDiags = append(outDiags, diags...)
	return out, outDiags, nil
}

// storeRangePrices groups returned prices by (Commodity, Currency) and
// writes one rangeEntry per missed query so a follow-up identical
// range fetches all of its prices in one cache hit.
func (c *cacheSource) storeRangePrices(missed []api.SourceQuery, startDay, endDay time.Time, prices []ast.Price, diags []ast.Diagnostic) {
	now := time.Now()
	// Group prices by (commodity, currency) so a follow-up identical-
	// range fetch finds all of its prices in one cache hit.
	grouped := map[latestKey][]ast.Price{}
	for _, p := range prices {
		k := latestKey{commodity: p.Commodity, currency: p.Amount.Currency}
		grouped[k] = append(grouped[k], p)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Diagnostics are not split per query by the source; we attach the
	// full diag slice to the first stored entry only so later identical-
	// range hits surface the original diagnostics without duplicating
	// them across every cached entry.
	pendingDiags := diags
	for _, m := range missed {
		gk := latestKey{commodity: m.Pair.Commodity, currency: m.Pair.QuoteCurrency}
		ps := grouped[gk]
		k := rangeKey{
			start:     startDay,
			end:       endDay,
			commodity: m.Pair.Commodity,
			currency:  m.Pair.QuoteCurrency,
			symbol:    m.Symbol,
		}
		c.evictIfFull()
		c.rng[k] = &rangeEntry{prices: ps, diags: pendingDiags, inserted: now}
		pendingDiags = nil
	}
}

// asSource returns c typed against whichever sub-interface combination
// the wrapped source satisfies, so a downstream type assertion
// recovers the original axes.
func (c *cacheSource) asSource() api.Source {
	hasL := c.latestSrc != nil
	hasA := c.atSrc != nil
	hasR := c.rngSrc != nil
	switch {
	case hasL && hasA && hasR:
		return cacheLatestAtRange{c}
	case hasL && hasA:
		return cacheLatestAt{c}
	case hasL && hasR:
		return cacheLatestRange{c}
	case hasA && hasR:
		return cacheAtRange{c}
	case hasL:
		return cacheLatestOnly{c}
	case hasA:
		return cacheAtOnly{c}
	case hasR:
		return cacheRangeOnly{c}
	default:
		return cacheBaseOnly{c}
	}
}

type cacheBaseOnly struct{ *cacheSource }

type cacheLatestOnly struct{ *cacheSource }

func (c cacheLatestOnly) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doLatest(ctx, q)
}

type cacheAtOnly struct{ *cacheSource }

func (c cacheAtOnly) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doAt(ctx, q, at)
}

type cacheRangeOnly struct{ *cacheSource }

func (c cacheRangeOnly) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doRange(ctx, q, start, end)
}

type cacheLatestAt struct{ *cacheSource }

func (c cacheLatestAt) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doLatest(ctx, q)
}
func (c cacheLatestAt) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doAt(ctx, q, at)
}

type cacheLatestRange struct{ *cacheSource }

func (c cacheLatestRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doLatest(ctx, q)
}
func (c cacheLatestRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doRange(ctx, q, start, end)
}

type cacheAtRange struct{ *cacheSource }

func (c cacheAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doAt(ctx, q, at)
}
func (c cacheAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doRange(ctx, q, start, end)
}

type cacheLatestAtRange struct{ *cacheSource }

func (c cacheLatestAtRange) QuoteLatest(ctx context.Context, q []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doLatest(ctx, q)
}
func (c cacheLatestAtRange) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doAt(ctx, q, at)
}
func (c cacheLatestAtRange) QuoteRange(ctx context.Context, q []api.SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return c.cacheSource.doRange(ctx, q, start, end)
}
