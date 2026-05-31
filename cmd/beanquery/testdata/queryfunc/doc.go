// Package main is a goplug plugin fixture used by cmd/beanquery's
// integration tests to verify the -plugin loader path
// (goplug.Load -> InitPlugin -> env.Register) end-to-end for BQL query
// functions. It registers a context-free scalar named "fixture_marker"
// that prefixes its string argument with "FIXTURE-".
//
// The fixture is hosted under cmd/beanquery/testdata rather than
// pkg/query/env/std because the latter is reserved for the in-tree
// parity library that gets statically linked into beanquery; mixing a
// .so fixture there would conflate the two roles. Plugin authors
// writing their own out-of-tree query functions can read plugin.go in
// this directory as the canonical reference: it exercises every
// required symbol (Manifest, InitPlugin) and the env.Register call from
// InitPlugin, which is identical in shape to how a plugin would instead
// (or additionally) call pkg/ext/postproc.Register to ship a
// postprocessor through the same flag.
//
// This is NOT a production query function.
package main
