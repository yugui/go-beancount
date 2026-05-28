package ast

import (
	"time"

	"github.com/cockroachdb/apd/v3"
)

// DirectiveKind assigns a canonical same-day processing priority to each
// directive type. Lower values sort earlier.
//
// Beancount processes same-day directives in a specific order so that, for
// example, a balance assertion is evaluated against the opening balance of
// the day (before any transactions posted that day). The order used here
// matches Beancount's canonical order:
//
//  1. open
//  2. balance
//  3. pad
//  4. transaction
//  5. note / document / event / commodity / query / custom
//  6. close
//  7. price
//
// Directives without a date (option, plugin, include) use KindFileHeader and
// sort before dated directives via their zero DirDate.
type DirectiveKind int

const (
	// KindFileHeader covers directives without an intrinsic date
	// (option, plugin, include).
	KindFileHeader DirectiveKind = iota
	// KindOpen is the canonical kind for account opening directives.
	KindOpen
	// KindBalance is the canonical kind for balance assertions.
	KindBalance
	// KindPad is the canonical kind for pad directives.
	KindPad
	// KindTransaction is the canonical kind for transactions.
	KindTransaction
	// KindOther covers directives (note, document, event, commodity,
	// query, custom) that share an ordering slot between transactions
	// and close directives.
	KindOther
	// KindClose is the canonical kind for account close directives.
	KindClose
	// KindPrice is the canonical kind for price directives.
	KindPrice
)

// Directive is the interface implemented by all AST directive types.
//
// Every Directive carries the metadata needed to place it in canonical
// order: a source span, a directive kind, and an effective date (zero for
// header directives). Because directive() is unexported, the interface can
// only be satisfied by types defined in this package, which keeps the kind
// and date contracts closed to external extension.
type Directive interface {
	directive()             // marker method
	DirSpan() Span          // DirSpan returns the source span of the directive.
	DirKind() DirectiveKind // DirKind returns the canonical kind for ordering.
	DirDate() time.Time     // DirDate returns the effective date, or zero for header directives.
	DirMeta() Metadata      // DirMeta returns the attached metadata, or the empty Metadata for header directives.
}

// Posting represents a posting within a transaction.
//
// Cost is a [CostHolder] (sealed union): either *[CostSpec] for the
// parse-tier form (the lowerer always installs this) or *[Cost] for
// the fully-booked form (installed by the inventory reducer's
// terminal pass). nil means the posting carries no cost annotation.
// Sites that need write access (e.g. reducer mutators) type-assert
// to *[CostSpec]; sites that only read use the [CostHolder] getters.
type Posting struct {
	Span    Span
	Flag    byte // '*', '!', or 0 if not specified
	Account Account
	Amount  *Amount          // nil if not specified (auto-balanced posting)
	Cost    CostHolder       // nil if no cost annotation; see Posting doc
	Price   *PriceAnnotation // nil if no price annotation
	Meta    Metadata
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

func (o *Option) directive()             {}
func (o *Option) DirSpan() Span          { return o.Span }
func (o *Option) DirKind() DirectiveKind { return KindFileHeader }
func (o *Option) DirDate() time.Time     { return time.Time{} }
func (o *Option) DirMeta() Metadata      { return Metadata{} }

// Plugin represents a plugin directive: plugin "name" ["config"]
type Plugin struct {
	Span   Span
	Name   string
	Config string // empty if not provided
}

func (p *Plugin) directive()             {}
func (p *Plugin) DirSpan() Span          { return p.Span }
func (p *Plugin) DirKind() DirectiveKind { return KindFileHeader }
func (p *Plugin) DirDate() time.Time     { return time.Time{} }
func (p *Plugin) DirMeta() Metadata      { return Metadata{} }

// Include represents an include directive: include "path"
// Include resolution is not performed at this layer.
type Include struct {
	Span Span
	Path string
}

func (i *Include) directive()             {}
func (i *Include) DirSpan() Span          { return i.Span }
func (i *Include) DirKind() DirectiveKind { return KindFileHeader }
func (i *Include) DirDate() time.Time     { return time.Time{} }
func (i *Include) DirMeta() Metadata      { return Metadata{} }

// Open represents an open directive: YYYY-MM-DD open Account [Currency,...] ["BookingMethod"]
type Open struct {
	Span       Span
	Date       time.Time
	Account    Account
	Currencies []string // optional constraint currencies
	// Booking is the typed booking method parsed from the optional
	// "STRICT"/"FIFO"/... keyword. The zero value BookingDefault
	// indicates that the source directive did not specify a booking
	// keyword, in which case consumers should fall back to the ledger's
	// configured default. Invalid keywords are rejected at parse time
	// by the lowerer, so this field always holds a valid enum value.
	Booking BookingMethod
	Meta    Metadata
}

func (o *Open) directive()                {}
func (o *Open) DirSpan() Span             { return o.Span }
func (o *Open) DirKind() DirectiveKind    { return KindOpen }
func (o *Open) DirDate() time.Time        { return o.Date }
func (o *Open) DirMeta() Metadata         { return o.Meta }
func (o *Open) directiveAccount() Account { return o.Account }

// Close represents a close directive: YYYY-MM-DD close Account
type Close struct {
	Span    Span
	Date    time.Time
	Account Account
	Meta    Metadata
}

func (c *Close) directive()                {}
func (c *Close) DirSpan() Span             { return c.Span }
func (c *Close) DirKind() DirectiveKind    { return KindClose }
func (c *Close) DirDate() time.Time        { return c.Date }
func (c *Close) DirMeta() Metadata         { return c.Meta }
func (c *Close) directiveAccount() Account { return c.Account }

// Commodity represents a commodity directive: YYYY-MM-DD commodity Currency
type Commodity struct {
	Span     Span
	Date     time.Time
	Currency string
	Meta     Metadata
}

func (c *Commodity) directive()             {}
func (c *Commodity) DirSpan() Span          { return c.Span }
func (c *Commodity) DirKind() DirectiveKind { return KindOther }
func (c *Commodity) DirDate() time.Time     { return c.Date }
func (c *Commodity) DirMeta() Metadata      { return c.Meta }

// Balance represents a balance assertion:
// YYYY-MM-DD balance Account Number [~ Number] Currency
//
// Tolerance, when non-nil, shares Amount.Currency; the tolerance number has
// no independent currency in Beancount's real syntax.
type Balance struct {
	Span      Span
	Date      time.Time
	Account   Account
	Amount    Amount
	Tolerance *apd.Decimal // optional; nil if not specified
	Meta      Metadata
}

func (b *Balance) directive()                {}
func (b *Balance) DirSpan() Span             { return b.Span }
func (b *Balance) DirKind() DirectiveKind    { return KindBalance }
func (b *Balance) DirDate() time.Time        { return b.Date }
func (b *Balance) DirMeta() Metadata         { return b.Meta }
func (b *Balance) directiveAccount() Account { return b.Account }

// Pad represents a pad directive: YYYY-MM-DD pad Account PadAccount
type Pad struct {
	Span       Span
	Date       time.Time
	Account    Account
	PadAccount Account
	Meta       Metadata
}

func (p *Pad) directive()                {}
func (p *Pad) DirSpan() Span             { return p.Span }
func (p *Pad) DirKind() DirectiveKind    { return KindPad }
func (p *Pad) DirDate() time.Time        { return p.Date }
func (p *Pad) DirMeta() Metadata         { return p.Meta }
func (p *Pad) directiveAccount() Account { return p.Account }

// Note represents a note directive: YYYY-MM-DD note Account "comment" [tags/links]
//
// Tags and Links carry any explicit `#tag` and `^link` tokens that appear
// after the comment string. Active tags from enclosing pushtag/poptag scopes
// are merged into Tags during lowering, mirroring the behavior of upstream
// beancount.
type Note struct {
	Span    Span
	Date    time.Time
	Account Account
	Comment string
	Tags    []string // e.g., ["trip-2024"] (without # prefix)
	Links   []string // e.g., ["invoice-123"] (without ^ prefix)
	Meta    Metadata
}

func (n *Note) directive()                {}
func (n *Note) DirSpan() Span             { return n.Span }
func (n *Note) DirKind() DirectiveKind    { return KindOther }
func (n *Note) DirDate() time.Time        { return n.Date }
func (n *Note) DirMeta() Metadata         { return n.Meta }
func (n *Note) directiveAccount() Account { return n.Account }

// Document represents a document directive: YYYY-MM-DD document Account "path" [tags/links]
//
// Tags and Links carry any explicit `#tag` and `^link` tokens that appear
// after the path string. Active tags from enclosing pushtag/poptag scopes
// are merged into Tags during lowering, mirroring the behavior of upstream
// beancount.
type Document struct {
	Span    Span
	Date    time.Time
	Account Account
	Path    string
	Tags    []string // e.g., ["trip-2024"] (without # prefix)
	Links   []string // e.g., ["invoice-123"] (without ^ prefix)
	Meta    Metadata
}

func (d *Document) directive()                {}
func (d *Document) DirSpan() Span             { return d.Span }
func (d *Document) DirKind() DirectiveKind    { return KindOther }
func (d *Document) DirDate() time.Time        { return d.Date }
func (d *Document) DirMeta() Metadata         { return d.Meta }
func (d *Document) directiveAccount() Account { return d.Account }

// Event represents an event directive: YYYY-MM-DD event "name" "value"
type Event struct {
	Span  Span
	Date  time.Time
	Name  string
	Value string
	Meta  Metadata
}

func (e *Event) directive()             {}
func (e *Event) DirSpan() Span          { return e.Span }
func (e *Event) DirKind() DirectiveKind { return KindOther }
func (e *Event) DirDate() time.Time     { return e.Date }
func (e *Event) DirMeta() Metadata      { return e.Meta }

// Query represents a query directive: YYYY-MM-DD query "name" "bql"
type Query struct {
	Span Span
	Date time.Time
	Name string
	BQL  string
	Meta Metadata
}

func (q *Query) directive()             {}
func (q *Query) DirSpan() Span          { return q.Span }
func (q *Query) DirKind() DirectiveKind { return KindOther }
func (q *Query) DirDate() time.Time     { return q.Date }
func (q *Query) DirMeta() Metadata      { return q.Meta }

// Price represents a price directive: YYYY-MM-DD price Commodity Amount
type Price struct {
	Span      Span
	Date      time.Time
	Commodity string // the base commodity (CURRENCY token)
	Amount    Amount // the price amount (number + quote currency)
	Meta      Metadata
}

func (p *Price) directive()             {}
func (p *Price) DirSpan() Span          { return p.Span }
func (p *Price) DirKind() DirectiveKind { return KindPrice }
func (p *Price) DirDate() time.Time     { return p.Date }
func (p *Price) DirMeta() Metadata      { return p.Meta }

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

func (t *Transaction) directive()             {}
func (t *Transaction) DirSpan() Span          { return t.Span }
func (t *Transaction) DirKind() DirectiveKind { return KindTransaction }
func (t *Transaction) DirDate() time.Time     { return t.Date }
func (t *Transaction) DirMeta() Metadata      { return t.Meta }

// Custom represents a custom directive: YYYY-MM-DD custom "type" Value...
type Custom struct {
	Span     Span
	Date     time.Time
	TypeName string
	Values   []MetaValue // heterogeneous value list (reuses MetaValue)
	Meta     Metadata
}

func (c *Custom) directive()             {}
func (c *Custom) DirSpan() Span          { return c.Span }
func (c *Custom) DirKind() DirectiveKind { return KindOther }
func (c *Custom) DirDate() time.Time     { return c.Date }
func (c *Custom) DirMeta() Metadata      { return c.Meta }
