// Package price provides the immutable, query-wide context that BQL
// price/valuation functions read. Its central type [Map] indexes a ledger's
// Price directives for nearest-on-or-before-date lookup, and [QueryContext]
// bundles that map (and, in future, other init-time directive maps) into the
// read-only context injected into scalar function evaluation.
package price

import (
	"sort"
	"sync"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// quoContext divides at decimal128 precision, matching the repository-wide
// convention for non-exact division (see pkg/ast/cost.go, pkg/inventory).
var quoContext = apd.BaseContext.WithPrecision(34)

// Map is an immutable price database derived from a ledger's Price
// directives. The index is built once, lazily, on the first lookup (via
// sync.Once); after that the stored rates never change, so a Map is safe for
// concurrent read-only use by many goroutines (Decision 6). A query that
// never calls a price function therefore never pays the indexing cost.
//
// Lookups are nearest-on-or-before-date with a one-hop inverse fallback: a
// base→quote query consults the directly indexed rates first, then the
// inverse (1/rate) of the quote→base rates. base == quote yields 1. Multi-hop
// transitive conversion is intentionally not performed.
type Map struct {
	ledger *ast.Ledger
	once   sync.Once
	rates  map[string]map[string][]datedRate
}

// datedRate is one observed base→quote rate effective from date.
type datedRate struct {
	date time.Time
	rate apd.Decimal
}

// NewMap returns a Map over ledger. It does not scan the ledger; the price
// index is built lazily on the first Get or Latest call. A nil ledger yields
// an empty map (every lookup misses).
func NewMap(ledger *ast.Ledger) *Map {
	return &Map{ledger: ledger}
}

func (m *Map) build() {
	m.rates = map[string]map[string][]datedRate{}
	if m.ledger == nil {
		return
	}
	for _, dir := range m.ledger.All() {
		p, ok := dir.(*ast.Price)
		if !ok {
			continue
		}
		m.insert(p.Commodity, p.Amount.Currency, p.Date, p.Amount.Number)
	}
	for _, byQuote := range m.rates {
		for _, list := range byQuote {
			sort.SliceStable(list, func(i, j int) bool { return list[i].date.Before(list[j].date) })
		}
	}
}

func (m *Map) insert(base, quote string, d time.Time, rate apd.Decimal) {
	byQuote := m.rates[base]
	if byQuote == nil {
		byQuote = map[string][]datedRate{}
		m.rates[base] = byQuote
	}
	byQuote[quote] = append(byQuote[quote], datedRate{date: d, rate: *ast.CloneDecimal(&rate)})
}

// Get returns the base→quote rate effective on or before date, and whether
// one exists. It tries the direct rate, then the one-hop inverse of the
// quote→base rate; base == quote returns 1. The returned decimal is an
// independent copy.
func (m *Map) Get(base, quote string, date time.Time) (apd.Decimal, bool) {
	m.once.Do(m.build)
	if base == quote {
		return one(), true
	}
	if r, ok := atOrBefore(m.rates[base][quote], date); ok {
		return r, true
	}
	if r, ok := atOrBefore(m.rates[quote][base], date); ok {
		return invert(r)
	}
	return apd.Decimal{}, false
}

// Latest returns the most recent base→quote rate, and whether one exists,
// using the same direct-then-inverse rule as Get. The returned decimal is an
// independent copy.
func (m *Map) Latest(base, quote string) (apd.Decimal, bool) {
	m.once.Do(m.build)
	if base == quote {
		return one(), true
	}
	if list := m.rates[base][quote]; len(list) > 0 {
		return *ast.CloneDecimal(&list[len(list)-1].rate), true
	}
	if list := m.rates[quote][base]; len(list) > 0 {
		return invert(list[len(list)-1].rate)
	}
	return apd.Decimal{}, false
}

// atOrBefore returns the rate of the latest entry dated on or before date in
// an ascending-by-date list, and whether one exists.
func atOrBefore(list []datedRate, date time.Time) (apd.Decimal, bool) {
	i := sort.Search(len(list), func(i int) bool { return list[i].date.After(date) })
	if i == 0 {
		return apd.Decimal{}, false
	}
	return *ast.CloneDecimal(&list[i-1].rate), true
}

func invert(rate apd.Decimal) (apd.Decimal, bool) {
	if rate.IsZero() {
		return apd.Decimal{}, false
	}
	o := one()
	q, err := ast.QuoNormalized(quoContext, &o, &rate)
	if err != nil {
		return apd.Decimal{}, false
	}
	return *q, true
}

func one() apd.Decimal { return *apd.New(1, 0) }
