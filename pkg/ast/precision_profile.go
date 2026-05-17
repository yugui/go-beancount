package ast

import (
	"sort"

	"github.com/cockroachdb/apd/v3"
)

// PrecisionProfile records, per currency, the distribution of fractional-digit
// counts observed from decimal amounts. Use [NewPrecisionProfile] to construct;
// the zero value is not usable. Safe for single-goroutine use only.
type PrecisionProfile struct {
	counts map[string]map[int]int
}

// NewPrecisionProfile returns an empty, ready-to-use PrecisionProfile.
func NewPrecisionProfile() *PrecisionProfile {
	return &PrecisionProfile{counts: make(map[string]map[int]int)}
}

// Update records one observation of d for currency. No-op when d is nil or
// currency is empty. The contributed precision is max(0, -d.Exponent).
func (p *PrecisionProfile) Update(d *apd.Decimal, currency string) {
	if d == nil || currency == "" {
		return
	}
	prec := 0
	if d.Exponent < 0 {
		prec = int(-d.Exponent)
	}
	if p.counts[currency] == nil {
		p.counts[currency] = make(map[int]int)
	}
	p.counts[currency][prec]++
}

// MostCommon returns the most-frequent fractional-digit count for currency and
// ok=true. Returns (0, false) when currency has no observations. On a tie in
// frequency, the highest precision wins.
func (p *PrecisionProfile) MostCommon(currency string) (int, bool) {
	if p == nil {
		return 0, false
	}
	dist, ok := p.counts[currency]
	if !ok {
		return 0, false
	}
	best, bestCount := 0, 0
	for prec, count := range dist {
		if count > bestCount || (count == bestCount && prec > best) {
			best, bestCount = prec, count
		}
	}
	return best, true
}

// Currencies returns a sorted list of currencies with at least one observation.
// Returns nil when p is nil. Each call returns a fresh slice.
func (p *PrecisionProfile) Currencies() []string {
	if p == nil {
		return nil
	}
	if len(p.counts) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.counts))
	for c := range p.counts {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
