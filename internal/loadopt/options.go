// Package loadopt defines options for loading beancount source.
package loadopt

// DefaultVirtualFilename is the filename recorded in spans when the source has
// no real file path (Load / LoadReader without WithFilename).
const DefaultVirtualFilename = "<input>"

// Options controls how beancount source is loaded.
type Options struct {
	// BaseDir is the directory used to resolve relative include paths and
	// (via VirtualFilename) document directive paths. An empty BaseDir means
	// no base directory has been configured: relative includes will produce
	// a diagnostic and be skipped.
	BaseDir string
	// VirtualFilename is the filename recorded in span positions for input
	// that has no real file path. Defaults to DefaultVirtualFilename.
	VirtualFilename string
}

// Default returns Options with sensible defaults.
func Default() Options {
	return Options{VirtualFilename: DefaultVirtualFilename}
}

// Resolve starts from Default(), applies each option func in order, and
// returns the result.
func Resolve(opts []func(*Options)) Options {
	o := Default()
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
