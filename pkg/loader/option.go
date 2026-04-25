package loader

import "github.com/yugui/go-beancount/pkg/ast"

// Option re-exports ast.LoadOption so callers configuring a load do not need to
// import pkg/ast separately.
type Option = ast.LoadOption

// WithBaseDir re-exports ast.WithBaseDir.
var WithBaseDir = ast.WithBaseDir

// WithFilename re-exports ast.WithFilename.
var WithFilename = ast.WithFilename
