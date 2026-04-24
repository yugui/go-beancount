package ast

// LoadOption configures a Load, LoadReader, or LoadFile call.
type LoadOption = func(*loadOptions)

// loadOptions is the resolved option bundle threaded through the loader.
type loadOptions struct {
	filename string
}

// WithFilename attaches a synthetic source path to inline source loaded via
// Load or LoadReader. The directory of path becomes the base for resolving
// include directives, and the same path is recorded on the resulting
// File.Filename and on every Span.Start.Filename.
//
// WithFilename has no effect on LoadFile, where the on-disk path is always
// authoritative.
func WithFilename(path string) LoadOption {
	return func(o *loadOptions) { o.filename = path }
}

func resolveLoadOptions(opts []LoadOption) loadOptions {
	var o loadOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
