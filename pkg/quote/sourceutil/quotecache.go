package sourceutil

import (
	"sync"
	"time"
)

// QuoteCacheOptions configures a [QuoteCache].
//
// TTL of zero or below disables expiry; entries live until the cache
// is dropped (the typical per-Fetch lifetime model). MaxEntries of
// zero or below disables the cap; otherwise the cache evicts the
// oldest entry (by insertion order, across all three shape buckets)
// when a Put would exceed the cap.
//
// Now is the clock used to stamp insertion times and evaluate TTL.
// If nil, [time.Now] is used. The field is primarily a test seam:
// production code should leave it zero.
type QuoteCacheOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
}

// QuoteCache memoises Source-internal price values by source-physical
// key — (QuoteCurrency, Symbol, time qualifier) — for use inside a
// Source implementation.
//
// Use this when your source's underlying API returns more data than
// the caller asked for (a "windfall"): a single call covering one
// symbol may return prices for several symbols on the same day. The
// orchestrator's [pkg/quote/api.SourceQuery] for a given level only
// carries the symbol→commodity mapping the caller is currently
// asking about, so the cache cannot live as a decorator outside the
// source — by the time a windfall entry arrives, its beancount
// commodity name is not yet known. Storing the values inside the
// source, keyed on the source-physical (QuoteCurrency, Symbol)
// addressing units that are stable across calls, lets a later level
// translate (Symbol, V) into an [pkg/ast.Price] using the inbound
// SourceQuery.Pair.Commodity.
//
// The value type V is whatever atomic unit the source tracks per
// quote — commonly *apd.Decimal, but a struct is appropriate for
// sources that carry several fields per price.
//
// # Three shape buckets
//
// QuoteCache exposes three Get/Put pairs corresponding to the three
// time-shape axes a Source may serve:
//
//   - Latest:  GetLatest(qc, sym) / PutLatest(qc, sym, v)
//   - At:      GetAt(qc, sym, day) / PutAt(qc, sym, day, v)
//   - Range:   GetRange(qc, sym, start, end) / PutRange(qc, sym, start, end, v)
//
// Each bucket has its own internal map; a Put on one shape does not
// affect lookups on another shape. day, start, end values are
// converted to UTC and truncated to the calendar day before keying
// so that callers passing different time-zone-equivalent values
// still hit the same entry.
//
// # Goroutine safety
//
// All operations take a single internal mutex; the cache is safe for
// concurrent use from multiple goroutines.
//
// # Lifetime
//
// Source authors typically construct one QuoteCache per Source value
// and let it live as long as the Source itself, or one per top-level
// call if cross-call reuse is undesirable. The cache holds no
// goroutines and no descriptors, so dropping it is sufficient cleanup.
type QuoteCache[V any] struct {
	opts QuoteCacheOptions
	now  func() time.Time

	mu      sync.Mutex
	latest  map[latestCacheKey]quoteCacheEntry[V]
	at      map[atCacheKey]quoteCacheEntry[V]
	rng     map[rangeCacheKey]quoteCacheEntry[V]
	counter uint64 // monotonic insertion counter for eviction
}

// latestCacheKey identifies one cached Latest entry.
type latestCacheKey struct {
	qc  string
	sym string
}

// atCacheKey identifies one cached At entry. day is normalised to
// 00:00 UTC of the requested calendar date.
type atCacheKey struct {
	qc  string
	sym string
	day time.Time
}

// rangeCacheKey identifies one cached Range entry. start and end
// are normalised to 00:00 UTC of the corresponding calendar dates.
type rangeCacheKey struct {
	qc    string
	sym   string
	start time.Time
	end   time.Time
}

// quoteCacheEntry carries the cached value plus metadata for TTL
// expiry and eviction ordering.
type quoteCacheEntry[V any] struct {
	value      V
	insertedAt time.Time
	seq        uint64
}

// NewQuoteCache returns an empty [QuoteCache] configured per opts.
func NewQuoteCache[V any](opts QuoteCacheOptions) *QuoteCache[V] {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &QuoteCache[V]{
		opts:   opts,
		now:    now,
		latest: map[latestCacheKey]quoteCacheEntry[V]{},
		at:     map[atCacheKey]quoteCacheEntry[V]{},
		rng:    map[rangeCacheKey]quoteCacheEntry[V]{},
	}
}

// normaliseCacheDay returns 00:00 UTC of t's calendar date.
func normaliseCacheDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// expired reports whether an entry has aged past the configured TTL.
// Caller holds c.mu.
func (c *QuoteCache[V]) expired(e quoteCacheEntry[V]) bool {
	if c.opts.TTL <= 0 {
		return false
	}
	return c.now().Sub(e.insertedAt) >= c.opts.TTL
}

// nextSeq returns a fresh insertion-order number. Caller holds c.mu.
func (c *QuoteCache[V]) nextSeq() uint64 {
	c.counter++
	return c.counter
}

// totalLocked returns the count of entries across all buckets.
// Caller holds c.mu.
func (c *QuoteCache[V]) totalLocked() int {
	return len(c.latest) + len(c.at) + len(c.rng)
}

// evictIfFull drops the oldest entry across all maps when MaxEntries
// is set and the total count is at the cap. Caller holds c.mu.
func (c *QuoteCache[V]) evictIfFull() {
	if c.opts.MaxEntries <= 0 {
		return
	}
	for c.totalLocked() >= c.opts.MaxEntries {
		if !c.evictOldestLocked() {
			return
		}
	}
}

// evictOldestLocked removes the single oldest entry across all three
// maps, ordered by insertion sequence. Caller holds c.mu. Returns
// false if there were no entries to evict.
func (c *QuoteCache[V]) evictOldestLocked() bool {
	var (
		bestSeq  uint64
		haveBest bool
		bestKind int // 0 latest, 1 at, 2 rng
		bestLK   latestCacheKey
		bestAK   atCacheKey
		bestRK   rangeCacheKey
	)
	for k, e := range c.latest {
		if !haveBest || e.seq < bestSeq {
			bestSeq, bestLK, bestKind, haveBest = e.seq, k, 0, true
		}
	}
	for k, e := range c.at {
		if !haveBest || e.seq < bestSeq {
			bestSeq, bestAK, bestKind, haveBest = e.seq, k, 1, true
		}
	}
	for k, e := range c.rng {
		if !haveBest || e.seq < bestSeq {
			bestSeq, bestRK, bestKind, haveBest = e.seq, k, 2, true
		}
	}
	if !haveBest {
		return false
	}
	switch bestKind {
	case 0:
		delete(c.latest, bestLK)
	case 1:
		delete(c.at, bestAK)
	case 2:
		delete(c.rng, bestRK)
	}
	return true
}

// GetLatest returns the cached "latest" value for (qc, sym), if any.
func (c *QuoteCache[V]) GetLatest(qc, sym string) (V, bool) {
	var zero V
	c.mu.Lock()
	defer c.mu.Unlock()
	k := latestCacheKey{qc: qc, sym: sym}
	e, ok := c.latest[k]
	if !ok {
		return zero, false
	}
	if c.expired(e) {
		delete(c.latest, k)
		return zero, false
	}
	return e.value, true
}

// PutLatest stores v as the "latest" value for (qc, sym).
func (c *QuoteCache[V]) PutLatest(qc, sym string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfFull()
	c.latest[latestCacheKey{qc: qc, sym: sym}] = quoteCacheEntry[V]{
		value:      v,
		insertedAt: c.now(),
		seq:        c.nextSeq(),
	}
}

// GetAt returns the cached "as-of-day" value for (qc, sym, day). day
// is normalised to its UTC calendar date.
func (c *QuoteCache[V]) GetAt(qc, sym string, day time.Time) (V, bool) {
	var zero V
	c.mu.Lock()
	defer c.mu.Unlock()
	k := atCacheKey{qc: qc, sym: sym, day: normaliseCacheDay(day)}
	e, ok := c.at[k]
	if !ok {
		return zero, false
	}
	if c.expired(e) {
		delete(c.at, k)
		return zero, false
	}
	return e.value, true
}

// PutAt stores v as the "as-of-day" value for (qc, sym, day).
func (c *QuoteCache[V]) PutAt(qc, sym string, day time.Time, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfFull()
	c.at[atCacheKey{qc: qc, sym: sym, day: normaliseCacheDay(day)}] = quoteCacheEntry[V]{
		value:      v,
		insertedAt: c.now(),
		seq:        c.nextSeq(),
	}
}

// GetRange returns the cached "range" value for (qc, sym, start, end).
// start and end are normalised to UTC calendar dates.
func (c *QuoteCache[V]) GetRange(qc, sym string, start, end time.Time) (V, bool) {
	var zero V
	c.mu.Lock()
	defer c.mu.Unlock()
	k := rangeCacheKey{
		qc:    qc,
		sym:   sym,
		start: normaliseCacheDay(start),
		end:   normaliseCacheDay(end),
	}
	e, ok := c.rng[k]
	if !ok {
		return zero, false
	}
	if c.expired(e) {
		delete(c.rng, k)
		return zero, false
	}
	return e.value, true
}

// PutRange stores v as the "range" value for (qc, sym, start, end).
func (c *QuoteCache[V]) PutRange(qc, sym string, start, end time.Time, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfFull()
	c.rng[rangeCacheKey{
		qc:    qc,
		sym:   sym,
		start: normaliseCacheDay(start),
		end:   normaliseCacheDay(end),
	}] = quoteCacheEntry[V]{
		value:      v,
		insertedAt: c.now(),
		seq:        c.nextSeq(),
	}
}
