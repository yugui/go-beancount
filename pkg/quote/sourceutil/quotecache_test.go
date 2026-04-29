package sourceutil

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
)

// decimalOf builds an apd.Decimal pointer from a literal for tests.
func decimalOf(t *testing.T, s string) *apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return d
}

func TestQuoteCache_LatestRoundTrip(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})

	if _, ok := c.GetLatest("USD", "EUR"); ok {
		t.Errorf("GetLatest on empty cache returned ok=true")
	}

	v := decimalOf(t, "1.0823")
	c.PutLatest("USD", "EUR", v)

	got, ok := c.GetLatest("USD", "EUR")
	if !ok {
		t.Fatalf("GetLatest after PutLatest returned ok=false")
	}
	if got.String() != "1.0823" {
		t.Errorf("got %s, want 1.0823", got.String())
	}

	// A different (qc, sym) is unaffected.
	if _, ok := c.GetLatest("USD", "JPY"); ok {
		t.Errorf("GetLatest on unrelated key returned ok=true")
	}
	if _, ok := c.GetLatest("JPY", "EUR"); ok {
		t.Errorf("GetLatest on different qc returned ok=true")
	}
}

func TestQuoteCache_AtRoundTrip(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})
	day := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)

	if _, ok := c.GetAt("USD", "EUR", day); ok {
		t.Errorf("GetAt on empty cache returned ok=true")
	}

	v := decimalOf(t, "1.09")
	c.PutAt("USD", "EUR", day, v)

	got, ok := c.GetAt("USD", "EUR", day)
	if !ok {
		t.Fatalf("GetAt after PutAt returned ok=false")
	}
	if got.String() != "1.09" {
		t.Errorf("got %s, want 1.09", got.String())
	}

	// A time-zone-equivalent day in UTC must hit the same entry: a
	// non-UTC time whose UTC calendar date matches day must also hit.
	// 2024-06-03 09:00 in JST (UTC+9) is 2024-06-03 00:00 UTC.
	jst := time.FixedZone("JST", 9*3600)
	sameDayJST := time.Date(2024, 6, 3, 9, 0, 0, 0, jst)
	if _, ok := c.GetAt("USD", "EUR", sameDayJST); !ok {
		t.Errorf("GetAt on time-zone-equivalent day did not hit")
	}

	// And a time within day but later than 00:00 UTC (still same UTC
	// calendar date) must hit too.
	laterSameDay := time.Date(2024, 6, 3, 17, 30, 0, 0, time.UTC)
	if _, ok := c.GetAt("USD", "EUR", laterSameDay); !ok {
		t.Errorf("GetAt on later-same-day did not hit")
	}

	// A different calendar day must miss.
	other := time.Date(2024, 6, 4, 0, 0, 0, 0, time.UTC)
	if _, ok := c.GetAt("USD", "EUR", other); ok {
		t.Errorf("GetAt on different day returned ok=true")
	}
}

func TestQuoteCache_RangeRoundTrip(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	if _, ok := c.GetRange("USD", "EUR", start, end); ok {
		t.Errorf("GetRange on empty cache returned ok=true")
	}

	v := decimalOf(t, "1.05")
	c.PutRange("USD", "EUR", start, end, v)

	got, ok := c.GetRange("USD", "EUR", start, end)
	if !ok {
		t.Fatalf("GetRange after PutRange returned ok=false")
	}
	if got.String() != "1.05" {
		t.Errorf("got %s, want 1.05", got.String())
	}

	// Time-zone-equivalent endpoints hit the same entry.
	jst := time.FixedZone("JST", 9*3600)
	startJST := time.Date(2024, 1, 1, 9, 0, 0, 0, jst)
	endJST := time.Date(2024, 1, 7, 9, 0, 0, 0, jst)
	if _, ok := c.GetRange("USD", "EUR", startJST, endJST); !ok {
		t.Errorf("GetRange on tz-equivalent endpoints did not hit")
	}

	// A different end date misses.
	endOther := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC)
	if _, ok := c.GetRange("USD", "EUR", start, endOther); ok {
		t.Errorf("GetRange on different end returned ok=true")
	}
}

func TestQuoteCache_ShapeIsolation(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})
	day := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)

	v := decimalOf(t, "1")
	c.PutLatest("USD", "EUR", v)
	if _, ok := c.GetAt("USD", "EUR", day); ok {
		t.Errorf("PutLatest leaked into GetAt")
	}
	if _, ok := c.GetRange("USD", "EUR", day, end); ok {
		t.Errorf("PutLatest leaked into GetRange")
	}

	c2 := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})
	c2.PutAt("USD", "EUR", day, v)
	if _, ok := c2.GetLatest("USD", "EUR"); ok {
		t.Errorf("PutAt leaked into GetLatest")
	}
	if _, ok := c2.GetRange("USD", "EUR", day, end); ok {
		t.Errorf("PutAt leaked into GetRange")
	}

	c3 := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})
	c3.PutRange("USD", "EUR", day, end, v)
	if _, ok := c3.GetLatest("USD", "EUR"); ok {
		t.Errorf("PutRange leaked into GetLatest")
	}
	if _, ok := c3.GetAt("USD", "EUR", day); ok {
		t.Errorf("PutRange leaked into GetAt")
	}
}

func TestQuoteCache_TTL(t *testing.T) {
	// Inject a controllable clock so the test is deterministic.
	var cur atomic.Int64
	cur.Store(int64(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()))
	now := func() time.Time { return time.Unix(0, cur.Load()).UTC() }
	advance := func(d time.Duration) { cur.Add(int64(d)) }

	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{
		TTL: 50 * time.Millisecond,
		Now: now,
	})
	day := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)

	c.PutAt("USD", "EUR", day, decimalOf(t, "1"))
	c.PutLatest("USD", "EUR", decimalOf(t, "1"))
	c.PutRange("USD", "EUR", day, day, decimalOf(t, "1"))

	// Within TTL: hits.
	advance(20 * time.Millisecond)
	if _, ok := c.GetAt("USD", "EUR", day); !ok {
		t.Errorf("GetAt within TTL: ok=false")
	}
	if _, ok := c.GetLatest("USD", "EUR"); !ok {
		t.Errorf("GetLatest within TTL: ok=false")
	}
	if _, ok := c.GetRange("USD", "EUR", day, day); !ok {
		t.Errorf("GetRange within TTL: ok=false")
	}

	// Past TTL: misses.
	advance(100 * time.Millisecond)
	if _, ok := c.GetAt("USD", "EUR", day); ok {
		t.Errorf("GetAt past TTL: ok=true")
	}
	if _, ok := c.GetLatest("USD", "EUR"); ok {
		t.Errorf("GetLatest past TTL: ok=true")
	}
	if _, ok := c.GetRange("USD", "EUR", day, day); ok {
		t.Errorf("GetRange past TTL: ok=true")
	}
}

func TestQuoteCache_MaxEntries(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{MaxEntries: 10})

	// Insert 10 distinct entries.
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	syms := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"}
	for _, sym := range syms {
		c.PutAt("USD", sym, day, decimalOf(t, "1"))
	}
	// All ten should still be present.
	for _, sym := range syms {
		if _, ok := c.GetAt("USD", sym, day); !ok {
			t.Errorf("entry %s evicted prematurely", sym)
		}
	}

	// Insert an eleventh: the oldest ("A") must be evicted.
	c.PutAt("USD", "K", day, decimalOf(t, "1"))
	if _, ok := c.GetAt("USD", "A", day); ok {
		t.Errorf("oldest entry A was not evicted at cap")
	}
	// The other nine plus K survive.
	for _, sym := range append([]string{"B", "C", "D", "E", "F", "G", "H", "I", "J"}, "K") {
		if _, ok := c.GetAt("USD", sym, day); !ok {
			t.Errorf("entry %s should still be present", sym)
		}
	}
}

func TestQuoteCache_Concurrent(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})

	const goroutines = 16
	const opsPerGoroutine = 200

	// Pre-allocate the decimal value once on the test goroutine.
	// Calling decimalOf inside the spawned goroutines would invoke
	// t.Fatalf from a non-test goroutine on parse error, which
	// runtime.Goexit()s the wrong goroutine and silently drops the
	// failure. The QuoteCache stores values opaquely, so a single
	// shared *apd.Decimal is safe.
	one := apd.New(1, 0)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				sym := string(rune('A' + g))
				switch i % 3 {
				case 0:
					c.PutLatest("USD", sym, one)
					c.GetLatest("USD", sym)
				case 1:
					day := time.Date(2024, 1, 1+i%28, 0, 0, 0, 0, time.UTC)
					c.PutAt("USD", sym, day, one)
					c.GetAt("USD", sym, day)
				case 2:
					start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
					end := time.Date(2024, 1, 1+i%28, 0, 0, 0, 0, time.UTC)
					c.PutRange("USD", sym, start, end, one)
					c.GetRange("USD", sym, start, end)
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestQuoteCache_WindfallPattern emulates the use case the cache is
// designed for: a single source call returns prices for symbols beyond
// what was queried. The source populates the cache for every entry the
// underlying API returned (keyed on the source-physical (qc, sym, day)
// units that are stable across calls); a follow-up call that asks for
// one of the windfall symbols then hits the cache without consulting
// the upstream.
func TestQuoteCache_WindfallPattern(t *testing.T) {
	c := NewQuoteCache[*apd.Decimal](QuoteCacheOptions{})
	day := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)

	// First call asked for (EUR, USD). Source fetched the daily feed
	// which returned EUR-base rates against USD, JPY, GBP. The source
	// caches all three on (qc=EUR, sym=USD/JPY/GBP, day).
	c.PutAt("EUR", "USD", day, decimalOf(t, "1.0823"))
	c.PutAt("EUR", "JPY", day, decimalOf(t, "163.5"))
	c.PutAt("EUR", "GBP", day, decimalOf(t, "0.85"))

	// Later: a SourceQuery for (EUR, JPY) arrives. The source hits the
	// cache without re-fetching.
	got, ok := c.GetAt("EUR", "JPY", day)
	if !ok {
		t.Fatalf("windfall cache miss for JPY")
	}
	if got.String() != "163.5" {
		t.Errorf("JPY windfall value = %s, want 163.5", got.String())
	}

	// Same for GBP.
	got, ok = c.GetAt("EUR", "GBP", day)
	if !ok {
		t.Fatalf("windfall cache miss for GBP")
	}
	if got.String() != "0.85" {
		t.Errorf("GBP windfall value = %s, want 0.85", got.String())
	}

	// And the originally-queried USD also hits.
	got, ok = c.GetAt("EUR", "USD", day)
	if !ok {
		t.Fatalf("originally-queried USD missing")
	}
	if got.String() != "1.0823" {
		t.Errorf("USD value = %s, want 1.0823", got.String())
	}
}
