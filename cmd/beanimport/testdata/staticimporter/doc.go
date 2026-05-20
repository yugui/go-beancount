// Package main is a goplug plugin fixture used by cmd/beanimport's
// integration tests to verify the --plugin loader path
// (goplug.Load -> InitPlugin -> importer.RegisterFactory) end-to-end.
// It registers an importer kind named by this plugin's fully-qualified
// Go import path (matching pkg/ext/postproc/std/*'s convention for
// uniqueness across third-party plugins) that returns a single canned
// Transaction for every input, regardless of file content.
//
// The fixture is hosted under cmd/beanimport/testdata rather than
// pkg/importer/std because the latter is reserved for in-tree importers
// that get statically linked into beanimport; mixing a .so fixture
// there would conflate the two roles. Plugin authors writing their
// own out-of-tree importer can read plugin.go in this directory as
// a reference: it exercises every required symbol (Manifest, InitPlugin)
// and the importer.RegisterFactory call from InitPlugin.
//
// This is NOT a production importer.
package main
