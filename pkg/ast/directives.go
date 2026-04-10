package ast

import "time"

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

// Balance represents a balance assertion: YYYY-MM-DD balance Account Amount [~ Tolerance]
type Balance struct {
	Span      Span
	Date      time.Time
	Account   string
	Amount    Amount
	Tolerance *Amount // optional; nil if not specified
	Meta      Metadata
}

func (b *Balance) directive()    {}
func (b *Balance) DirSpan() Span { return b.Span }
