package loader

import "github.com/yugui/go-beancount/pkg/ast"

// Option re-exports ast.LoadOption so callers configuring a load do not need
// to import pkg/ast separately.
type Option = ast.LoadOption

// WithBaseDir sets the directory used to resolve relative include paths and
// document directives.
func WithBaseDir(dir string) Option { return ast.WithBaseDir(dir) }

// WithFilename sets the virtual filename recorded in span positions for
// input that has no real file path.
func WithFilename(name string) Option { return ast.WithFilename(name) }
