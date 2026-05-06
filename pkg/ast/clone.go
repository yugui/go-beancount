package ast

import "github.com/cockroachdb/apd/v3"

// This file provides Clone methods for AST node types whose nested
// pointers and apd.Decimal values would otherwise alias the original
// when copied via plain struct assignment. The deep/shallow boundary
// is deliberate:
//
//   - Postings are deep-cloned: a Transaction's Postings slice is
//     reallocated, and each Posting's Amount, Cost, and Price pointers
//     are deep-copied (including their nested apd.Decimal values and
//     CostSpec.Date) so a clone may be mutated freely.
//   - Tags, Links, and Meta are shared by convention. The codebase
//     treats these as immutable after construction; no consumer
//     mutates them in place, so reallocating them on every clone
//     would be wasteful.
//   - Span, Date, Flag, Payee, Narration, Account, Currency, Label
//     are scalars or small value types; they are copied by value.
//
// Each Clone method on a pointer receiver is nil-safe: calling Clone
// on a nil pointer returns nil. This matches the pattern established
// by inventory.Cost.Clone and lets callers chain clones across
// optional fields without nil checks.

// Clone returns a deep copy of t. The Postings slice is reallocated
// and each Posting deep-cloned; Tags, Links, Meta and scalar fields
// are shared with the receiver. Returns nil if t is nil.
func (t *Transaction) Clone() *Transaction {
	if t == nil {
		return nil
	}
	out := *t
	if t.Postings != nil {
		out.Postings = make([]Posting, len(t.Postings))
		for i := range t.Postings {
			out.Postings[i] = t.Postings[i].Clone()
		}
	}
	return &out
}

// Clone returns a deep copy of the posting. Amount, Cost, and Price
// are deep-cloned via their own Clone methods; Span, Flag, Account,
// and Meta are shared with the receiver.
func (p Posting) Clone() Posting {
	out := p
	out.Amount = p.Amount.Clone()
	out.Cost = p.Cost.Clone()
	out.Price = p.Price.Clone()
	return out
}

// Clone returns a deep copy of c. PerUnit, Total, and Date are
// deep-cloned (a fresh time.Time pointer is allocated for Date);
// Span and Label are shared with the receiver. Returns nil if c is
// nil.
func (c *CostSpec) Clone() *CostSpec {
	if c == nil {
		return nil
	}
	out := *c
	out.PerUnit = c.PerUnit.Clone()
	out.Total = c.Total.Clone()
	if c.Date != nil {
		d := *c.Date
		out.Date = &d
	}
	return &out
}

// Clone returns a deep copy of a. The Number decimal is copied via
// apd.Decimal.Set so the clone owns its coefficient buffer; Currency
// is shared with the receiver. Returns nil if a is nil.
func (a *Amount) Clone() *Amount {
	if a == nil {
		return nil
	}
	out := Amount{Currency: a.Currency}
	out.Number.Set(&a.Number)
	return &out
}

// Clone returns a deep copy of p. The embedded Amount is deep-cloned
// via Amount.Clone; Span and IsTotal are shared with the receiver.
// Returns nil if p is nil.
func (p *PriceAnnotation) Clone() *PriceAnnotation {
	if p == nil {
		return nil
	}
	out := *p
	out.Amount = *p.Amount.Clone()
	return &out
}

// Clone returns a deep copy of b. The Amount is deep-cloned via
// Amount.Clone; if Tolerance is non-nil, a fresh apd.Decimal is
// allocated so the clone does not alias the receiver's coefficient
// buffer. Span, Date, Account, and Meta are shared with the receiver.
// Returns nil if b is nil.
func (b *Balance) Clone() *Balance {
	if b == nil {
		return nil
	}
	out := *b
	out.Amount = *b.Amount.Clone()
	if b.Tolerance != nil {
		t := new(apd.Decimal)
		t.Set(b.Tolerance)
		out.Tolerance = t
	}
	return &out
}
