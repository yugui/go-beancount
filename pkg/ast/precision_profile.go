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

// Precision returns the display precision (fractional-digit count) for
// currency and ok=true. The chosen precision is the one most frequently
// observed; on a tie, the higher precision wins. Returns (0, false) when
// currency has no observations.
func (p *PrecisionProfile) Precision(currency string) (int, bool) {
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

// observeLedger builds a PrecisionProfile from the transaction posting amounts,
// balance amounts, and price amounts in ledger. Cost amounts and posting price
// annotations are excluded, matching upstream beancount's dcontext behavior.
func observeLedger(ledger *Ledger) *PrecisionProfile {
	p := NewPrecisionProfile()
	for _, d := range ledger.All() {
		switch v := d.(type) {
		case *Transaction:
			for _, posting := range v.Postings {
				if a := posting.Amount; a != nil {
					p.Update(&a.Number, a.Currency)
				}
			}
		case *Balance:
			p.Update(&v.Amount.Number, v.Amount.Currency)
		case *Price:
			p.Update(&v.Amount.Number, v.Amount.Currency)
		}
	}
	return p
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
