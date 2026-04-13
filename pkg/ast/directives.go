package ast

import (
	"time"

	"github.com/cockroachdb/apd/v3"
)

// Posting represents a posting within a transaction.
type Posting struct {
	Span    Span
	Flag    byte // '*', '!', or 0 if not specified
	Account string
	Amount  *Amount          // nil if not specified (auto-balanced posting)
	Cost    *CostSpec        // nil if no cost spec
	Price   *PriceAnnotation // nil if no price annotation
	Meta    Metadata
}

// CostSpec represents a cost specification on a posting.
//
// PerUnit and Total carry distinct, non-overlapping meanings; there is no
// disambiguation flag. The mapping from source syntax is:
//
//	{X CUR}            -> PerUnit=X,    Total=nil
//	{{X CUR}}          -> PerUnit=nil,  Total=X
//	{X # Y CUR}        -> PerUnit=X,    Total=Y      (combined form, future)
//	{} or {{}}         -> PerUnit=nil,  Total=nil    (empty)
//
// In the combined form (both non-nil) both components share the same
// currency. The empty form is normalized to "{}" on print; "{{}}" does not
// round-trip byte-for-byte.
type CostSpec struct {
	Span    Span
	PerUnit *Amount    // per-unit cost component; nil if absent
	Total   *Amount    // total / surcharge cost component; nil if absent
	Date    *time.Time // optional acquisition date
	Label   string     // optional lot label; empty if not specified
}

// PriceAnnotation represents a price annotation on a posting.
type PriceAnnotation struct {
	Span    Span
	Amount  Amount
	IsTotal bool // true if @@ (total price), false if @ (per-unit price)
}

// Option represents an option directive: option "key" "value"
type Option struct {
	Span  Span
	Key   string
	Value string
}

func (o *Option) directive()    {}
func (o *Option) DirSpan() Span { return o.Span }

// Plugin represents a plugin directive: plugin "name" ["config"]
type Plugin struct {
	Span   Span
	Name   string
	Config string // empty if not provided
}

func (p *Plugin) directive()    {}
func (p *Plugin) DirSpan() Span { return p.Span }

// Include represents an include directive: include "path"
// Include resolution is not performed at this layer.
type Include struct {
	Span Span
	Path string
}

func (i *Include) directive()    {}
func (i *Include) DirSpan() Span { return i.Span }

// Open represents an open directive: YYYY-MM-DD open Account [Currency,...] ["BookingMethod"]
type Open struct {
	Span       Span
	Date       time.Time
	Account    string
	Currencies []string // optional constraint currencies
	Booking    string   // optional booking method (e.g. "STRICT", "NONE"); empty if not provided
	Meta       Metadata
}

func (o *Open) directive()    {}
func (o *Open) DirSpan() Span { return o.Span }

// Close represents a close directive: YYYY-MM-DD close Account
type Close struct {
	Span    Span
	Date    time.Time
	Account string
	Meta    Metadata
}

func (c *Close) directive()    {}
func (c *Close) DirSpan() Span { return c.Span }

// Commodity represents a commodity directive: YYYY-MM-DD commodity Currency
type Commodity struct {
	Span     Span
	Date     time.Time
	Currency string
	Meta     Metadata
}

func (c *Commodity) directive()    {}
func (c *Commodity) DirSpan() Span { return c.Span }

// Balance represents a balance assertion:
// YYYY-MM-DD balance Account Number [~ Number] Currency
//
// Tolerance, when non-nil, shares Amount.Currency; the tolerance number has
// no independent currency in Beancount's real syntax.
type Balance struct {
	Span      Span
	Date      time.Time
	Account   string
	Amount    Amount
	Tolerance *apd.Decimal // optional; nil if not specified
	Meta      Metadata
}

func (b *Balance) directive()    {}
func (b *Balance) DirSpan() Span { return b.Span }

// Pad represents a pad directive: YYYY-MM-DD pad Account PadAccount
type Pad struct {
	Span       Span
	Date       time.Time
	Account    string
	PadAccount string
	Meta       Metadata
}

func (p *Pad) directive()    {}
func (p *Pad) DirSpan() Span { return p.Span }

// Note represents a note directive: YYYY-MM-DD note Account "comment"
type Note struct {
	Span    Span
	Date    time.Time
	Account string
	Comment string
	Meta    Metadata
}

func (n *Note) directive()    {}
func (n *Note) DirSpan() Span { return n.Span }

// Document represents a document directive: YYYY-MM-DD document Account "path"
type Document struct {
	Span    Span
	Date    time.Time
	Account string
	Path    string
	Meta    Metadata
}

func (d *Document) directive()    {}
func (d *Document) DirSpan() Span { return d.Span }

// Event represents an event directive: YYYY-MM-DD event "name" "value"
type Event struct {
	Span  Span
	Date  time.Time
	Name  string
	Value string
	Meta  Metadata
}

func (e *Event) directive()    {}
func (e *Event) DirSpan() Span { return e.Span }

// Query represents a query directive: YYYY-MM-DD query "name" "bql"
type Query struct {
	Span Span
	Date time.Time
	Name string
	BQL  string
	Meta Metadata
}

func (q *Query) directive()    {}
func (q *Query) DirSpan() Span { return q.Span }

// Price represents a price directive: YYYY-MM-DD price Commodity Amount
type Price struct {
	Span      Span
	Date      time.Time
	Commodity string // the base commodity (CURRENCY token)
	Amount    Amount // the price amount (number + quote currency)
	Meta      Metadata
}

func (p *Price) directive()    {}
func (p *Price) DirSpan() Span { return p.Span }

// Transaction represents a transaction directive.
type Transaction struct {
	Span      Span
	Date      time.Time
	Flag      byte     // '*' or '!'
	Payee     string   // empty if not specified
	Narration string   // empty if not specified
	Tags      []string // e.g., ["trip-2024"] (without # prefix)
	Links     []string // e.g., ["invoice-123"] (without ^ prefix)
	Postings  []Posting
	Meta      Metadata
}

func (t *Transaction) directive()    {}
func (t *Transaction) DirSpan() Span { return t.Span }

// Custom represents a custom directive: YYYY-MM-DD custom "type" Value...
type Custom struct {
	Span     Span
	Date     time.Time
	TypeName string
	Values   []MetaValue // heterogeneous value list (reuses MetaValue)
	Meta     Metadata
}

func (c *Custom) directive()    {}
func (c *Custom) DirSpan() Span { return c.Span }
