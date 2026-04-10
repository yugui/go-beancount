package ast

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
