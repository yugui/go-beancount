package ast

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// CloneDecimal returns a freshly allocated deep copy of x. The returned
// pointer owns its own storage; the caller may discard or mutate the
// source after the call.
func CloneDecimal(x *apd.Decimal) *apd.Decimal {
	out := new(apd.Decimal)
	out.Set(x)
	return out
}

// This file provides Clone methods for AST node types whose nested
// pointers and apd.Decimal values would otherwise alias the original
// when copied via plain struct assignment. The deep/shallow boundary
// is deliberate:
//
//   - Postings are deep-cloned: a Transaction's Postings slice is
//     reallocated, and each Posting's Amount, Cost, and Price pointers
//     are deep-copied (including their nested apd.Decimal values and
//     CostSpec.Date) so a clone may be mutated freely.
//   - Tags and Links are shared by convention. The codebase treats these
//     as append-only after construction; no consumer mutates them in place.
//   - Meta is deep-cloned: a fresh Metadata.Props map is allocated so
//     callers that strip routing keys before emit do not alias the
//     original AST's metadata.
//   - Span, Date, Flag, Payee, Narration, Account, Currency, Label
//     are scalars or small value types; they are copied by value.
//
// Each Clone method on a pointer receiver is nil-safe: calling Clone
// on a nil pointer returns nil. This lets callers chain clones across
// optional fields without nil checks.

// Clone returns a deep copy of t. The Postings slice is reallocated and
// each Posting deep-cloned. Meta is deep-cloned (fresh Props map) so the
// clone may have metadata keys stripped without aliasing the original.
// Tags and Links are shared with the receiver. Returns nil if t is nil.
func (t *Transaction) Clone() *Transaction {
	if t == nil {
		return nil
	}
	out := *t
	out.Meta = CloneMeta(t.Meta)
	if t.Postings != nil {
		out.Postings = make([]Posting, len(t.Postings))
		for i := range t.Postings {
			out.Postings[i] = t.Postings[i].Clone()
		}
	}
	return &out
}

// Clone returns a deep copy of the posting. Amount, Cost, and Price are
// deep-cloned via their own Clone methods. Meta is deep-cloned (fresh
// Props map) so the clone may have metadata keys stripped without aliasing
// the original. Span, Flag, and Account are copied by value.
func (p Posting) Clone() Posting {
	out := p
	out.Meta = CloneMeta(p.Meta)
	out.Amount = p.Amount.Clone()
	out.Cost = cloneCostHolder(p.Cost)
	out.Price = p.Price.Clone()
	return out
}

// cloneCostHolder dispatches deep-clone over the [CostHolder] sealed
// union. The switch is exhaustive: only *[CostSpec] and *[Cost]
// satisfy the interface (enforced by the unexported isCostHolder
// marker), so an unknown concrete type indicates a programming error
// in this package.
func cloneCostHolder(h CostHolder) CostHolder {
	switch c := h.(type) {
	case nil:
		return nil
	case *CostSpec:
		return c.Clone()
	case *Cost:
		return c.Clone()
	default:
		panic(fmt.Sprintf("ast: unknown CostHolder concrete type %T", h))
	}
}

// Clone returns a deep copy of c. PerUnit, Total, and Date are
// deep-cloned (fresh apd.Decimal and time.Time allocations); Span,
// Currency, and Label are copied by value. Returns nil if c is nil.
func (c *CostSpec) Clone() *CostSpec {
	if c == nil {
		return nil
	}
	out := *c
	if c.PerUnit != nil {
		out.PerUnit = CloneDecimal(c.PerUnit)
	}
	if c.Total != nil {
		out.Total = CloneDecimal(c.Total)
	}
	if c.Date != nil {
		d := *c.Date
		out.Date = &d
	}
	return &out
}

// Clone returns a deep copy of c. It is nil-safe: calling Clone on a
// nil receiver returns nil, which matches the convention used by
// Position for the optional Cost field.
func (c *Cost) Clone() *Cost {
	if c == nil {
		return nil
	}
	return &Cost{
		Number:   *CloneDecimal(&c.Number),
		Currency: c.Currency,
		Date:     c.Date,
		Label:    c.Label,
		PerUnit:  c.PerUnit.Clone(),
		Total:    c.Total.Clone(),
	}
}

// Clone returns a deep copy of a. The Number decimal is copied via
// CloneDecimal so the clone owns its coefficient buffer; Currency
// is shared with the receiver. Returns nil if a is nil.
func (a *Amount) Clone() *Amount {
	if a == nil {
		return nil
	}
	return &Amount{Number: *CloneDecimal(&a.Number), Currency: a.Currency}
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
		out.Tolerance = CloneDecimal(b.Tolerance)
	}
	return &out
}

// CloneMeta returns a fresh Metadata whose Props map is a shallow copy of
// src.Props. The values themselves (MetaValue structs) are copied by value;
// the map is a new allocation so mutations do not alias the original.
func CloneMeta(src Metadata) Metadata {
	if src.Props == nil {
		return Metadata{}
	}
	out := Metadata{Props: make(map[string]MetaValue, len(src.Props))}
	for k, v := range src.Props {
		out.Props[k] = v
	}
	return out
}

// Clone returns a shallow copy of o with scalar and shared fields preserved.
// Meta is shared by convention. Currencies is also shared: the slice header
// is copied by value so the clone aliases the same backing array. Returns nil
// if o is nil.
func (o *Open) Clone() *Open {
	if o == nil {
		return nil
	}
	out := *o
	return &out
}

// Clone returns a shallow copy of c. Meta is shared by convention.
// Returns nil if c is nil.
func (c *Close) Clone() *Close {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}

// Clone returns a shallow copy of p. Meta is shared by convention.
// Returns nil if p is nil.
func (p *Pad) Clone() *Pad {
	if p == nil {
		return nil
	}
	out := *p
	return &out
}

// Clone returns a shallow copy of n. Tags, Links, and Meta are shared by
// convention. Returns nil if n is nil.
func (n *Note) Clone() *Note {
	if n == nil {
		return nil
	}
	out := *n
	return &out
}

// Clone returns a shallow copy of d. Tags, Links, and Meta are shared by
// convention. Returns nil if d is nil.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	out := *d
	return &out
}

// Clone returns a shallow copy of p. Amount is copied by value (embedded
// struct, not pointer). Meta is shared by convention.
// Returns nil if p is nil.
func (p *Price) Clone() *Price {
	if p == nil {
		return nil
	}
	out := *p
	return &out
}

// Clone returns a shallow copy of e. Meta is shared by convention.
// Returns nil if e is nil.
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	out := *e
	return &out
}

// Clone returns a shallow copy of q. Meta is shared by convention.
// Returns nil if q is nil.
func (q *Query) Clone() *Query {
	if q == nil {
		return nil
	}
	out := *q
	return &out
}

// Clone returns a shallow copy of c. Values and Meta are shared by
// convention. Returns nil if c is nil.
func (c *Custom) Clone() *Custom {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}

// Clone returns a shallow copy of c. Meta is shared by convention.
// Returns nil if c is nil.
func (c *Commodity) Clone() *Commodity {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}
