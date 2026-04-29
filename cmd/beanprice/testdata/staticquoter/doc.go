// Package main is a goplug plugin fixture used by cmd/beanprice's
// integration tests to verify the --plugin loader path
// (goplug.Load -> InitPlugin -> quote.Register) end-to-end. It
// registers a quoter named "staticquoter" that returns Number=1 for
// every query, regardless of pair, mode, or date.
//
// The fixture is hosted under cmd/beanprice/testdata rather than
// pkg/quote/std because the latter is reserved for in-tree quoters
// that get statically linked into beanprice; mixing a .so fixture
// there would conflate the two roles. Plugin authors writing their
// own out-of-tree quoter can read plugin.go in this directory as
// the canonical reference: it exercises every required symbol
// (Manifest, InitPlugin) and the quote.Register call from
// InitPlugin.
//
// This is NOT a production quote source.
package main
