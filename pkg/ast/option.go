package ast

import "github.com/yugui/go-beancount/internal/loadopt"

// LoadOption configures Load, LoadReader, and LoadFile.
type LoadOption = func(*loadopt.Options)

// WithBaseDir sets the directory used to resolve relative include paths.
// When unset, relative include directives produce a diagnostic and are
// skipped; absolute include paths still work. LoadFile defaults BaseDir to
// the directory of the file being loaded.
func WithBaseDir(dir string) LoadOption {
	return func(o *loadopt.Options) { o.BaseDir = dir }
}

// WithFilename sets the filename recorded in span positions. It is used in
// diagnostic messages and as the anchor for resolving relative paths in
// document directives. Defaults to "<input>". LoadFile defaults to the
// absolute path of the file being loaded.
func WithFilename(name string) LoadOption {
	return func(o *loadopt.Options) { o.VirtualFilename = name }
}

// WithOverlay supplies in-memory source bytes that take precedence over
// disk for matching absolute paths during Load, LoadReader, and LoadFile.
//
// Keys must be absolute paths in the OS-native form (filepath.IsAbs);
// non-absolute keys are ignored and produce a Warning diagnostic with
// Code "overlay-non-absolute-key". A nil or empty map is a no-op.
//
// The map and its []byte values are borrowed for the duration of the
// load; the caller must not mutate them until the Load* call returns.
// Passing WithOverlay multiple times replaces the previous overlay
// (last-wins). Composes orthogonally with WithBaseDir and WithFilename.
func WithOverlay(overlay map[string][]byte) LoadOption {
	return func(o *loadopt.Options) { o.Overlay = overlay }
}
