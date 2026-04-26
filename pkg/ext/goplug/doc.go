// Package goplug loads beancount postprocessor plugins from Go plugin
// shared objects (.so files) built with `go build -buildmode=plugin`.
//
// # Contract
//
// A plugin .so MUST export two symbols:
//
//   - Manifest — a value of type [Manifest] carrying the plugin's
//     metadata. Read by the loader before any plugin code runs so an
//     incompatible plugin is rejected without side effects.
//   - InitPlugin — a function of type func() error. Invoked after the
//     Manifest check passes. Its responsibility is to register the
//     plugin's [api.Plugin] implementation(s) via
//     [github.com/yugui/go-beancount/pkg/ext/postproc.Register]. Returning
//     a non-nil error aborts the load.
//
// Because Go plugins share the host process's runtime and linked
// packages, a call to postproc.Register inside the plugin mutates the
// same registry the host reads from — there is no separate per-plugin
// registry.
//
// # Build constraints
//
// Plugins must be built with:
//
//   - The same Go toolchain version as the host binary.
//   - The same module dependency graph — including the same version of
//     github.com/yugui/go-beancount — as the host binary.
//   - GOOS supporting Go plugins: linux, freebsd, or darwin.
//   - cgo enabled (plugin mode requires it).
//
// Go's standard library [plugin.Open] enforces build-graph parity by
// rejecting a .so whose package build IDs don't match the host's. This
// package additionally checks [APIVersion] against the plugin-supplied
// value so operators get a clearer message when the goplug contract
// itself evolves incompatibly.
//
// # Example
//
// A minimal plugin looks like this:
//
//	package main
//
//	import (
//	    "context"
//
//	    "github.com/yugui/go-beancount/pkg/ext/postproc"
//	    "github.com/yugui/go-beancount/pkg/ext/postproc/api"
//	    "github.com/yugui/go-beancount/pkg/ext/goplug"
//	)
//
//	var Manifest = goplug.Manifest{
//	    APIVersion: goplug.APIVersion,
//	    Name:       "github.com/example/hello",
//	    Version:    "v0.1.0",
//	}
//
//	var plug api.PluginFunc = func(ctx context.Context, in api.Input) (api.Result, error) {
//	    return api.Result{}, nil
//	}
//
//	func InitPlugin() error {
//	    postproc.Register("github.com/example/hello", plug)
//	    return nil
//	}
//
// Build it with:
//
//	go build -buildmode=plugin -o hello.so ./path/to/plugin
//
// And load it from the host:
//
//	if err := goplug.Load("hello.so"); err != nil { ... }
//	if err := postproc.Apply(ctx, ledger); err != nil { ... } // plugin runs
//	                                                          // when its directive
//	                                                          // is encountered;
//	                                                          // findings land in
//	                                                          // ledger.Diagnostics.
package goplug
