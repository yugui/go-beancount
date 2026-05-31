// Package goplugflag is the command-line and environment-variable front end
// for selecting goplug plugins.
//
// The go-beancount commands that accept out-of-tree postprocessors, quoters,
// or importers (beancheck, beancount-lsp, beanprice, beanimport) all let an
// operator name goplug .so files two ways: a repeatable -plugin flag and the
// BEANCOUNT_PLUGINS environment variable. [Var] registers that flag and merges
// it with the environment into a single ordered list; the resulting paths are
// handed to [github.com/yugui/go-beancount/pkg/ext/goplug.LoadAll] to load.
//
// The environment variable matters most for beancount-lsp, which is launched
// by an editor or IDE rather than typed at a shell: the editor configures the
// server's environment, not its argv.
package goplugflag
